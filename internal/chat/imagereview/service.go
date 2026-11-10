// Package imagereview implements the read-only inspect_image Agent tool (#245).
// It resolves one picture of the command's session through the same handle
// policy as every other tool, reads its bytes only for the command's owner, and
// reports deterministic facts plus a Schema-declared checklist. It never creates
// a Task, spends credits or changes the session.
package imagereview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

// InputAssetReader opens an input asset (an upload or a render) for its owner.
type InputAssetReader interface {
	OpenOwned(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error)
}

// TaskAssetReader opens one saved asset of a finished Task for its owner.
type TaskAssetReader interface {
	OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error)
}

type Config struct {
	Context catalog.ChatContextSchema
	Review  catalog.ChatImageReviewSchema
	Inputs  InputAssetReader
	Tasks   TaskAssetReader
}

type Service struct {
	context catalog.ChatContextSchema
	review  catalog.ChatImageReviewSchema
	inputs  InputAssetReader
	tasks   TaskAssetReader
}

func New(config Config) (*Service, error) {
	if config.Inputs == nil && config.Tasks == nil {
		return nil, errors.New("image review: no asset reader")
	}
	return &Service{context: config.Context, review: config.Review, inputs: config.Inputs, tasks: config.Tasks}, nil
}

// Inspect executes one inspect_image call.
func (service *Service) Inspect(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	var arguments struct {
		Image     string `json:"image"`
		Checklist string `json:"checklist"`
	}
	if err := json.Unmarshal(command.Call.Arguments, &arguments); err != nil || strings.TrimSpace(arguments.Image) == "" {
		return chat.ToolResult{Error: "invalid image", NoSubmission: true}, nil
	}
	checklistName := arguments.Checklist
	if checklistName == "" {
		checklistName = "delivery"
	}
	checklist, checklistText, ok := service.checklist(checklistName)
	if !ok {
		return chat.ToolResult{Error: "unknown checklist", NoSubmission: true}, nil
	}
	used := 0
	for _, call := range command.EarlierCalls {
		if call.Name == "inspect_image" {
			used++
		}
	}
	if service.review.MaxRounds > 0 && used >= service.review.MaxRounds {
		return chat.ToolResult{Error: strings.ReplaceAll(service.review.RoundsExhaustedTemplate,
			"{maximum}", fmt.Sprint(service.review.MaxRounds)), NoSubmission: true}, nil
	}
	policy := chat.NewHandlePolicy(service.context)
	resolved, err := policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection,
		[]string{arguments.Image}, chat.AttachmentKindImage)
	if err != nil {
		var unknown *chat.UnknownHandleError
		if errors.As(err, &unknown) {
			template := service.context.UnknownHandleTemplate
			if unknown.Reason == "kind" {
				template = service.context.WrongHandleKindTemplate
			}
			return chat.ToolResult{Error: strings.ReplaceAll(template, "{handle}", unknown.Handle), NoSubmission: true}, nil
		}
		return chat.ToolResult{Error: "invalid image", NoSubmission: true}, nil
	}
	if len(resolved) != 1 {
		return chat.ToolResult{Error: "invalid image", NoSubmission: true}, nil
	}
	picture := resolved[0]
	data, err := service.read(ctx, command.OwnerID, picture)
	if err != nil {
		message := strings.NewReplacer("{handle}", picture.Attachment.Handle, "{reason}", err.Error()).
			Replace(service.review.UnreadableTemplate)
		return chat.ToolResult{Error: message, NoSubmission: true}, nil
	}
	report, err := imageinspect.Inspect(data, imageinspect.Limits{
		MaxBytes:  service.review.Limits.MaxBytes,
		MaxPixels: service.review.Limits.MaxPixels,
	})
	if err != nil {
		message := strings.NewReplacer("{handle}", picture.Attachment.Handle, "{reason}", err.Error()).
			Replace(service.review.UnreadableTemplate)
		return chat.ToolResult{Error: message, NoSubmission: true}, nil
	}
	checks := report.Review(checklist)
	result := map[string]any{
		"ok": true,
		"image": map[string]any{
			"handle": picture.Attachment.Handle, "task_id": picture.Attachment.TaskID,
			"asset_id": picture.Attachment.AssetID, "width": report.Width, "height": report.Height,
		},
		"report": report,
		"usage_check": map[string]any{
			"checklist": checklistName, "checklist_text": checklistText, "checks": checks,
		},
		"rounds_remaining": max(0, service.review.MaxRounds-used-1),
	}
	content, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "image inspection failed", NoSubmission: true}, nil
	}
	passed, failed := 0, 0
	for _, check := range checks {
		if check.Status == imageinspect.StatusPass {
			passed++
		} else {
			failed++
		}
	}
	summary, _ := json.Marshal(map[string]any{
		"tool": "inspect_image", "handle": picture.Attachment.Handle,
		"width": report.Width, "height": report.Height,
		"checks": map[string]int{"passed": passed, "failed": failed},
	})
	return chat.ToolResult{AgentContent: string(content), Summary: summary, NoSubmission: true}, nil
}

// ReviewAttachment inspects one already-resolved attachment with a named
// checklist. The 3D input gate calls it without going through a tool call.
func (service *Service) ReviewAttachment(ctx context.Context, owner string, attachment chat.Attachment, url, checklistName string) (imageinspect.Report, []imageinspect.Check, error) {
	checklist, _, ok := service.checklist(checklistName)
	if !ok {
		return imageinspect.Report{}, nil, errors.New("unknown checklist")
	}
	data, err := service.read(ctx, owner, chat.ResolvedAttachment{Attachment: attachment, URL: url})
	if err != nil {
		return imageinspect.Report{}, nil, err
	}
	report, err := imageinspect.Inspect(data, imageinspect.Limits{
		MaxBytes:  service.review.Limits.MaxBytes,
		MaxPixels: service.review.Limits.MaxPixels,
	})
	if err != nil {
		return imageinspect.Report{}, nil, err
	}
	return report, report.Review(checklist), nil
}

func (service *Service) checklist(name string) (imageinspect.Checklist, string, bool) {
	switch name {
	case "delivery":
		return checklistFromSchema(service.review.Delivery), service.review.DeliveryChecklist, true
	case "3d_input":
		return checklistFromSchema(service.review.Input3D), service.review.Input3DChecklist, true
	default:
		return imageinspect.Checklist{}, "", false
	}
}

func checklistFromSchema(schema catalog.ChatImageChecklistSchema) imageinspect.Checklist {
	return imageinspect.Checklist{
		RequireAlpha:           schema.RequireAlpha,
		RequirePlainBackground: schema.RequirePlainBackground,
		MaxBackgroundSpread:    schema.MaxBackgroundSpread,
		MinSubjectRatio:        schema.MinSubjectRatio,
		MaxSubjectRatio:        schema.MaxSubjectRatio,
		RequireEdgeClear:       schema.RequireEdgeClear,
		MaxComponents:          schema.MaxComponents,
	}
}

func (service *Service) read(ctx context.Context, owner string, picture chat.ResolvedAttachment) ([]byte, error) {
	limit := service.review.Limits.MaxBytes
	if picture.Attachment.TaskID != "" && service.tasks != nil && picture.Attachment.AssetID != "" {
		content, err := service.tasks.OpenAsset(ctx, generation.OwnerID(owner),
			generation.TaskID(picture.Attachment.TaskID), generation.AssetID(picture.Attachment.AssetID))
		if err != nil {
			return nil, errors.New("asset is not readable")
		}
		defer content.Body.Close()
		return readLimited(content.Body, limit)
	}
	if picture.Attachment.AssetID != "" && service.inputs != nil {
		_, body, err := service.inputs.OpenOwned(ctx, generation.OwnerID(owner), generation.AssetID(picture.Attachment.AssetID))
		if err != nil {
			return nil, errors.New("asset is not readable")
		}
		defer body.Close()
		return readLimited(body, limit)
	}
	if strings.HasPrefix(picture.URL, "data:image/") {
		if comma := strings.Index(picture.URL, ","); comma > 0 && strings.Contains(picture.URL[:comma], ";base64") {
			return base64.StdEncoding.DecodeString(picture.URL[comma+1:])
		}
	}
	return nil, errors.New("picture has no stored asset")
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(reader)
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("picture is over the %d-byte limit", limit)
	}
	return data, nil
}
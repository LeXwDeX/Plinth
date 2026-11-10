// Package imagemask implements the read-only create_selection_mask Agent tool
// (#244). It resolves one picture of the command's session through the same
// handle policy as every other tool, builds a deterministic mask locally (a
// subject split, a rectangle, or the inverse), stores it as the owner's input
// asset and names its URL so refine_image can take it as its mask argument. It
// never calls an upstream model, creates a Task or spends credits.
package imagemask

import (
	"bytes"
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

// InputAssetGateway uploads and opens owned input assets.
type InputAssetGateway interface {
	UploadAsset(context.Context, generation.OwnerID, generation.InputAssetUpload) (generation.InputAsset, error)
	OpenOwned(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error)
}

// TaskAssetGateway opens one saved asset of a finished Task for its owner.
type TaskAssetGateway interface {
	OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error)
}

type Config struct {
	Context          catalog.ChatContextSchema
	Mask             catalog.ChatImageMaskSchema
	Inputs           InputAssetGateway
	Tasks            TaskAssetGateway
	AssetItemRoute   string
	DefaultAssetType string
}

type Service struct {
	context          catalog.ChatContextSchema
	mask             catalog.ChatImageMaskSchema
	inputs           InputAssetGateway
	tasks            TaskAssetGateway
	assetItemRoute   string
	defaultAssetType string
}

func New(config Config) (*Service, error) {
	if config.Inputs == nil || config.Tasks == nil ||
		strings.TrimSpace(config.AssetItemRoute) == "" {
		return nil, errors.New("image mask: incomplete dependencies")
	}
	return &Service{
		context:          config.Context,
		mask:             config.Mask,
		inputs:           config.Inputs,
		tasks:            config.Tasks,
		assetItemRoute:   config.AssetItemRoute,
		defaultAssetType: config.DefaultAssetType,
	}, nil
}

type arguments struct {
	Image  string `json:"image"`
	Mode   string `json:"mode"`
	X0     int    `json:"x0"`
	Y0     int    `json:"y0"`
	X1     int    `json:"x1"`
	Y1     int    `json:"y1"`
	Invert bool   `json:"invert"`
}

// Execute runs one create_selection_mask call.
func (service *Service) Execute(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	var parsed arguments
	if err := json.Unmarshal(command.Call.Arguments, &parsed); err != nil {
		return service.refusal(service.mask.Texts.InvalidRegion), nil
	}
	if strings.TrimSpace(parsed.Image) == "" {
		return service.refusal(strings.ReplaceAll(service.context.UnknownHandleTemplate, "{handle}", parsed.Image)), nil
	}
	policy := chat.NewHandlePolicy(service.context)
	resolved, err := policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection,
		[]string{parsed.Image}, chat.AttachmentKindImage)
	if err != nil {
		var unknown *chat.UnknownHandleError
		if errors.As(err, &unknown) {
			template := service.context.UnknownHandleTemplate
			if unknown.Reason == "kind" {
				template = service.context.WrongHandleKindTemplate
			}
			return service.refusal(strings.ReplaceAll(template, "{handle}", unknown.Handle)), nil
		}
		return service.refusal(service.mask.Texts.InvalidRegion), nil
	}
	if len(resolved) != 1 {
		return service.refusal(service.mask.Texts.InvalidRegion), nil
	}
	picture := resolved[0]
	data, err := service.read(ctx, command.OwnerID, picture)
	if err != nil {
		return service.refusal(strings.NewReplacer(
			"{handle}", picture.Attachment.Handle,
			"{reason}", err.Error(),
		).Replace(service.mask.Texts.UnreadableTemplate)), nil
	}
	limits := imageinspect.Limits{MaxBytes: service.mask.Limits.MaxBytes, MaxPixels: service.mask.Limits.MaxPixels}
	var (
		encoded []byte
		report  imageinspect.MaskReport
	)
	switch parsed.Mode {
	case "subject":
		encoded, report, err = imageinspect.RenderSubjectMask(data, limits)
		if errors.Is(err, imageinspect.ErrNoSubject) {
			return service.refusal(service.mask.Texts.NoSubject), nil
		}
	case "region":
		encoded, report, err = imageinspect.RenderRegionMask(data, parsed.X0, parsed.Y0, parsed.X1, parsed.Y1,
			parsed.Invert, limits)
		var region *imageinspect.RegionError
		if errors.As(err, &region) {
			return service.refusal(service.mask.Texts.InvalidRegion), nil
		}
	default:
		return service.refusal(service.mask.Texts.InvalidRegion), nil
	}
	if err != nil {
		return service.refusal(strings.NewReplacer(
			"{handle}", picture.Attachment.Handle, "{reason}", err.Error(),
		).Replace(service.mask.Texts.UnreadableTemplate)), nil
	}
	if parsed.Mode == "subject" && parsed.Invert {
		encoded, report, err = imageinspect.InvertMask(encoded, limits)
		if err != nil {
			return service.refusal(strings.NewReplacer(
				"{handle}", picture.Attachment.Handle, "{reason}", err.Error(),
			).Replace(service.mask.Texts.UnreadableTemplate)), nil
		}
	}
	asset, err := service.inputs.UploadAsset(ctx, generation.OwnerID(command.OwnerID), generation.InputAssetUpload{
		Body:             bytes.NewReader(encoded),
		OriginalFilename: "selection-mask.png",
		AssetType:        service.defaultAssetType,
		MediaType:        service.mask.Preview.MediaType,
		MaxBytes:         service.mask.Limits.MaxBytes,
	})
	if err != nil {
		return service.refusal(strings.NewReplacer(
			"{handle}", picture.Attachment.Handle, "{reason}", err.Error(),
		).Replace(service.mask.Texts.UnreadableTemplate)), nil
	}
	maskURL := service.assetURL(string(asset.ID))
	note := strings.NewReplacer(
		"{url}", maskURL,
		"{width}", fmt.Sprint(report.Width),
		"{height}", fmt.Sprint(report.Height),
		"{coverage}", fmt.Sprintf("%.1f%%", report.Coverage*100),
	).Replace(service.mask.Texts.ResultNote)
	content, _ := json.Marshal(map[string]any{
		"success":  true,
		"mask_url": maskURL,
		"width":    report.Width,
		"height":   report.Height,
		"bbox":     report.BBox,
		"coverage": report.Coverage,
		"note":     note,
	})
	return chat.ToolResult{
		// The mask is the reply's picture, so the person sees it and may name it
		// by handle later; the Agent passes its URL as refine_image's mask.
		ImageURLs:    []string{maskURL},
		AgentContent: string(content),
		Summary:      content,
	}, nil
}

func (service *Service) refusal(message string) chat.ToolResult {
	content, _ := json.Marshal(map[string]any{"success": false, "error": message})
	return chat.ToolResult{Error: message, AgentContent: string(content), NoSubmission: true}
}

func (service *Service) read(ctx context.Context, ownerID string, picture chat.ResolvedAttachment) ([]byte, error) {
	owner := generation.OwnerID(ownerID)
	// A generated picture carries both a TaskID and an AssetID, but its asset
	// lives in the Task's outputs, not in the input-asset store: the Task is
	// the source of truth whenever it is present (#244). Trying the input store
	// first made every generated picture unreadable to create_selection_mask.
	if picture.Attachment.TaskID != "" {
		content, err := service.tasks.OpenAsset(ctx, owner, generation.TaskID(picture.Attachment.TaskID),
			generation.AssetID(picture.Attachment.AssetID))
		if err != nil {
			return nil, err
		}
		defer content.Body.Close()
		return io.ReadAll(io.LimitReader(content.Body, service.limit()+1))
	}
	if picture.Attachment.AssetID != "" {
		_, body, err := service.inputs.OpenOwned(ctx, owner, generation.AssetID(picture.Attachment.AssetID))
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return io.ReadAll(io.LimitReader(body, service.limit()+1))
	}
	if strings.HasPrefix(strings.TrimSpace(picture.URL), "data:") {
		comma := strings.IndexByte(picture.URL, ',')
		if comma < 0 {
			return nil, errors.New("invalid data URI")
		}
		decoded, err := base64.StdEncoding.DecodeString(picture.URL[comma+1:])
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return nil, errors.New("picture has no readable asset")
}

func (service *Service) limit() int64 {
	if service.mask.Limits.MaxBytes > 0 {
		return service.mask.Limits.MaxBytes
	}
	return 32 << 20
}

func (service *Service) assetURL(assetID string) string {
	parts := strings.Split(service.assetItemRoute, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[index] = assetID
			break
		}
	}
	return strings.Join(parts, "/")
}

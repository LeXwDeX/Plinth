// Package imagereview implements the read-only inspect_image Agent tool (#245).
// It resolves one picture of the command's session through the same handle
// policy as every other tool, reads its bytes only for the command's owner, and
// reports deterministic facts plus a Schema-declared checklist. The delivery
// checklist also judges the picture against the session's 2D purpose (#282,
// #284, #287). It never creates a Task, spends credits or changes the session.
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
	"github.com/LeXwDeX/Plinth/internal/visionview"
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
	// Usage and Brief locate the session's 2D purpose: Usage declares the
	// purposes (application.chat.image2d_usage) and Brief the production brief
	// field that names one. Without Review.Usage they are unused and the
	// delivery checklist stays the historical one.
	Usage catalog.ChatImage2DUsageSchema
	Brief catalog.ChatProductionBriefSchema
	// Offerings names the models that reach a purpose's minimum short edge in
	// the hint of a failed resolution check. Nil names none.
	Offerings OfferingLookup
}

type Service struct {
	context    catalog.ChatContextSchema
	review     catalog.ChatImageReviewSchema
	inputs     InputAssetReader
	tasks      TaskAssetReader
	usage      catalog.ChatImage2DUsageSchema
	imageField string
	offerings  OfferingLookup
}

func New(config Config) (*Service, error) {
	if config.Inputs == nil && config.Tasks == nil {
		return nil, errors.New("image review: no asset reader")
	}
	return &Service{
		context: config.Context, review: config.Review, inputs: config.Inputs, tasks: config.Tasks,
		usage: config.Usage, imageField: config.Brief.FieldNameOfType("image_profile"), offerings: config.Offerings,
	}, nil
}

// Inspect executes one inspect_image call.
func (service *Service) Inspect(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(command.Call.Arguments, &raw); err != nil {
		return chat.ToolResult{Error: "invalid image", NoSubmission: true}, nil
	}
	var arguments struct {
		Image     string
		Checklist string
	}
	_ = json.Unmarshal(raw["image"], &arguments.Image)
	_ = json.Unmarshal(raw["checklist"], &arguments.Checklist)
	if strings.TrimSpace(arguments.Image) == "" {
		return chat.ToolResult{Error: "invalid image", NoSubmission: true}, nil
	}
	crop := catalog.ChatVisionCropSchema{}
	if vision := service.context.Vision; vision != nil && vision.Crop.Enabled {
		crop = vision.Crop
	}
	var region visionview.Region
	regionRequested := false
	if crop.Enabled && crop.RegionArgument != "" {
		if value, exists := raw[crop.RegionArgument]; exists && !jsonEqualNull(value) {
			if json.Unmarshal(value, &region) != nil {
				return chat.ToolResult{Error: "invalid region", NoSubmission: true}, nil
			}
			regionRequested = true
		}
	}
	showPicture := false
	if crop.Enabled && crop.ShowPictureArgument != "" {
		_ = json.Unmarshal(raw[crop.ShowPictureArgument], &showPicture)
	}
	checklistName := arguments.Checklist
	if checklistName == "" {
		checklistName = "delivery"
	}
	checklist, checklistText, ok := service.checklist(checklistName)
	if !ok {
		return chat.ToolResult{Error: "unknown checklist", NoSubmission: true}, nil
	}
	visualItems := service.visualItems(checklistName)
	verdicts, validVisual := service.visualVerdicts(raw, visualItems)
	if !validVisual {
		return chat.ToolResult{Error: "invalid visual review", NoSubmission: true}, nil
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
	var pictures []chat.ToolPicture
	imageFacts := map[string]any{
		"handle": picture.Attachment.Handle, "task_id": picture.Attachment.TaskID,
		"asset_id": picture.Attachment.AssetID,
	}
	// The whole picture's size, which show_picture describes beside a crop; a
	// region crop's own bytes only exist inside the region branch.
	originalWidth, originalHeight := 0, 0
	if regionRequested {
		decoded, decodeErr := visionview.Decode(data)
		if decodeErr != nil {
			return chat.ToolResult{Error: service.unreadable(picture, decodeErr), NoSubmission: true}, nil
		}
		source := decoded.Bounds()
		originalWidth, originalHeight = source.Dx(), source.Dy()
		cropped, rect, cropErr := visionview.CropRegion(decoded, region,
			crop.MaximumPixels, crop.MinimumSidePixels)
		if cropErr != nil {
			if tooLarge, ok := cropErr.(*visionview.RegionTooLargeError); ok {
				message := strings.NewReplacer(
					"{width}", fmt.Sprint(tooLarge.Width),
					"{height}", fmt.Sprint(tooLarge.Height),
					"{maximum}", fmt.Sprint(tooLarge.Maximum),
				).Replace(crop.TooLargeTemplate)
				return chat.ToolResult{Error: message, NoSubmission: true}, nil
			}
			return chat.ToolResult{Error: "invalid region", NoSubmission: true}, nil
		}
		maxBytes := service.context.VisionInlineMaxBytes
		encoded, encodeErr := visionview.EncodeJPEG(cropped, maxBytes,
			service.context.VisionDerivative.JPEGQualities)
		if encodeErr != nil {
			message := strings.NewReplacer("{handle}", picture.Attachment.Handle, "{reason}", encodeErr.Error()).
				Replace(service.review.UnreadableTemplate)
			return chat.ToolResult{Error: message, NoSubmission: true}, nil
		}
		data = encoded
		imageFacts["source_width"], imageFacts["source_height"] = source.Dx(), source.Dy()
		imageFacts["region"] = rect
		imageFacts["crop_width"], imageFacts["crop_height"] = rect.Width, rect.Height
		if showPicture {
			label := crop.PictureLabelTemplate
			label = strings.NewReplacer(
				"{handle}", picture.Attachment.Handle,
				"{region}", rect.String(),
				"{width}", fmt.Sprint(rect.Width),
				"{height}", fmt.Sprint(rect.Height),
			).Replace(label)
			pictures = append(pictures, chat.ToolPicture{
				Handle: picture.Attachment.Handle, Purpose: chat.ToolPictureCrop,
				Detail: crop.Detail, Label: label,
				URL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(encoded),
			})
		}
	}
	// The purpose checks judge the whole delivered picture. A region crop is a
	// part of it (its short edge, seams and edge are not the picture's), and
	// the 3D input checklist has its own gate, so neither is judged against the
	// purpose.
	var judgement *purposeJudgement
	if checklistName == "delivery" && !regionRequested {
		judgement = service.judgement(command.Options.ProductionBrief)
	}
	var requirements imageinspect.Requirements
	if judgement != nil {
		requirements = judgement.requirements
	}
	report, err := imageinspect.InspectFor(data, imageinspect.Limits{
		MaxBytes:  service.review.Limits.MaxBytes,
		MaxPixels: service.review.Limits.MaxPixels,
	}, requirements)
	if err != nil {
		message := strings.NewReplacer("{handle}", picture.Attachment.Handle, "{reason}", err.Error()).
			Replace(service.review.UnreadableTemplate)
		return chat.ToolResult{Error: message, NoSubmission: true}, nil
	}
	imageFacts["width"], imageFacts["height"] = report.Width, report.Height
	if originalWidth <= 0 || originalHeight <= 0 {
		// No crop asked: report is the original picture's own inspection.
		originalWidth, originalHeight = report.Width, report.Height
	}
	if showPicture {
		// The whole picture first, so the Agent can place the crop in it. It
		// has its own label and its own size: it is not a crop and must not
		// carry the crop region or the crop's dimensions.
		original := chat.ToolPicture{
			Handle: picture.Attachment.Handle, URL: picture.URL,
			Purpose: chat.ToolPictureCrop, Detail: crop.Detail,
			Label: strings.NewReplacer(
				"{handle}", picture.Attachment.Handle,
				"{width}", fmt.Sprint(originalWidth),
				"{height}", fmt.Sprint(originalHeight),
			).Replace(crop.OriginalPictureLabelTemplate),
		}
		pictures = append([]chat.ToolPicture{original}, pictures...)
	}
	checks := report.Review(checklist)
	usageCheck := map[string]any{"checklist": checklistName, "checklist_text": checklistText}
	if judgement != nil {
		purposeChecks := report.ReviewRequirements(judgement.requirements)
		service.addHints(purposeChecks, judgement)
		checks = append(checks, purposeChecks...)
		usageCheck["purpose"] = judgement.profile.ID
		usageCheck["purpose_label"] = judgement.profile.Label
		usageCheck["purpose_source"] = judgement.source
		usageCheck["note"] = service.note(judgement)
	}
	usageCheck["checks"] = checks
	// The Agent's verdicts on the checklist's visual items (#286) join the
	// deterministic checks in the result and in the counts the summary keeps.
	visual := newVisualOutcome(visualItems, verdicts)
	visual.attach(usageCheck)
	result := map[string]any{
		"ok":               true,
		"image":            imageFacts,
		"report":           report,
		"usage_check":      usageCheck,
		"rounds_remaining": max(0, service.review.MaxRounds-used-1),
	}
	content, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "image inspection failed", NoSubmission: true}, nil
	}
	// A check the picture cannot show is counted apart from the passed ones: an
	// unjudged check must not read as a passed one (the ledger's rule for
	// inspect_model).
	passed, failed, unknown := visual.Passed, visual.Failed, 0
	var failedChecks []map[string]string
	for _, check := range checks {
		switch check.Status {
		case imageinspect.StatusPass:
			passed++
		case imageinspect.StatusUnknown:
			unknown++
		default:
			failed++
			failedChecks = append(failedChecks, map[string]string{
				"check": check.Name, "actual": truncate(check.Actual, 120), "fix": check.Fix,
			})
		}
	}
	summaryFacts := map[string]any{
		"tool": "inspect_image", "handle": picture.Attachment.Handle,
		"width": report.Width, "height": report.Height,
		"checks": map[string]int{"passed": passed, "failed": failed, "unknown": unknown},
	}
	if len(visual.Checks) > 0 {
		summaryFacts["visual"] = visual.Checks
	}
	if len(failedChecks) > 0 {
		summaryFacts["failed_checks"] = failedChecks
	}
	summary, _ := json.Marshal(summaryFacts)
	return chat.ToolResult{
		AgentContent: string(content), Summary: summary, Pictures: pictures,
		NoSubmission: true,
	}, nil
}

// OpenPicture reads one already-resolved picture for the reference
// comparison (#246), bounded by the image review byte limit. The owner check
// is the same one Inspect applies to a handle or an asset id.
func (service *Service) OpenPicture(ctx context.Context, owner string, attachment chat.Attachment, url string) ([]byte, error) {
	return service.read(ctx, owner, chat.ResolvedAttachment{Attachment: attachment, URL: url})
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
		MinMainComponentRatio:  schema.MinMainComponentRatio,
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

// unreadable is the Schema refusal naming a picture that cannot be decoded.
func (service *Service) unreadable(picture chat.ResolvedAttachment, err error) string {
	return strings.NewReplacer(
		"{handle}", picture.Attachment.Handle,
		"{reason}", err.Error(),
	).Replace(service.review.UnreadableTemplate)
}

// truncate bounds a measured value kept in the persisted summary.
func truncate(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

func jsonEqualNull(value json.RawMessage) bool {
	return strings.TrimSpace(string(value)) == "null" || len(strings.TrimSpace(string(value))) == 0
}

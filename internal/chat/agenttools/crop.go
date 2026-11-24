package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
)

// Error codes the crop_image_regions result carries. They are the keys of
// application.chat.image_crop.texts, so every refusal has Schema copy.
const (
	cropCodeInvalidArguments = "invalid_arguments"
	cropCodeInvalidRegion    = "invalid_region"
	cropCodeImageTooSmall    = "image_too_small"
	cropCodeImageTooLarge    = "image_too_large"
	cropCodeCropLimitReached = "crop_limit_reached"
	cropCodeImageNotFound    = "image_not_found"
	cropCodeImageUnavailable = "image_unavailable"
	// cropCodeNotPresentable refuses a call before any crop is stored: the
	// session cannot show crops to the person as handled pictures (#552).
	cropCodeNotPresentable = "not_presentable"
	// cropCodeNotPresented replaces a result whose crops did not all become
	// handled pictures of the reply (#552).
	cropCodeNotPresented = "not_presented"

	// cropEntriesField is the result array whose entries describe the shown
	// crops one to one; the orchestrator writes each crop's handle into it.
	cropEntriesField = "crops"
)

// CropRegionsService is the read-only batch crop (#331).
type CropRegionsService interface {
	CropRegions(context.Context, listcrop.Request) ([]listcrop.Result, error)
}

// CropToolConfig declares the read-only crop_image_regions tool. Context
// carries the handle syntax, Schema the refusal copy and Crops the service.
// AssetItemRoute is the Schema's input asset route (runtime.http.routes
// .asset_item): every crop is shown to the person as a picture of the reply
// by that URL (#552).
type CropToolConfig struct {
	Context        catalog.ChatContextSchema
	Schema         catalog.ChatImageCropSchema
	Crops          CropRegionsService
	AssetItemRoute string
	// Localize translates one default-locale Schema text for the turn's
	// locale. Nil returns the text unchanged.
	Localize func(locale, text string) string
}

// CropTool implements crop_image_regions: it resolves one picture of the
// command's owner, cuts the normalized regions through listcrop and returns
// each crop's asset id and geometry. Every crop is also a picture of the reply
// the person sees and the Agent names by handle (#552): a crop the person
// cannot see is never reported as done. It never creates a Task, counts a
// step, writes a self-check or returns picture bytes.
type CropTool struct {
	context        catalog.ChatContextSchema
	schema         catalog.ChatImageCropSchema
	crops          CropRegionsService
	assetItemRoute string
	localize       func(locale, text string) string
}

func NewCropTool(config CropToolConfig) (*CropTool, error) {
	if config.Crops == nil {
		return nil, errors.New("crop tool: missing crop service")
	}
	if strings.TrimSpace(config.Schema.Tool) == "" {
		return nil, errors.New("crop tool: missing image_crop Schema section")
	}
	localize := config.Localize
	if localize == nil {
		localize = func(_, text string) string { return text }
	}
	return &CropTool{
		context:        config.Context,
		schema:         config.Schema,
		crops:          config.Crops,
		assetItemRoute: strings.TrimSpace(config.AssetItemRoute),
		localize:       localize,
	}, nil
}

// cropArguments is the tool's declared object. The owner, session and source
// picture never come from the arguments: the source is resolved against the
// command's own history, selection or input assets.
type cropArguments struct {
	Image   string             `json:"image"`
	Regions []cropArgumentItem `json:"regions"`
}

type cropArgumentItem struct {
	Label  string          `json:"label"`
	Region listcrop.Region `json:"region"`
}

// cropContent is the Agent-facing result. One call cuts out of one source, so
// the crops are one source group and the source size is carried once. Note
// is the Schema's word that the crops are pictures the person sees (#552).
type cropContent struct {
	Success      bool       `json:"success"`
	SourceWidth  int        `json:"source_width"`
	SourceHeight int        `json:"source_height"`
	Crops        []cropItem `json:"crops"`
	Note         string     `json:"note"`
}

// cropItem is one stored crop beside the label its request gave it and its
// pixel size. The orchestrator adds its handle once the reply shows it.
type cropItem struct {
	Label  string `json:"label,omitempty"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	listcrop.Result
}

// Execute runs one crop_image_regions call. Refusals are results, not errors:
// every failure becomes the Schema's copy beside its error code.
func (tool *CropTool) Execute(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	arguments, refused := tool.arguments(command)
	if refused != nil {
		return *refused, nil
	}
	// Nothing is cut when the crops could not reach the person: a stored
	// crop nobody can see is no delivery (#552).
	if detail := tool.unpresentable(); detail != "" {
		return tool.refusal(command, cropCodeNotPresentable, detail, 0), nil
	}
	source, detail, code := tool.source(command, arguments.Image)
	if code != "" {
		return tool.refusal(command, code, detail, 0), nil
	}
	regions := make([]listcrop.Region, len(arguments.Regions))
	for index := range arguments.Regions {
		regions[index] = arguments.Regions[index].Region
	}
	results, err := tool.crops.CropRegions(ctx, listcrop.Request{
		Owner:   generation.OwnerID(command.OwnerID),
		Source:  source,
		Regions: regions,
	})
	if err != nil {
		return tool.failure(command, arguments.Regions, err), nil
	}
	content := cropContent{Success: true, Crops: make([]cropItem, len(results))}
	imageURLs := make([]string, len(results))
	for index, result := range results {
		imageURLs[index] = tool.assetURL(result.InputAssetID)
		content.Crops[index] = cropItem{
			Width: result.PixelRect.Width(), Height: result.PixelRect.Height(), Result: result,
		}
		if index < len(arguments.Regions) {
			content.Crops[index].Label = arguments.Regions[index].Label
		}
		if index == 0 {
			content.SourceWidth, content.SourceHeight = result.SourceWidth, result.SourceHeight
		}
	}
	content.Note = strings.ReplaceAll(tool.localize(command.Options.Locale, tool.schema.Texts.PresentedNote),
		"{count}", strconv.Itoa(len(results)))
	encoded, err := json.Marshal(content)
	if err != nil {
		return chat.ToolResult{Error: "crop result unavailable", NoSubmission: true}, nil
	}
	ids := make([]generation.AssetID, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.InputAssetID)
	}
	summary, _ := json.Marshal(map[string]any{
		"tool":            ToolCropImageRegions,
		"crop_count":      len(results),
		"input_asset_ids": ids,
	})
	unpresented := tool.refusal(command, cropCodeNotPresented,
		"the reply could not show every crop as a picture with its own handle", 0)
	return chat.ToolResult{
		AgentContent:       string(encoded),
		Summary:            summary,
		NoSubmission:       true,
		ImageURLs:          imageURLs,
		PresentedEntries:   cropEntriesField,
		UnpresentedContent: unpresented.AgentContent,
	}, nil
}

// unpresentable says why the crops could not be shown to the person as
// handled pictures of the reply, or is empty when they can: picture handles
// must be on, the result must be able to carry them and the Schema must name
// the route a crop picture is served by.
func (tool *CropTool) unpresentable() string {
	switch {
	case !chat.NewHandlePolicy(tool.context).Enabled():
		return "picture handles are off in this session"
	case strings.TrimSpace(tool.context.ResultHandlesField) == "":
		return "the result cannot carry picture handles"
	case !strings.Contains(tool.assetItemRoute, "{"):
		return "no picture route is configured for the crops"
	}
	return ""
}

// assetURL renders the platform URL of one stored crop from the Schema's
// input asset route, so the reply shows it like any input picture.
func (tool *CropTool) assetURL(id generation.AssetID) string {
	parts := strings.Split(tool.assetItemRoute, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[index] = string(id)
			break
		}
	}
	return strings.Join(parts, "/")
}

// arguments decodes the declared object strictly. Unknown keys are refused
// rather than ignored, like every other Agent tool.
func (tool *CropTool) arguments(command chat.ToolCommand) (cropArguments, *chat.ToolResult) {
	var arguments cropArguments
	decoder := json.NewDecoder(bytes.NewReader(command.Call.Arguments))
	decoder.DisallowUnknownFields()
	if len(command.Call.Arguments) == 0 || decoder.Decode(&arguments) != nil ||
		decoder.Decode(new(any)) != io.EOF {
		refused := tool.refusal(command, cropCodeInvalidArguments,
			"the arguments are not one valid object", 0)
		return arguments, &refused
	}
	arguments.Image = strings.TrimSpace(arguments.Image)
	if arguments.Image == "" {
		refused := tool.refusal(command, cropCodeInvalidArguments, "image is required", 0)
		return arguments, &refused
	}
	if len(arguments.Regions) == 0 {
		refused := tool.refusal(command, cropCodeInvalidArguments,
			"at least one region is required", 0)
		return arguments, &refused
	}
	for index := range arguments.Regions {
		arguments.Regions[index].Label = strings.TrimSpace(arguments.Regions[index].Label)
	}
	return arguments, nil
}

// source resolves the image argument to an owned input asset id. @imgN and
// @selected go through the same handle policy as every tool; an input asset
// id is passed through and owner-checked by the service. @projectN is a
// project token, never a picture of this session, and is refused. detail and
// code are empty on success.
func (tool *CropTool) source(command chat.ToolCommand, image string) (generation.AssetID, string, string) {
	policy := chat.NewHandlePolicy(tool.context)
	if policy.Enabled() {
		_, _, isHandle := policy.Parse(image)
		if isHandle || policy.IsSelectionAlias(image) {
			resolved, err := policy.ResolveHandles(command.ReferenceHistory(),
				command.Options.Selection, []string{image}, chat.AttachmentKindImage)
			if err != nil {
				var unknown *chat.UnknownHandleError
				if errors.As(err, &unknown) {
					reason := "this session has no object with that handle"
					if unknown.Reason == "kind" {
						reason = "the handle does not name a picture"
					}
					return "", fmt.Sprintf("%s: %s", image, reason), cropCodeInvalidArguments
				}
				return "", fmt.Sprintf("%s: %s", image, err.Error()), cropCodeInvalidArguments
			}
			if len(resolved) != 1 {
				return "", fmt.Sprintf("%s names %d pictures; name one picture handle",
					image, len(resolved)), cropCodeInvalidArguments
			}
			assetID := strings.TrimSpace(resolved[0].Attachment.AssetID)
			if assetID == "" {
				return "", fmt.Sprintf("%s has no stored asset id, so it cannot be cropped",
					image), cropCodeInvalidArguments
			}
			return generation.AssetID(assetID), "", ""
		}
	}
	if strings.HasPrefix(image, "@") || strings.HasPrefix(image, "#") {
		return "", fmt.Sprintf("%s: not a picture handle of this session", image),
			cropCodeInvalidArguments
	}
	return generation.AssetID(image), "", ""
}

// failure maps a service refusal to its error code and copy. A refusal tied to
// one region names its position and label; a batch conflict names the region
// it stopped at. {limit} comes from the service's LimitError.
func (tool *CropTool) failure(command chat.ToolCommand, regions []cropArgumentItem, err error) chat.ToolResult {
	code := cropCodeImageUnavailable
	switch {
	case errors.Is(err, listcrop.ErrLimit):
		code = cropCodeCropLimitReached
	case errors.Is(err, listcrop.ErrTooLarge):
		code = cropCodeImageTooLarge
	case errors.Is(err, listcrop.ErrNotFound):
		code = cropCodeImageNotFound
	case errors.Is(err, listcrop.ErrInvalidSource), errors.Is(err, listcrop.ErrUnavailable):
		code = cropCodeImageUnavailable
	case errors.Is(err, listcrop.ErrInvalid):
		code = cropCodeInvalidArguments
	case errors.Is(err, listcrop.ErrInvalidRegion):
		var small *listcrop.TooSmallError
		if errors.As(err, &small) {
			code = cropCodeImageTooSmall
		} else {
			code = cropCodeInvalidRegion
		}
	}
	detail := err.Error()
	if index, found := listcrop.FailureIndex(err); found && index >= 0 && index < len(regions) {
		detail = fmt.Sprintf("regions[%d]", index)
		if label := regions[index].Label; label != "" {
			detail += " (" + label + ")"
		}
		detail += ": " + err.Error()
	}
	var limit int
	var limitError *listcrop.LimitError
	if errors.As(err, &limitError) {
		limit = limitError.Limit
	}
	return tool.refusal(command, code, detail, limit)
}

// refusal renders one Schema text beside its error code. The Schema text is
// localized first, so the bundle key keeps its placeholders.
func (tool *CropTool) refusal(command chat.ToolCommand, code, detail string, limit int) chat.ToolResult {
	text := tool.localize(command.Options.Locale, tool.text(code))
	message := strings.NewReplacer(
		"{detail}", detail,
		"{limit}", strconv.Itoa(limit),
	).Replace(text)
	content, _ := json.Marshal(map[string]any{
		"success": false,
		"code":    code,
		"error":   message,
	})
	return chat.ToolResult{AgentContent: string(content), Error: message, NoSubmission: true}
}

func (tool *CropTool) text(code string) string {
	switch code {
	case cropCodeInvalidArguments:
		return tool.schema.Texts.InvalidArguments
	case cropCodeInvalidRegion:
		return tool.schema.Texts.InvalidRegion
	case cropCodeImageTooSmall:
		return tool.schema.Texts.ImageTooSmall
	case cropCodeImageTooLarge:
		return tool.schema.Texts.ImageTooLarge
	case cropCodeCropLimitReached:
		return tool.schema.Texts.CropLimitReached
	case cropCodeImageNotFound:
		return tool.schema.Texts.ImageNotFound
	case cropCodeNotPresentable:
		return tool.schema.Texts.NotPresentable
	case cropCodeNotPresented:
		return tool.schema.Texts.NotPresented
	default:
		return tool.schema.Texts.ImageUnavailable
	}
}

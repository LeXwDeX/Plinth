package generationtool

// compare_model_reference (#246): render one view of a finished model beside
// the reference picture frozen with an image-to-3D task, and report the
// deterministic subject metrics. The picture is a derived input asset with the
// render role, so the reply attaches it and the Agent can see it.

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// modelReferencePair is one reference picture and the view it is assumed to
// show.
type modelReferencePair struct {
	Image string
	View  string
}

type modelReferenceView struct {
	View    string                         `json:"view"`
	Image   string                         `json:"image"`
	Width   int                            `json:"width"`
	Height  int                            `json:"height"`
	Message string                         `json:"message,omitempty"`
	Metrics imageinspect.SubjectComparison `json:"metrics"`
}

type modelReferenceResult struct {
	OK                   bool                    `json:"ok"`
	Message              string                  `json:"message,omitempty"`
	Model                modelReviewIdentity     `json:"model"`
	Views                []modelReferenceView    `json:"views"`
	FailedViews          []modelReviewFailedView `json:"failed_views"`
	RenderCallsRemaining int                     `json:"render_calls_remaining"`
	Caveats              string                  `json:"caveats,omitempty"`
	Note                 string                  `json:"note,omitempty"`
	SelfCheck            *selfCheckStatusBlock   `json:"self_check,omitempty"`
	ImageURLs            []string                `json:"-"`
	Pictures             []chat.ToolPicture      `json:"-"`
}

// CompareModelReference backs compare_model_reference. It never creates a Task
// or spends credits.
func (executor *Executor) CompareModelReference(ctx context.Context, command chat.ToolCommand) (chat.ToolResult, error) {
	review := executor.chat.ModelReview
	reference := executor.selfCheckReference()
	if executor.modelAssets == nil || executor.inputAssets == nil || executor.pictureReader == nil || reference.Size <= 0 {
		return chat.ToolResult{Error: "reference comparison unavailable", NoSubmission: true}, nil
	}
	arguments, err := modelReviewArguments(command)
	if err != nil {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "the arguments are not a JSON object")), nil
	}
	if refusal := executor.modelReviewBudgetRefusal(command, review); refusal != nil {
		return *refusal, nil
	}
	source, refusal, err := executor.resolveReviewedModel(ctx, command, review,
		rawString(arguments[review.Source.TaskArgument]), rawString(arguments[review.Source.AssetArgument]))
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal != nil {
		return *refusal, nil
	}
	if refusal := executor.selfCheckRenderRefusal(ctx, command, source); refusal != nil {
		return *refusal, nil
	}
	mode := modelinspect.RenderMode(reference.Mode)
	if raw, present := arguments["mode"]; present && rawString(raw) != "" {
		parsed, refusal := executor.modelReviewMode(command, review, rawString(raw))
		if refusal != nil {
			return *refusal, nil
		}
		mode = parsed
	}
	size, ok := modelReviewSize(arguments["size"], reference.Size, review.Render.MaxSize)
	if !ok {
		return executor.modelReviewRefusal(command, executor.fillText(command, review.Texts.InvalidArguments,
			"{detail}", "size must be a positive integer")), nil
	}
	pairs, refusal := executor.modelReferencePairs(ctx, command, review, reference, arguments, source)
	if refusal != nil {
		return *refusal, nil
	}
	result := modelReferenceResult{
		Model:                executor.modelReviewIdentity(ctx, command, source),
		Views:                []modelReferenceView{},
		FailedViews:          []modelReviewFailedView{},
		RenderCallsRemaining: executor.modelReviewCallsRemaining(command, review),
		Caveats:              executor.localized(command, executor.selfChecks.Schema().Texts.Caveats),
	}
	entry, refusal, err := executor.loadReviewedModel(ctx, command, review, source)
	if err != nil {
		return chat.ToolResult{}, err
	}
	if refusal != nil {
		return *refusal, nil
	}
	referenceLimits := imageinspect.Limits{
		MaxBytes:  executor.chat.ImageReview.Limits.MaxBytes,
		MaxPixels: executor.chat.ImageReview.Limits.MaxPixels,
	}
	for _, pair := range pairs {
		data, reason, err := executor.openReferencePicture(ctx, command, pair.Image)
		if err != nil {
			return chat.ToolResult{}, err
		}
		if reason != "" {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{
				View: pair.View, Error: executor.fillText(command, executor.selfChecks.Schema().Texts.ReferenceUnreadable,
					"{handle}", pair.Image, "{reason}", reason),
			})
			continue
		}
		referencePicture, decodeErr := imageinspect.Decode(data, referenceLimits)
		if decodeErr != nil {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{
				View: pair.View, Error: executor.fillText(command, executor.selfChecks.Schema().Texts.ReferenceUnreadable,
					"{handle}", pair.Image, "{reason}", decodeErr.Error()),
			})
			continue
		}
		rendered, renderErr := executor.renderReviewedView(ctx, review, entry, modelinspect.View(pair.View), mode, nil, false, size)
		if renderErr != nil {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{
				View: pair.View, Error: executor.fillText(command, review.Texts.RenderFailed,
					"{view}", pair.View, "{reason}", modelReviewErrorReason(renderErr)),
			})
			continue
		}
		metrics := imageinspect.CompareSubjects(referencePicture, rendered,
			reference.SubjectThreshold, reference.NormalizedSide)
		left := modelReferenceLetterbox(referencePicture, rendered.Bounds().Dx(), modelReviewBackground(review.Render.Background))
		picture := modelReviewSideBySide(left, rendered)
		url, failure, err := executor.uploadReviewedPicture(ctx, command, review, picture,
			string(source.task.ID)+"-reference-"+pair.View)
		if err != nil {
			return chat.ToolResult{}, err
		}
		if failure != "" {
			result.FailedViews = append(result.FailedViews, modelReviewFailedView{View: pair.View, Error: failure})
			continue
		}
		result.Views = append(result.Views, modelReferenceView{
			View: pair.View, Image: pair.Image,
			Width: picture.Bounds().Dx(), Height: picture.Bounds().Dy(),
			Message: executor.fillText(command, executor.selfChecks.Schema().Texts.ReferenceReady,
				"{model}", result.Model.Model, "{image}", pair.Image, "{view}", pair.View,
				"{iou}", strconv.FormatFloat(metrics.SilhouetteIOU, 'f', 4, 64)),
			Metrics: metrics,
		})
		executor.appendReviewedPicture(&result.Pictures, &result.ImageURLs, url)
	}
	result.OK = len(result.Views) > 0
	if result.OK {
		result.Message = executor.fillText(command, review.Texts.CompareReady,
			"{count}", strconv.Itoa(len(result.Views)), "{views}", modelReferenceViewNames(result.Views))
	}
	result.Note = executor.modelReviewNote(command, review, len(result.Views))
	result.SelfCheck = executor.appendSelfCheckCheck(ctx, command, source, chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindReference, Tool: command.Call.Name,
		AssetID: string(source.asset.ID), Views: modelReferenceViewNames2(result.Views), OK: result.OK,
		Metrics: referenceCheckMetrics(result.Views),
	})
	encoded, err := json.Marshal(result)
	if err != nil {
		return chat.ToolResult{Error: "reference comparison not readable", NoSubmission: true}, nil
	}
	return chat.ToolResult{
		AgentContent: string(encoded),
		NoSubmission: true,
		ImageURLs:    result.ImageURLs,
		Pictures:     result.Pictures,
		Summary: modelReviewSummaryJSON(modelReviewMessageSummary{
			Tool: "reference", Model: rawString(arguments[review.Source.TaskArgument]),
			TaskID: string(source.task.ID), Views: modelReferenceViewNames2(result.Views),
			FailedViews:          modelReviewSummaryFailedViews(result.FailedViews),
			RenderCallsRemaining: result.RenderCallsRemaining,
		}),
	}, nil
}

func (executor *Executor) selfCheckReference() catalog.ChatSelfCheckReferenceSchema {
	if executor.selfChecks == nil {
		return catalog.ChatSelfCheckReferenceSchema{}
	}
	return executor.selfChecks.Schema().Reference
}

// modelReferencePairs resolves the explicit image/pairs arguments or, when
// neither is given, the reference pictures frozen with the task's input.
func (executor *Executor) modelReferencePairs(ctx context.Context, command chat.ToolCommand,
	review catalog.ChatModelReviewSchema, reference catalog.ChatSelfCheckReferenceSchema,
	arguments map[string]json.RawMessage, source reviewedModel) ([]modelReferencePair, *chat.ToolResult) {
	limit := reference.MaxPairsPerCall
	if review.Render.MaxViewsPerCall > 0 && (limit <= 0 || limit > review.Render.MaxViewsPerCall) {
		limit = review.Render.MaxViewsPerCall
	}
	if limit < 1 {
		limit = 1
	}
	image := strings.TrimSpace(rawString(arguments["image"]))
	view := strings.TrimSpace(rawString(arguments["view"]))
	if view == "" {
		view = reference.DefaultView
	}
	if !modelReferenceViewDeclared(reference.Views, view) {
		return nil, executor.referenceViewRefusal(command, view)
	}
	var pairs []modelReferencePair
	if raw, present := arguments["pairs"]; present {
		if len(raw) > 0 && string(raw) != "null" {
			var decoded []struct {
				Image string `json:"image"`
				View  string `json:"view"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				return nil, executor.modelReferenceArgumentsRefusal(command, "pairs must be a list of {image, view} objects")
			}
			for _, pair := range decoded {
				imageToken := strings.TrimSpace(pair.Image)
				viewToken := strings.TrimSpace(pair.View)
				if imageToken == "" || viewToken == "" {
					return nil, executor.modelReferenceArgumentsRefusal(command, "every pair needs image and view")
				}
				if !modelReferenceViewDeclared(reference.Views, viewToken) {
					return nil, executor.referenceViewRefusal(command, viewToken)
				}
				pairs = append(pairs, modelReferencePair{Image: imageToken, View: viewToken})
			}
			if image != "" {
				return nil, executor.modelReferenceArgumentsRefusal(command, "name image or pairs, not both")
			}
		}
	}
	if len(pairs) == 0 && image != "" {
		pairs = append(pairs, modelReferencePair{Image: image, View: view})
	}
	if len(pairs) == 0 {
		frozen, reason := frozenReferencePictures(source.task)
		if len(frozen) == 0 {
			text := executor.localized(command, executor.selfChecks.Schema().Texts.ReferenceNoReference)
			if reason != "" {
				text += " (" + reason + ")"
			}
			return nil, executor.modelReviewRefusalResult(command, text)
		}
		views, _ := ReferenceViews(source.task.Offering, source.task.Input)
		for index, token := range frozen {
			pairView := view
			if index < len(views) && modelReferenceViewDeclared(reference.Views, views[index]) {
				pairView = views[index]
			}
			pairs = append(pairs, modelReferencePair{Image: token, View: pairView})
		}
	}
	if len(pairs) > limit {
		return nil, executor.modelReferenceArgumentsRefusal(command,
			"at most "+strconv.Itoa(limit)+" reference pairs can be compared per call")
	}
	return pairs, nil
}

func (executor *Executor) modelReferenceArgumentsRefusal(command chat.ToolCommand, detail string) *chat.ToolResult {
	return executor.modelReviewRefusalResult(command, executor.fillText(command,
		executor.selfChecks.Schema().Texts.RecordInvalidArguments, "{detail}", detail))
}

func (executor *Executor) referenceViewRefusal(command chat.ToolCommand, view string) *chat.ToolResult {
	return executor.modelReviewRefusalResult(command, executor.fillText(command,
		executor.chat.ModelReview.Texts.UnknownView, "{view}", view,
		"{views}", strings.Join(executor.selfCheckReference().Views, ", ")))
}

// modelReviewRefusalResult is the same refusal shape as modelReviewRefusal but
// returns a pointer for the pair resolver.
func (executor *Executor) modelReviewRefusalResult(command chat.ToolCommand, text string) *chat.ToolResult {
	refusal := executor.modelReviewRefusal(command, text)
	return &refusal
}

// openReferencePicture reads an explicit handle or asset id, or a frozen input
// asset named by its id. reason is the Schema-facing failure; err is a
// service failure that must abort the call.
func (executor *Executor) openReferencePicture(ctx context.Context, command chat.ToolCommand,
	token string) (data []byte, reason string, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, "empty reference", nil
	}
	policy := executor.handlePolicy()
	if _, _, isHandle := policy.Parse(token); isHandle || policy.IsSelectionAlias(token) {
		resolved, resolveErr := policy.ResolveHandles(command.ReferenceHistory(), command.Options.Selection,
			[]string{token}, chat.AttachmentKindImage)
		if resolveErr != nil || len(resolved) == 0 {
			return nil, "the handle names no picture of this session", nil
		}
		data, err := executor.pictureReader.OpenPicture(ctx, command.OwnerID, resolved[0].Attachment, resolved[0].URL)
		if err != nil {
			return nil, err.Error(), nil
		}
		return data, "", nil
	}
	attachment := chat.Attachment{Handle: token, Kind: chat.AttachmentKindImage, AssetID: token}
	data, openErr := executor.pictureReader.OpenPicture(ctx, command.OwnerID, attachment, "")
	if openErr != nil {
		// An explicit unknown token is reported by the caller as unreadable,
		// not as an internal failure.
		return nil, openErr.Error(), nil
	}
	return data, "", nil
}

// frozenReferencePictures reads the reference_assets of a task's frozen input,
// including the single reference of a text-to-3D Offering that declares one.
func frozenReferencePictures(task generation.Task) ([]string, string) {
	var values map[string]json.RawMessage
	if json.Unmarshal(task.Input, &values) != nil {
		return nil, "the task input is not readable"
	}
	var pictures []string
	for _, parameter := range task.Offering.Parameters {
		if parameter.Semantic != generation.SemanticReferenceImage {
			continue
		}
		raw, present := values[parameter.Name]
		if !present {
			continue
		}
		if json.Unmarshal(raw, &pictures) == nil && len(pictures) > 0 {
			return pictures, ""
		}
		var single string
		if json.Unmarshal(raw, &single) == nil && single != "" {
			return []string{single}, ""
		}
	}
	return nil, ""
}

func modelReferenceViewDeclared(views []string, view string) bool {
	for _, declared := range views {
		if declared == view {
			return true
		}
	}
	return false
}

func modelReferenceViewNames(views []modelReferenceView) string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.View)
	}
	return strings.Join(names, ", ")
}

func modelReferenceViewNames2(views []modelReferenceView) []string {
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.View)
	}
	return names
}

func referenceCheckMetrics(views []modelReferenceView) *chat.SelfCheckMetrics {
	if len(views) == 0 {
		return nil
	}
	first := views[0].Metrics
	return &chat.SelfCheckMetrics{
		SilhouetteIOU:         first.SilhouetteIOU,
		BBoxAspectDelta:       first.BBoxAspectDelta,
		DominantColorDistance: first.DominantColorDistance,
		DominantReference:     first.DominantReference,
		DominantRender:        first.DominantRender,
		ReferenceSubjectRatio: first.ReferenceSubjectRatio,
		RenderSubjectRatio:    first.RenderSubjectRatio,
		Method:                first.Method,
	}
}

// modelReferenceLetterbox scales the reference into a square canvas while
// preserving its aspect, so the comparison never stretches the reference.
func modelReferenceLetterbox(source *image.NRGBA, side int, background color.NRGBA) *image.NRGBA {
	if source == nil || side <= 0 {
		return source
	}
	output := image.NewNRGBA(image.Rect(0, 0, side, side))
	draw.Draw(output, output.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return output
	}
	scale := float64(side) / float64(width)
	if vertical := float64(side) / float64(height); vertical < scale {
		scale = vertical
	}
	targetWidth := max(1, int(float64(width)*scale))
	targetHeight := max(1, int(float64(height)*scale))
	offsetX, offsetY := (side-targetWidth)/2, (side-targetHeight)/2
	for y := 0; y < targetHeight; y++ {
		sourceY := bounds.Min.Y + y*height/targetHeight
		for x := 0; x < targetWidth; x++ {
			sourceX := bounds.Min.X + x*width/targetWidth
			offset := source.PixOffset(sourceX, sourceY)
			destination := output.PixOffset(offsetX+x, offsetY+y)
			copy(output.Pix[destination:destination+4], source.Pix[offset:offset+4])
		}
	}
	return output
}

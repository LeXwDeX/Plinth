package generationtool

import (
	"context"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// ModelInspectRequest names one saved model for InspectReviewedModel.
type ModelInspectRequest struct {
	Owner  generation.OwnerID
	TaskID generation.TaskID
	// AssetID is the saved model file to read; empty selects the Task's main
	// model file by SourceVariantPreference (generation.PrimaryModelAsset).
	AssetID                 generation.AssetID
	SourceVariantPreference []string
	Brief                   chat.ProductionBrief
	Locale                  string
}

// ModelInspectResult is the read-only inspection of one saved model.
type ModelInspectResult struct {
	TaskID         generation.TaskID
	AssetID        generation.AssetID
	Report         modelinspect.Report
	UsageCheck     *ModelReviewUsageCheck
	PartsTotal     int
	PartsTruncated bool
}

// ModelInspectRefusal is an inspection refused before or while reading the
// file. Kind is one of the ModelInspect* constants; Message is the localized
// text a tool result would show.
type ModelInspectRefusal struct {
	Kind    string
	Message string
}

func (refusal *ModelInspectRefusal) Error() string { return refusal.Message }

// The refusal kinds of InspectReviewedModel: the source is not the owner's
// (not_found), has not succeeded or has no saved model file (not_ready), is not
// binary glTF (not_glb), is over the Schema's byte limit (too_large), cannot be
// opened or parsed (unreadable), or the review state is not wired
// (unavailable).
const (
	ModelInspectNotFound    = "not_found"
	ModelInspectNotReady    = "not_ready"
	ModelInspectNotGLB      = "not_glb"
	ModelInspectTooLarge    = "too_large"
	ModelInspectUnreadable  = "unreadable"
	ModelInspectUnavailable = "unavailable"
)

// InspectReviewedModel is the read-only core of inspect_model behind the
// outward MCP server (#332): it loads the owner's Task, parses the saved file
// through the review cache and concurrency slots, truncates the listed parts
// and judges the report against the purpose like the platform tool. It never
// reads the session, writes self-check evidence or creates a Task.
func (executor *Executor) InspectReviewedModel(
	ctx context.Context,
	request ModelInspectRequest,
) (ModelInspectResult, error) {
	review := executor.chat.ModelReview
	if executor.modelAssets == nil || executor.modelReview == nil {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: ModelInspectUnavailable, Message: "model review is unavailable",
		}
	}
	command := chat.ToolCommand{OwnerID: string(request.Owner), Options: chat.TurnOptions{Locale: request.Locale}}
	task, err := executor.gateway.Get(ctx, request.Owner, request.TaskID)
	if err != nil {
		if generation.IsCode(err, generation.ErrNotFound) {
			return ModelInspectResult{}, &ModelInspectRefusal{
				Kind: ModelInspectNotFound, Message: executor.localized(command, review.Source.SourceNotFound),
			}
		}
		return ModelInspectResult{}, err
	}
	command.SessionID = task.SessionID
	if task.State != generation.TaskSucceeded {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: ModelInspectNotReady, Message: executor.localized(command, review.Source.SourceNotReady),
		}
	}
	asset, refusal := modelInspectAsset(task, request, command, review, executor)
	if refusal != nil {
		return ModelInspectResult{}, refusal
	}
	media := strings.ToLower(strings.TrimSpace(asset.MediaType))
	if media == "" {
		media = modelReviewMediaType
	}
	if media != modelReviewMediaType {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: ModelInspectNotGLB,
			Message: executor.fillText(command, review.Source.SourceNotGLB,
				"{asset}", string(asset.ID), "{media}", media),
		}
	}
	limits := modelReviewLimits(review)
	if asset.Size > limits.MaxBytes {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: ModelInspectTooLarge,
			Message: executor.fillText(command, review.Texts.TooLarge,
				"{bytes}", strconv.FormatInt(asset.Size, 10),
				"{limit}", strconv.FormatInt(limits.MaxBytes, 10)),
		}
	}
	source := reviewedModel{task: task, asset: asset}
	entry, loadRefusal, err := executor.loadReviewedModel(ctx, command, review, source)
	if err != nil {
		return ModelInspectResult{}, err
	}
	if loadRefusal != nil {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: ModelInspectUnreadable, Message: rejectionMessage(loadRefusal),
		}
	}
	if entry == nil {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: ModelInspectUnreadable, Message: "saved model cannot be reviewed",
		}
	}
	options := modelReviewInspectOptions(review)
	report, err := entry.inspect(ctx, executor.modelReview, options)
	if err != nil {
		return ModelInspectResult{}, err
	}
	total := len(report.Parts)
	truncated := false
	if review.Inspect.MaxListedParts > 0 && total > review.Inspect.MaxListedParts {
		report.Parts = append([]modelinspect.PartReport(nil), report.Parts[:review.Inspect.MaxListedParts]...)
		if len(report.UV.Primitives) > review.Inspect.MaxListedParts {
			report.UV.Primitives = append([]modelinspect.PrimitiveUVReport(nil), report.UV.Primitives[:review.Inspect.MaxListedParts]...)
		}
		truncated = true
	}
	// A processing result's exports the purpose sized are what its texture
	// budget judges; the inspected file is reported beside them (#292). A file
	// the purpose sized itself is judged as it is, and the upstream's own file
	// kept beside it is measured so the result can name it (#305).
	sized := executor.sizedDeliverable(task, asset)
	if sized != nil && sized.Own {
		sized.Original = executor.siblingTextureEdge(ctx, command, review, options, source)
	}
	usage := newModelReviewUsageCheckFor(
		report,
		sized,
		executor.chat.Model3DUsage,
		review.Inspect.Production,
		request.Brief,
		func(template string, replacements ...string) string {
			return executor.fillText(command, template, replacements...)
		},
		executor.modelReviewFixAvailability(),
	)
	return ModelInspectResult{
		TaskID:         task.ID,
		AssetID:        asset.ID,
		Report:         report,
		UsageCheck:     usage,
		PartsTotal:     total,
		PartsTruncated: truncated,
	}, nil
}

// modelInspectReadRefusal reports whether the refusal came from opening or
// parsing the saved file, the failures inspect_model records as failed
// self-check evidence. A source that is not found or not ready was rejected
// before the file was read and leaves no evidence, as before.
func modelInspectReadRefusal(kind string) bool {
	switch kind {
	case ModelInspectNotGLB, ModelInspectTooLarge, ModelInspectUnreadable:
		return true
	}
	return false
}

// modelInspectAsset resolves the saved model file the request names, or the
// Task's main model file when it names none.
func modelInspectAsset(
	task generation.Task,
	request ModelInspectRequest,
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	executor *Executor,
) (generation.Asset, *ModelInspectRefusal) {
	if request.AssetID != "" {
		asset, found := taskAsset(task, request.AssetID)
		if !found {
			return generation.Asset{}, &ModelInspectRefusal{
				Kind: ModelInspectNotFound, Message: executor.localized(command, review.Source.SourceNotFound),
			}
		}
		if asset.StorageKey == "" {
			return generation.Asset{}, &ModelInspectRefusal{
				Kind: ModelInspectNotReady, Message: executor.localized(command, review.Source.SourceNotReady),
			}
		}
		return asset, nil
	}
	asset, found := generation.PrimaryModelAsset(task, request.SourceVariantPreference)
	if !found {
		return generation.Asset{}, &ModelInspectRefusal{
			Kind: ModelInspectNotReady, Message: executor.localized(command, review.Source.SourceNotReady),
		}
	}
	return asset, nil
}

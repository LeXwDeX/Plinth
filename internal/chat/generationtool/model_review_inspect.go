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
	// Purpose names one chat.model3d_usage profile to judge this inspection
	// only (#332 7c-1). Empty judges against the Brief like the platform tool.
	// The caller refuses an ID the Schema does not declare.
	Purpose string
	// AllowedFixes bounds the fix hints to capabilities the caller can execute
	// itself; nil keeps the platform's runtime availability check. The outward
	// MCP path passes the declared process_model capabilities.
	AllowedFixes []generation.Capability
	Locale       string
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
// opened or parsed (unreadable), names a purpose the Schema does not declare
// (unknown_purpose), or the review state is not wired (unavailable).
const (
	ModelInspectNotFound       = "not_found"
	ModelInspectNotReady       = "not_ready"
	ModelInspectNotGLB         = "not_glb"
	ModelInspectTooLarge       = "too_large"
	ModelInspectUnreadable     = "unreadable"
	ModelInspectUnknownPurpose = "unknown_purpose"
	ModelInspectUnavailable    = "unavailable"
)

// InspectReviewedModel is the read-only core of inspect_model behind the
// outward MCP server (#332): it loads the owner's Task, parses the saved file
// through the review cache and concurrency slots, truncates the listed parts
// and judges the report against the purpose like the platform tool. An explicit
// Purpose judges this call only, and AllowedFixes keeps fix hints to
// capabilities the caller can execute. It never reads the session, writes
// self-check evidence or creates a Task.
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
	// An argument purpose the Schema does not declare is refused here too, not
	// silently replaced by the brief's purpose: the check below would otherwise
	// judge something the caller did not ask for (#332 7c-1).
	if request.Purpose != "" && !modelReviewUsageProfileDeclared(executor.chat.Model3DUsage, request.Purpose) {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: ModelInspectUnknownPurpose, Message: "unknown purpose",
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
	if !modelReviewMediaTypeLeavesFormatOpen(media) {
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind:    ModelInspectNotGLB,
			Message: executor.modelReviewNotGLBRefusal(command, review, reviewedModel{task: task, asset: asset}).Error,
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
	entry, loadRefusal, kind, err := executor.loadReviewedModelKind(ctx, command, review, source)
	if err != nil {
		return ModelInspectResult{}, err
	}
	if loadRefusal != nil {
		if kind == "" {
			kind = ModelInspectUnreadable
		}
		return ModelInspectResult{}, &ModelInspectRefusal{
			Kind: kind, Message: rejectionMessage(loadRefusal),
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
		request.Purpose,
		func(template string, replacements ...string) string {
			return executor.fillText(command, template, replacements...)
		},
		executor.modelReviewFixAvailability(),
		request.AllowedFixes,
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

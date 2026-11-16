package generationtool

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modelcheck"
	"github.com/LeXwDeX/Plinth/internal/modelinspect"
)

// The deliverables a purpose sized (#292). A processing Offering declares, on
// the outputs whose textures one parameter sets (the exports a session purpose
// bounds), texture_size_parameter: the select parameter of the texture-size
// semantic whose frozen value is their texture edge. The purpose's texture
// budget judges those deliverables, because the purpose chose their size; the
// inspected file, usually the native result the upstream wrote at its own
// resolution, is reported beside them and is neither a pass nor a failure of the
// budget. A Task whose Offering declares none keeps the file's own verdict.
//
// The edge comes from the Task's frozen input, not from the exported file:
// inspect_model reads binary glTF only, so the export's textures are not
// measured, and the result says so. The exception is a GLB the purpose sized
// (the model slot of a rig or a decimation, whose GLB is a conversion of the
// upstream's, #305): inspect_model can read that file, so it is judged by its own
// bytes, which show whether the conversion really wrote the size. The upstream's
// own GLB is kept beside it as the original file, which is no model source for the
// tools; so the converted model's item names the original's measured edge, and the
// delivery is not taken for a smaller one than it is.

// sizedDeliverable is the deliverables of one Task that the frozen input sized.
type sizedDeliverable struct {
	// Edge is their texture edge in pixels.
	Edge int
	// Variants are the delivered outputs they are, in the Offering's order.
	Variants []string
	// Own marks an inspected file that is itself one of them (#305): the budget
	// judges the file's own bytes, not Edge, and Edge only names what the step was
	// asked for.
	Own bool
	// Original is the largest texture edge of the files the Task kept that the
	// purpose did not size, read beside an Own file (the upstream's own GLB, #305);
	// 0 when there is none or none could be measured.
	Original int
}

// judged is the edge the budget takes from the frozen input in place of the
// inspected file's own: Edge for a sized export the inspector cannot read, 0 when
// the inspected file is itself sized and so measured as it is.
func (sized *sizedDeliverable) judged() int {
	if sized == nil || sized.Own {
		return 0
	}
	return sized.Edge
}

// formats names the sized deliverables the way a person reads them ("FBX/OBJ").
func (sized sizedDeliverable) formats() string {
	names := make([]string, 0, len(sized.Variants))
	for _, variant := range sized.Variants {
		names = append(names, strings.ToUpper(variant))
	}
	return strings.Join(names, "/")
}

// sizedDeliverable finds, among the outputs a Task delivered, the ones its
// Offering declares as sized by a parameter, and the edge the frozen input gave
// them. When the inspected asset is one of them it is marked Own and judged by
// its own bytes, so the other sized outputs of its Task do not stand in for it. It
// returns nil when the Task has none, the Schema declares no texture-size budget,
// or a declaration cannot be read, which leaves the inspected file to decide as
// before.
func (executor *Executor) sizedDeliverable(task generation.Task, inspected generation.Asset) *sizedDeliverable {
	limit, bounded := executor.chat.Model3DUsage.BudgetLimit(generation.SemanticTextureSize)
	if !bounded || limit.Bound != catalog.BudgetBoundMaxTextureSize || len(task.Input) == 0 {
		return nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(task.Input, &values) != nil {
		return nil
	}
	var sized sizedDeliverable
	for _, output := range task.Offering.Outputs {
		if output.TextureSizeParameter == "" || !taskDelivered(task, output) {
			continue
		}
		sized.Own = sized.Own || output.Role == inspected.Role && output.Variant == inspected.Variant
		var parameter generation.Parameter
		found := false
		for _, candidate := range task.Offering.Parameters {
			if candidate.Name == output.TextureSizeParameter {
				parameter, found = candidate, true
			}
		}
		if !found {
			return nil
		}
		value, present := values[parameter.Name]
		if !present {
			value = parameter.Default
		}
		pixels, measured := limit.Measure(task.Offering, parameter, value, values)
		if !measured || pixels <= 0 {
			return nil
		}
		sized.Edge = max(sized.Edge, int(pixels))
		sized.Variants = append(sized.Variants, output.Variant)
	}
	if sized.Edge == 0 {
		return nil
	}
	return &sized
}

// unsizedSiblings are the binary glTF files the Task saved that no parameter
// sized, other than the inspected one: for a rig or a decimation the upstream's own
// GLB, kept beside the converted model as the original file (#305). The budget does
// not judge them, so they are named rather than left out of the result.
func unsizedSiblings(task generation.Task, inspected generation.Asset) []generation.Asset {
	var siblings []generation.Asset
	for _, output := range task.Offering.Outputs {
		if output.TextureSizeParameter != "" || len(output.MediaTypes) != 1 || output.MediaTypes[0] != modelReviewMediaType {
			continue
		}
		for _, asset := range task.Assets {
			if asset.Role == output.Role && asset.Variant == output.Variant && asset.StorageKey != "" && asset.ID != inspected.ID {
				siblings = append(siblings, asset)
			}
		}
	}
	return siblings
}

// siblingTextureEdge is the largest texture edge among the unsizedSiblings of the
// inspected file, so the result can say what the budget does not judge and a
// converted model does not stand for the whole delivery. It is stated only when it
// was measured: a file that cannot be loaded within the Schema's limits is left out,
// and 0 means there is none to name.
func (executor *Executor) siblingTextureEdge(
	ctx context.Context,
	command chat.ToolCommand,
	review catalog.ChatModelReviewSchema,
	options modelinspect.InspectOptions,
	source reviewedModel,
) int {
	edge := 0
	for _, sibling := range unsizedSiblings(source.task, source.asset) {
		entry, refusal, err := executor.loadReviewedModel(ctx, command, review, reviewedModel{task: source.task, asset: sibling})
		if err != nil || refusal != nil || entry == nil {
			continue
		}
		report, err := entry.inspect(ctx, executor.modelReview, options)
		if err != nil {
			continue
		}
		measured, _, _ := modelcheck.TextureEdge(report, 0)
		edge = max(edge, measured)
	}
	return edge
}

// taskDelivered reports whether the Task saved a file for the declared output.
func taskDelivered(task generation.Task, output generation.AssetRequirement) bool {
	for _, asset := range task.Assets {
		if asset.Role == output.Role && asset.Variant == output.Variant && asset.StorageKey != "" {
			return true
		}
	}
	return false
}

// SizedTextureEdge is the texture edge, in pixels, of the deliverables of task
// that the session purpose sized, or 0 when it has none. The processing plan's
// checkpoints judge texture_size with it, so a checkpoint reaches the verdict
// inspect_model reaches about the same Task (#292). inspected is the file the
// checkpoint reads.
func (executor *Executor) SizedTextureEdge(task generation.Task, inspected generation.AssetID) int {
	asset, _ := taskAsset(task, inspected)
	if sized := executor.sizedDeliverable(task, asset); sized != nil {
		return sized.judged()
	}
	return 0
}

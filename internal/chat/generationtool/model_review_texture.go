package generationtool

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
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
// measured, and the result says so.

// sizedDeliverable is the deliverables of one Task that the frozen input sized.
type sizedDeliverable struct {
	// Edge is their texture edge in pixels.
	Edge int
	// Variants are the delivered outputs they are, in the Offering's order.
	Variants []string
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
// them. The inspected asset is never one of them: a file that is itself sized is
// judged by its own bytes. It returns nil when the Task has none, the Schema
// declares no texture-size budget, or a declaration cannot be read, which leaves
// the inspected file to decide as before.
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
		if output.TextureSizeParameter == "" || (output.Role == inspected.Role && output.Variant == inspected.Variant) ||
			!taskDelivered(task, output) {
			continue
		}
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
		return sized.Edge
	}
	return 0
}

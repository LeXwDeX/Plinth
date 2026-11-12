package catalog

import (
	"encoding/json"
	"slices"
	"strings"
)

// Platform variants of a purpose (#275). The brief's target platform picks one
// of a purpose's Platforms; the variant's bounds replace the purpose's and its
// parameter overrides are merged over the purpose's. The choosing lives here
// once so that every reader of the purpose (the model choice, the budget, the
// inspect_model check, the plan checkpoints and the brief text) resolves the
// same budget and the same overrides.

// ChatModel3DUsagePlatformChoice names the platform variant that shaped a
// resolved purpose: the brief option id and the option's label.
type ChatModel3DUsagePlatformChoice struct {
	ID    string
	Label string
}

// PlatformOption returns the declared option of the platform brief field with
// that id. A Catalog without a platform field, or a value that is not one of
// the field's options (a free-text value written before the field became a
// select), has none.
func (usage ChatModel3DUsageSchema) PlatformOption(id string) (ChatProductionBriefOption, bool) {
	if usage.PlatformField == "" || strings.TrimSpace(id) == "" {
		return ChatProductionBriefOption{}, false
	}
	for _, field := range usage.Brief.Fields {
		if field.Name == usage.PlatformField && field.Type == "select" {
			return field.OptionByID(id)
		}
	}
	return ChatProductionBriefOption{}, false
}

// ForPlatform returns the purpose as it applies to the target platform with
// that option id: the variant's budget patched over the purpose's and its
// parameter overrides merged over the purpose's. The returned choice says which
// platform shaped the result. An empty platform, one the field does not
// declare or one no variant of the purpose covers returns the purpose unchanged
// and a nil choice, so a session without a platform keeps the purpose's single
// budget. The Schema value is never modified.
func (usage ChatModel3DUsageSchema) ForPlatform(
	profile ChatModel3DUsageProfile,
	platformID string,
) (ChatModel3DUsageProfile, *ChatModel3DUsagePlatformChoice) {
	platformID = strings.TrimSpace(platformID)
	option, declared := usage.PlatformOption(platformID)
	if !declared {
		return profile, nil
	}
	for _, variant := range profile.Platforms {
		if !slices.Contains(variant.IDs, platformID) {
			continue
		}
		resolved := profile
		resolved.Budget = profile.Budget.WithPlatform(variant.Budget)
		resolved.ParameterOverrides = mergeParameterOverrides(profile.ParameterOverrides, variant.ParameterOverrides)
		resolved.Platforms = nil
		return resolved, &ChatModel3DUsagePlatformChoice{ID: option.ID, Label: option.Label}
	}
	return profile, nil
}

// WithPlatform patches a platform variant's bounds over the budget: a bound the
// variant declares replaces the purpose's, every other field stays.
func (budget ChatModel3DUsageBudget) WithPlatform(platform ChatModel3DUsagePlatformBudget) ChatModel3DUsageBudget {
	if platform.MinTriangles != nil {
		budget.MinTriangles = platform.MinTriangles
	}
	if platform.MaxTriangles != nil {
		budget.MaxTriangles = *platform.MaxTriangles
	}
	if platform.MaxTextureSize != nil {
		budget.MaxTextureSize = platform.MaxTextureSize
	}
	return budget
}

// mergeParameterOverrides returns the purpose's overrides with the variant's
// values written over them, per Offering and per parameter. The inputs are
// Schema values shared by every session, so the result is built from copies.
func mergeParameterOverrides(base, patch map[string]map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	if len(patch) == 0 {
		return base
	}
	merged := make(map[string]map[string]json.RawMessage, len(base)+len(patch))
	for offering, values := range base {
		copied := make(map[string]json.RawMessage, len(values))
		for name, value := range values {
			copied[name] = value
		}
		merged[offering] = copied
	}
	for offering, values := range patch {
		target := merged[offering]
		if target == nil {
			target = make(map[string]json.RawMessage, len(values))
			merged[offering] = target
		}
		for name, value := range values {
			target[name] = value
		}
	}
	return merged
}

package catalog

import (
	"strings"
	"testing"
)

// #363: a bundle field path may not translate a machine identity or route.
// These leaves are compared by code, so a translation breaks behavior.
func TestLocalizationRejectsMachineFieldLeaves(t *testing.T) {
	for _, path := range []string{
		"task_states[internal_state=QUEUED].internal_state",
		"content.navigation[key=workspace].href",
		"capabilities[id=text-to-3d].id",
		"chat.tools[name=process_model].name",
	} {
		t.Run(path, func(t *testing.T) {
			application := cloneLocalization(t, defaultApplicationForLocalization(t))
			application.Localization.Bundles["en"].Fields[path] = "translated"
			err := ValidateApplicationSchema(application)
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("machine field accepted or not named: %v", err)
			}
		})
	}
}

// Labels and descriptions next to machine fields stay translatable, as do
// user-facing fields whose name is also a machine field elsewhere.
func TestLocalizationAcceptsUserFacingFields(t *testing.T) {
	for _, path := range []string{
		"task_states[internal_state=QUEUED].label",
		"content.navigation[key=workspace].label",
		"capabilities[id=text-to-3d].label",
		"chat.tools[name=process_model].description",
		"task_history.asset_recovery.action",
		"provider_discovery.legacy_models[name=静态 GLB → glTF].name",
		"agent_configuration.mcp_presets[id=workspace-read].name",
	} {
		t.Run(path, func(t *testing.T) {
			application := cloneLocalization(t, defaultApplicationForLocalization(t))
			application.Localization.Bundles["en"].Fields[path] = "translated"
			if err := ValidateApplicationSchema(application); err != nil {
				t.Fatalf("user-facing field rejected: %v", err)
			}
		})
	}
}

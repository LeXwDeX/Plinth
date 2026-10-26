package mock

import (
	"encoding/json"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// multiviewViews are the four saved views a Tripo Edit Multiview source must have.
var multiviewViews = map[string]bool{"front": true, "left": true, "back": true, "right": true}

// ResolveDependencyBinding gives Tripo Edit Multiview's provider-bound source
// (Schema task_source_binding "provider") on the local mock stack the same
// bounded, read-only evidence the Tripo Adapter projects: the exact source
// Task, Attempt and remote job of a complete four-view image-to-multiview
// result. Every other provider-bound source is refused, because its real
// Adapter checks evidence (rig spec, result identity) the mock does not have.
// Generation has already checked owner, session, state, Offering allowlist
// and saved bytes.
func (a *Adapter) ResolveDependencyBinding(request generation.DependencyBindingRequest) (json.RawMessage, error) {
	incomplete := errors.New("mock dependency binding: not a complete four-view source")
	if request.Offering.Capability != schema.ImageToMultiview || request.TaskID == "" || request.AttemptID == "" ||
		request.Job.RemoteID == "" || request.Job.ProviderID != request.Offering.ProviderID {
		return nil, incomplete
	}
	seen := map[string]bool{}
	for _, asset := range request.Assets {
		if asset.Role == "image" && multiviewViews[asset.Variant] && asset.Required && asset.Size > 0 {
			seen[asset.Variant] = true
		}
	}
	if len(seen) != len(multiviewViews) {
		return nil, incomplete
	}
	return json.Marshal(map[string]any{"task_id": request.TaskID, "attempt_id": request.AttemptID, "remote_id": request.Job.RemoteID})
}

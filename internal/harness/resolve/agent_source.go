package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var ErrSourceClarification = errors.New("harness: clarify model source")

const agentSourceMaxCandidates = 128

// AgentSource is an explicit permission to choose a scoped Task source. An
// absent source or historical fixed source never acquires this permission.
func AgentSource(s harness.Selection) bool {
	return len(s.Sources) == 1 && s.Sources[0].Kind == harness.SourceTaskAsset && s.Sources[0].SelectionMode == "agent"
}

// AgentSourceCandidate is private frozen evidence. Model/public projections
// must explicitly select the source identity and display metadata only.
type AgentSourceCandidate struct {
	Source      harness.Source    `json:"source"`
	Fingerprint SourceFingerprint `json:"fingerprint"`
	Value       string            `json:"value"`
	Model       string            `json:"model"`
	CreatedAt   time.Time         `json:"created_at"`
	Preferred   bool              `json:"preferred,omitempty"`
}

func (s Snapshot) BoundSources() []harness.Source {
	if AgentSource(s.Selection) {
		return s.ResolvedSources
	}
	return s.Selection.Sources
}

type preferredSourcesKey struct{}

// WithPreferredSources accepts server-verified current objects only. They are
// hints for this new snapshot, never substitutes for scope/contract checks.
func WithPreferredSources(ctx context.Context, sources []harness.Source) context.Context {
	return context.WithValue(ctx, preferredSourcesKey{}, append([]harness.Source(nil), sources...))
}

type sourceTaskLister interface {
	List(context.Context, generation.TaskListScope, generation.ListQuery) (generation.TaskPage, error)
}

func agentSourceParameter(selected harness.Selection, o generation.Offering) (generation.Parameter, error) {
	if !AgentSource(selected) {
		return generation.Parameter{}, harness.ErrInvalid
	}
	for _, p := range o.Parameters {
		role, ok := schema.SourceParameterRole(p)
		if (p.Name == selected.Sources[0].Parameter || selected.Sources[0].Parameter == "*") && ok && (role == schema.SemanticSourceAsset || role == schema.SemanticSourceTask) {
			return p, nil
		}
	}
	return generation.Parameter{}, invalid("Agent source route is not declared")
}

func (s Service) resolveAgentSources(ctx context.Context, snapshot *Snapshot, o generation.Offering, tool *catalog.ChatToolSchema) (bool, error) {
	if _, err := agentSourceParameter(snapshot.Selection, o); err != nil {
		return false, err
	}
	lister, ok := s.Gateway.(sourceTaskLister)
	if !ok {
		return false, ErrUnavailable
	}
	preferred, _ := ctx.Value(preferredSourcesKey{}).([]harness.Source)
	parameter := snapshot.Selection.Sources[0].Parameter
	route := o
	if parameter != "*" {
		route = sourceOffering(o, parameter)
	}
	seen := map[string]bool{}
	addTask := func(task generation.Task, wanted string) {
		if task.ID == "" || task.OwnerID != generation.OwnerID(snapshot.Selection.OwnerID) || task.SessionID != snapshot.Selection.SessionID || task.State != generation.TaskSucceeded {
			return
		}
		assets := append([]generation.Asset(nil), task.Assets...)
		// The main model first, as the panel and the tools bind it (#326).
		slices.SortStableFunc(assets, generation.CompareModelAssets(sourcePreference(tool)))
		for _, asset := range assets {
			if wanted != "" && string(asset.ID) != wanted {
				continue
			}
			if asset.ID == "" || asset.Role != "model" || asset.StorageKey == "" || asset.SHA256 == "" || asset.Size <= 0 || (asset.TaskID != "" && asset.TaskID != task.ID) {
				continue
			}
			binding, err := generation.BindModelSource(route, task, asset.ID, sourcePreference(tool))
			if err != nil || (parameter != "*" && binding.Parameter != parameter) {
				continue
			}
			exact, bindErr := generation.BindModelSource(sourceOffering(o, binding.Parameter), task, asset.ID, sourcePreference(tool))
			if bindErr != nil || exact != binding {
				continue
			}
			// Task-backed wire inputs may have several downloadable formats but one
			// input value. Freeze the preferred real file instead of duplicating it.
			key := string(task.ID) + "\x00" + binding.Parameter + "\x00" + binding.Value
			if seen[key] {
				continue
			}
			seen[key] = true
			source := harness.Source{Kind: harness.SourceTaskAsset, Parameter: binding.Parameter, TaskID: string(task.ID), AssetID: string(asset.ID)}
			choice := AgentSourceCandidate{Source: source, Value: binding.Value, Model: task.Offering.ModelName, CreatedAt: task.CreatedAt, Fingerprint: SourceFingerprint{Kind: harness.SourceTaskAsset, Parameter: binding.Parameter, TaskID: task.ID, AssetID: asset.ID, SHA256: asset.SHA256, Size: asset.Size, Variant: asset.Variant}}
			for _, p := range preferred {
				if p.TaskID == source.TaskID && (p.AssetID == "" || p.AssetID == source.AssetID) {
					choice.Preferred = true
				}
			}
			if len(snapshot.SourceCandidates) >= agentSourceMaxCandidates {
				snapshot.SourceScanTruncated = true
				return
			}
			snapshot.SourceCandidates = append(snapshot.SourceCandidates, choice)
		}
	}
	if len(preferred) > agentSourceMaxCandidates {
		return false, invalid("too many preferred model sources")
	}
	for _, p := range preferred {
		task, err := s.Gateway.Get(ctx, generation.OwnerID(snapshot.Selection.OwnerID), generation.TaskID(p.TaskID))
		if err != nil {
			if generation.IsCode(err, generation.ErrNotFound) || errors.Is(err, generation.RepositoryNotFound) {
				continue
			}
			return false, err
		}
		if task.ID == generation.TaskID(p.TaskID) {
			addTask(task, p.AssetID)
		}
	}
	cursor := ""
	// Match the existing bounded session-model reader: at most 200 Tasks. A
	// partial scan permits explicit frozen choices, never automatic uniqueness.
	for page := 0; page < 4; page++ {
		result, err := lister.List(ctx, generation.OwnTasks(generation.OwnerID(snapshot.Selection.OwnerID)), generation.ListQuery{SessionID: snapshot.Selection.SessionID, States: []generation.TaskState{generation.TaskSucceeded}, Limit: 50, Cursor: cursor})
		if err != nil {
			return false, err
		}
		if len(result.Tasks) > 50 {
			return false, invalid("candidate page exceeds requested bound")
		}
		for _, task := range result.Tasks {
			addTask(task, "")
		}
		if result.NextCursor == "" {
			break
		}
		if result.NextCursor == cursor || page == 3 {
			snapshot.SourceScanTruncated = true
			break
		}
		cursor = result.NextCursor
	}
	preferredCount := 0
	for _, c := range snapshot.SourceCandidates {
		if c.Preferred {
			v := c.Source
			snapshot.DefaultSource = &v
			preferredCount++
		}
	}
	if preferredCount != 1 || len(preferred) != 1 {
		snapshot.DefaultSource = nil
	}
	if len(preferred) == 0 && len(snapshot.SourceCandidates) == 1 && !snapshot.SourceScanTruncated {
		v := snapshot.SourceCandidates[0].Source
		snapshot.DefaultSource = &v
	}
	if len(snapshot.SourceCandidates) > 0 {
		var contract map[string]any
		if json.Unmarshal(snapshot.ToolParameters, &contract) != nil {
			return false, harness.ErrInvalid
		}
		choices := make([]any, 0, len(snapshot.SourceCandidates))
		for _, c := range snapshot.SourceCandidates {
			choices = append(choices, map[string]any{"type": "object", "properties": map[string]any{"task_id": map[string]any{"type": "string", "const": c.Source.TaskID}, "asset_id": map[string]any{"type": "string", "const": c.Source.AssetID}}, "required": []string{"task_id", "asset_id"}, "additionalProperties": false})
		}
		contract["properties"].(map[string]any)["source"] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id":  map[string]any{"type": "string"},
				"asset_id": map[string]any{"type": "string"},
			},
			"required":             []string{"task_id", "asset_id"},
			"additionalProperties": false,
			"oneOf":                choices,
		}
		if snapshot.DefaultSource == nil {
			contract["required"] = []string{"source"}
		}
		snapshot.ToolParameters, _ = json.Marshal(contract)
	}
	return len(snapshot.SourceCandidates) > 0, nil
}

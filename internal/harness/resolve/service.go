// Package resolve validates Harness choices and projects their current readiness.
// Its Gateway exposes reads only; execution remains in Generation and Processing.
package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

const (
	StatusReady       = "ready"
	StatusNeedsInput  = "needs_input"
	StatusUnavailable = "unavailable"
	StatusFree        = "free"
)

var ErrUnavailable = errors.New("harness resolver: dependencies unavailable")

type Gateway interface {
	OfferingAvailable(generation.OfferingID) bool
	Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error)
}

type UploadedModels interface {
	Inspect(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, modelinput.Profile, error)
}

// ParameterUploadedModels is optional for historical inspector ports. Frozen
// policy declarations require this capability and never use the legacy path.
type ParameterUploadedModels interface {
	InspectForParameter(context.Context, generation.OwnerID, generation.AssetID, generation.Parameter) (generation.InputAsset, modelinput.Profile, error)
}

type Service struct {
	UploadedModels UploadedModels
	Store          harness.Store
	Schema         catalog.ChatHarnessSchema
	Tools          []catalog.ChatToolSchema
	Catalog        *catalog.Catalog
	Gateway        Gateway
}

// Snapshot is safe to persist in a private checkpoint. Public/model consumers
// must select an explicit projection; ToolArguments are frozen server metadata.
// Restoring a checkpoint must preserve its original input, arguments and source
// fingerprints, rather than replacing them with a freshly resolved Snapshot.
type Snapshot struct {
	Selection         harness.Selection          `json:"selection"`
	OperationID       string                     `json:"operation_id,omitempty"`
	Label             string                     `json:"label,omitempty"`
	ToolName          string                     `json:"tool_name,omitempty"`
	ToolArguments     map[string]json.RawMessage `json:"tool_arguments,omitempty"`
	ToolParameters    json.RawMessage            `json:"tool_parameters,omitempty"`
	PrerequisitePaths [][]string                 `json:"prerequisite_paths,omitempty"`
	Capability        generation.Capability      `json:"capability,omitempty"`
	OfferingRevision  string                     `json:"offering_revision,omitempty"`
	Input             map[string]json.RawMessage `json:"input"`
	Status            string                     `json:"status"`
	Reason            string                     `json:"reason,omitempty"`
	MissingParameters []string                   `json:"missing_parameters"`
	// ConversationParameters are the reference-image inputs the Agent takes
	// from the conversation (see schema.ConversationReferenceImage); they never block
	// readiness and are not listed as missing.
	ConversationParameters []string `json:"conversation_parameters,omitempty"`
	// CallableWithMissing marks a needs_input turn whose only gap is a
	// parameter the person must still set: the target tool stays callable and
	// answers which input is missing, so the Agent can ask for it (#187 D). A
	// missing or unusable source still hides the tool.
	CallableWithMissing bool                            `json:"callable_with_missing,omitempty"`
	CanPropose          bool                            `json:"can_propose"`
	SourceCandidates    []AgentSourceCandidate          `json:"source_candidates,omitempty"`
	DefaultSource       *harness.Source                 `json:"default_source,omitempty"`
	SourceScanTruncated bool                            `json:"source_scan_truncated,omitempty"`
	ResolvedSources     []harness.Source                `json:"resolved_sources,omitempty"`
	SourceViews         []SourceView                    `json:"source_views,omitempty"`
	SourceFingerprints  []SourceFingerprint             `json:"source_fingerprints"`
	Operation           *catalog.HarnessOperationSchema `json:"-"`
	Tool                *catalog.ChatToolSchema         `json:"-"`
	Offering            *generation.Offering            `json:"-"`
}

type SourceFingerprint struct {
	Kind            harness.SourceKind `json:"kind,omitempty"`
	ProfileID       string             `json:"profile_id,omitempty"`
	ProfileRevision string             `json:"profile_revision,omitempty"`
	Parameter       string             `json:"parameter"`
	TaskID          generation.TaskID  `json:"task_id"`
	AssetID         generation.AssetID `json:"asset_id"`
	SHA256          string             `json:"sha256,omitempty"`
	Size            int64              `json:"size"`
	Variant         string             `json:"variant,omitempty"`
}

func (s Service) Selection(ctx context.Context, owner, session string) (harness.Selection, error) {
	if s.Store == nil {
		return harness.Selection{}, ErrUnavailable
	}
	return s.Store.GetHarnessSelection(ctx, owner, session)
}

func (s Service) Update(ctx context.Context, owner, session string, expectedRevision int64, candidate harness.Selection) (harness.Selection, error) {
	// Establish authenticated scope before accepting any candidate metadata.
	if _, err := s.Selection(ctx, owner, session); err != nil {
		return harness.Selection{}, err
	}
	candidate.OwnerID = owner
	candidate.SessionID = session
	if candidate.OperationID == "" {
		candidate.OfferingID = ""
		candidate.OfferingRevision = ""
		candidate.Sources = []harness.Source{}
		candidate.FixedParameters = map[string]json.RawMessage{}
		candidate.FillableParameters = []string{}
		candidate.AllowPrerequisites = false
	}
	candidate, err := harness.NormalizeSelection(candidate)
	if err != nil {
		return harness.Selection{}, err
	}
	if err = s.validateEnabled(candidate); err != nil {
		return harness.Selection{}, err
	}
	if candidate.OperationID != "" {
		_, _, offering, err := s.declarations(candidate)
		if err != nil {
			return harness.Selection{}, err
		}
		if err = s.validateParameters(candidate, offering); err != nil {
			return harness.Selection{}, err
		}
		if AgentSource(candidate) {
			if _, err := agentSourceParameter(candidate, offering); err != nil {
				return harness.Selection{}, err
			}
		} else if len(candidate.Sources) == 1 && candidate.Sources[0].Kind == harness.SourceInputAsset {
			if _, _, err = s.uploadedSource(ctx, candidate, offering); err != nil {
				return harness.Selection{}, err
			}
		} else if _, _, err = s.source(ctx, candidate, offering, true); err != nil {
			return harness.Selection{}, err
		}
	}
	candidate, err = harness.NormalizeSelection(candidate)
	if err != nil {
		return harness.Selection{}, err
	}
	return s.Store.CompareAndSwapHarnessSelection(ctx, candidate, expectedRevision)
}

func (s Service) Snapshot(ctx context.Context, owner, session string) (Snapshot, error) {
	selected, err := s.Selection(ctx, owner, session)
	if err != nil {
		return Snapshot{}, err
	}
	return s.ResolveSelection(ctx, selected)
}

// ResolveSelection explicitly re-evaluates a supplied selection. It never reads
// the current selection or rewrites an existing checkpoint. Admission separately
// compares the immutable execution binding with the latest stored selection.
func (s Service) ResolveSelection(ctx context.Context, selected harness.Selection) (Snapshot, error) {
	normalized, err := harness.NormalizeSelection(selected)
	if err != nil {
		return Snapshot{}, err
	}
	if !selected.Binding.Valid() || selected.Digest != normalized.Digest {
		return Snapshot{}, harness.ErrInvalid
	}
	snapshot := Snapshot{Selection: normalized, OperationID: normalized.OperationID, OfferingRevision: normalized.OfferingRevision,
		Input: cloneValues(normalized.FixedParameters), MissingParameters: []string{}, SourceFingerprints: []SourceFingerprint{}}
	if normalized.OperationID == "" {
		snapshot.Status = StatusFree
		return snapshot, nil
	}
	operation, tool, offering, err := s.declarations(normalized)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return Snapshot{}, err
		}
		snapshot.Status = StatusUnavailable
		snapshot.Reason = s.Schema.UnavailableMessage
		return snapshot, nil
	}
	snapshot.Operation = &operation
	snapshot.Tool = tool
	snapshot.Offering = &offering
	snapshot.Label = operation.Label
	snapshot.ToolName = operation.ToolName
	snapshot.ToolArguments = cloneValues(operation.ToolArguments)
	snapshot.Capability = operation.Capability
	snapshot.ToolParameters, err = toolParameters(normalized, offering, s.Schema.ConversationInputDescription)
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.validateEnabled(normalized); err != nil {
		snapshot.Status = StatusUnavailable
		snapshot.Reason = s.Schema.UnavailableMessage
		return snapshot, nil
	}
	if err = s.validateParameters(normalized, offering); err != nil {
		snapshot.Status = StatusUnavailable
		snapshot.Reason = s.Schema.UnavailableMessage
		return snapshot, nil
	}
	var task *generation.Task
	var sourceUsable bool
	if AgentSource(normalized) {
		sourceUsable, err = s.resolveAgentSources(ctx, &snapshot, offering, tool)
	} else if len(normalized.Sources) == 1 && normalized.Sources[0].Kind == harness.SourceInputAsset {
		sourceUsable, err = s.resolveUploadedSnapshot(ctx, &snapshot, offering)
	} else {
		task, sourceUsable, err = s.source(ctx, normalized, offering, false)
	}
	if err != nil {
		if errors.Is(err, harness.ErrInvalid) {
			snapshot.Status = StatusNeedsInput
			snapshot.Reason = s.Schema.MissingInputMessage
			return snapshot, nil
		}
		return Snapshot{}, err
	}
	if task != nil {
		chosen := normalized.Sources[0]
		prefer := sourcePreference(tool)
		var binding generation.ModelSourceBinding
		if sourceUsable {
			var bindErr error
			binding, bindErr = generation.BindModelSource(sourceOffering(offering, chosen.Parameter), *task, generation.AssetID(chosen.AssetID), prefer)
			if bindErr == nil && binding.Parameter == chosen.Parameter {
				snapshot.Input[binding.Parameter], err = json.Marshal(binding.Value)
				if err != nil {
					return Snapshot{}, err
				}
			} else {
				sourceUsable = false
			}
		}
		// The real source remains frozen even if it cannot feed the goal yet.
		// A legal prerequisite proposal needs this original identity as well.
		if asset, ok := fingerprintAsset(*task, chosen, binding, prefer); ok {
			snapshot.SourceFingerprints = append(snapshot.SourceFingerprints, SourceFingerprint{
				Parameter: chosen.Parameter, TaskID: task.ID, AssetID: asset.ID,
				SHA256: asset.SHA256, Size: asset.Size, Variant: asset.Variant})
		}
	}
	// Source failures are read even when runtime wiring is unavailable: storage
	// faults must remain errors, never an innocuous product-unavailable result.
	if s.Gateway == nil {
		return Snapshot{}, ErrUnavailable
	}
	if !offering.Enabled || !s.Gateway.OfferingAvailable(offering.ID) || tool == nil || operation.ToolName == "" {
		snapshot.Status = StatusUnavailable
		snapshot.Reason = operation.UnavailableReason
		if snapshot.Reason == "" {
			snapshot.Reason = s.Schema.UnavailableMessage
		}
		return snapshot, nil
	}
	if normalized.AllowPrerequisites && task != nil && len(snapshot.SourceFingerprints) == 1 {
		snapshot.PrerequisitePaths = s.availablePaths(operation, normalized, *task, snapshot.SourceFingerprints[0].AssetID, sourcePreference(tool))
	}
	snapshot.CanPropose = len(snapshot.PrerequisitePaths) > 0

	needsSource := hasModelSource(offering)
	sourceBlocked := needsSource && !sourceUsable
	if sourceBlocked {
		snapshot.MissingParameters = append(snapshot.MissingParameters, sourceParameter(offering, normalized))
	}
	snapshot.ConversationParameters = conversationParameters(offering)
	parameterBlocked := false
	values := conditionValues(offering, snapshot.Input)
	for _, parameter := range offering.Parameters {
		if !parameter.Required || generation.ParameterOmitted(parameter, values) {
			continue
		}
		if _, source := generationSourceRole(parameter); source {
			continue
		}
		// A reference image comes with the request in the conversation; the
		// call is refused with the missing input named when it does not.
		if schema.ConversationReferenceImage(parameter) {
			continue
		}
		if value, exists := values[parameter.Name]; exists && value != nil && value != "" {
			continue
		}
		snapshot.MissingParameters = append(snapshot.MissingParameters, parameter.Name)
		if !slices.Contains(normalized.FillableParameters, parameter.Name) {
			parameterBlocked = true
		}
	}
	slices.Sort(snapshot.MissingParameters)
	snapshot.MissingParameters = slices.Compact(snapshot.MissingParameters)
	snapshot.Status = StatusReady
	if sourceBlocked || parameterBlocked {
		snapshot.Status = StatusNeedsInput
		snapshot.Reason = s.Schema.MissingInputMessage
		snapshot.CallableWithMissing = !sourceBlocked && len(snapshot.ToolParameters) > 0
	}
	return snapshot, nil
}

func (s Service) validateEnabled(selected harness.Selection) error {
	known := map[string]bool{}
	for _, operation := range s.Schema.Operations {
		known[operation.ID] = true
	}
	for _, id := range selected.EnabledOperations {
		if !known[id] {
			return invalid("unknown enabled operation %q", id)
		}
	}
	if selected.OperationID != "" && !slices.Contains(selected.EnabledOperations, selected.OperationID) {
		return invalid("active operation must be enabled")
	}
	return nil
}

func (s Service) declarations(selected harness.Selection) (catalog.HarnessOperationSchema, *catalog.ChatToolSchema, generation.Offering, error) {
	if s.Catalog == nil {
		return catalog.HarnessOperationSchema{}, nil, generation.Offering{}, ErrUnavailable
	}
	operation, ok := s.operation(selected.OperationID)
	if !ok {
		return operation, nil, generation.Offering{}, invalid("unknown operation")
	}
	offering, ok := s.Catalog.Get(generation.OfferingID(selected.OfferingID))
	if !ok || selected.OfferingRevision == "" || offering.Revision != selected.OfferingRevision || offering.Capability != operation.Capability {
		return operation, nil, generation.Offering{}, invalid("offering identity or capability mismatch")
	}
	if len(operation.ModelIDs) > 0 && !slices.Contains(operation.ModelIDs, offering.ModelID) {
		return operation, nil, offering, invalid("offering model is not registered for operation")
	}
	var tool *catalog.ChatToolSchema
	for _, candidate := range s.Tools {
		if candidate.Name == operation.ToolName && candidate.Processing != nil && candidate.Processing.Plan == nil && slices.Contains(candidate.Execution.Capabilities, operation.Capability) {
			copy := candidate
			tool = &copy
			break
		}
	}
	return operation, tool, offering, nil
}

func (s Service) operation(id string) (catalog.HarnessOperationSchema, bool) {
	for _, operation := range s.Schema.Operations {
		if operation.ID == id {
			return operation, true
		}
	}
	return catalog.HarnessOperationSchema{}, false
}

func invalid(message string, args ...any) error {
	return fmt.Errorf("%w: %s", harness.ErrInvalid, fmt.Sprintf(message, args...))
}

func cloneValues(values map[string]json.RawMessage) map[string]json.RawMessage {
	clone := make(map[string]json.RawMessage, len(values))
	for name, value := range values {
		clone[name] = append(json.RawMessage(nil), value...)
	}
	return clone
}

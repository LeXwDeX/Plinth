package generationtool

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

// Bind only against this turn's immutable candidates. New work and recovery
// parse the same raw call and never re-list a session or recompute its default.
// The Selection and its execution binding remain unchanged.
func bindHarnessSource(command chat.ToolCommand) (chat.ToolCommand, error) {
	snapshot := command.Options.Harness
	if snapshot == nil || snapshot.Selection.OperationID == "" {
		return command, nil
	}
	args, err := decodeHarnessObject(command.Call.Arguments, command.OwnerID, command.SessionID)
	if err != nil {
		return command, ErrOperationConflict
	}
	raw, explicit := args["source"]
	if !resolve.AgentSource(snapshot.Selection) {
		if explicit {
			return command, ErrOperationConflict
		}
		return command, nil
	}
	source := snapshot.DefaultSource
	if explicit {
		fields, err := decodeHarnessObject(raw, command.OwnerID, command.SessionID)
		if err != nil || len(fields) != 2 {
			return command, ErrOperationConflict
		}
		var task, asset string
		if json.Unmarshal(fields["task_id"], &task) != nil || json.Unmarshal(fields["asset_id"], &asset) != nil || task == "" || asset == "" {
			return command, ErrOperationConflict
		}
		source = &harness.Source{TaskID: task, AssetID: asset}
	}
	if source == nil {
		return command, resolve.ErrSourceClarification
	}
	var chosen *resolve.AgentSourceCandidate
	for i := range snapshot.SourceCandidates {
		c := &snapshot.SourceCandidates[i]
		if c.Source.TaskID == source.TaskID && c.Source.AssetID == source.AssetID {
			if chosen != nil {
				return command, ErrOperationConflict
			}
			chosen = c
		}
	}
	if chosen == nil || chosen.Source.Kind != harness.SourceTaskAsset || (snapshot.Selection.Sources[0].Parameter != "*" && chosen.Source.Parameter != snapshot.Selection.Sources[0].Parameter) || chosen.Fingerprint.TaskID != generation.TaskID(chosen.Source.TaskID) || chosen.Fingerprint.AssetID != generation.AssetID(chosen.Source.AssetID) || chosen.Value == "" {
		return command, ErrOperationConflict
	}
	copy := *snapshot
	copy.ResolvedSources = []harness.Source{chosen.Source}
	copy.SourceFingerprints = []resolve.SourceFingerprint{chosen.Fingerprint}
	copy.Input = map[string]json.RawMessage{}
	for k, v := range snapshot.Input {
		copy.Input[k] = append(json.RawMessage(nil), v...)
	}
	copy.Input[chosen.Source.Parameter], _ = json.Marshal(chosen.Value)
	command.Options.Harness = &copy
	return command, nil
}

// Recheck a new Agent-bound source before authorization/submission, including
// recovered operations. A bound grant is observed on its original Task earlier
// in the existing recovery path and never reaches this method.
func (e *Executor) verifyAgentHarnessPreparedSource(ctx context.Context, command chat.ToolCommand, prepared generation.PreparedInput) (chat.ToolCommand, error) {
	if !activeHarness(command) || !resolve.AgentSource(command.Options.Harness.Selection) {
		return command, nil
	}
	bound, err := bindHarnessSource(command)
	if err != nil {
		return command, err
	}
	var offering generation.Offering
	for _, o := range e.gateway.Offerings(prepared.Capability) {
		if o.ID == prepared.OfferingID && o.Revision == prepared.OfferingRevision {
			offering = o
			break
		}
	}
	if offering.ID == "" {
		return command, ErrOperationConflict
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(prepared.NormalizedInput, &input) != nil || !fixedHarnessInputMatches(bound, prepared.NormalizedInput) {
		return command, ErrOperationConflict
	}
	if err = e.verifyHarnessSource(ctx, bound, offering, input); err != nil {
		return command, err
	}
	return bound, nil
}

// VerifyDetachedHarnessSource is the read-only Harness gate of the existing
// pending-approval submitter. It uses the original turn scope and frozen
// PreparedInput, not a fresh candidate search or current workspace selection.
func (e *Executor) VerifyDetachedHarnessSource(ctx context.Context, turn chat.Turn, op Operation, prepared generation.PreparedInput) error {
	if turn.OwnerID != op.OwnerID || turn.SessionID != op.SessionID || turn.AssistantMessageID != op.AssistantMessageID {
		return ErrOperationConflict
	}
	if prepared.ID != op.PreparedInputID || string(prepared.OwnerID) != op.OwnerID || prepared.SessionID != op.SessionID || prepared.InputHash != op.PreparedInputHash || prepared.OfferingID != op.OfferingID || prepared.OfferingRevision != op.OfferingRevision {
		return ErrOperationConflict
	}
	snapshot := turn.Options.Harness
	if op.Harness.IsZero() {
		// A persisted Harness snapshot always has a real selection digest. Only
		// the explicit historical no-snapshot contract may use a zero binding.
		if snapshot != nil {
			return ErrOperationConflict
		}
		return nil
	}
	if snapshot == nil || snapshot.Selection.Binding != op.Harness {
		return ErrOperationConflict
	}
	selected := snapshot.Selection
	selected.OwnerID = op.OwnerID
	normalized, normErr := harness.NormalizeSelection(selected)
	if normErr != nil || normalized.Digest != selected.Digest || selected.SessionID != op.SessionID {
		return ErrOperationConflict
	}
	if !resolve.AgentSource(snapshot.Selection) {
		if len(snapshot.SourceCandidates) > 0 || len(snapshot.ResolvedSources) > 0 || snapshot.DefaultSource != nil || snapshot.SourceScanTruncated {
			return ErrOperationConflict
		}
		return nil // Explicit fixed/historical contract; ordinary admission still runs.
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(prepared.NormalizedInput, &values) != nil {
		return ErrOperationConflict
	}
	var chosen *resolve.AgentSourceCandidate
	for i := range snapshot.SourceCandidates {
		c := &snapshot.SourceCandidates[i]
		var value string
		if json.Unmarshal(values[c.Source.Parameter], &value) != nil {
			continue
		}
		if c.Value == value {
			if chosen != nil {
				return ErrOperationConflict
			}
			chosen = c
		}
	}
	if chosen == nil {
		return ErrOperationConflict
	}
	args, _ := json.Marshal(map[string]any{"source": map[string]string{"task_id": chosen.Source.TaskID, "asset_id": chosen.Source.AssetID}})
	command := chat.ToolCommand{OwnerID: op.OwnerID, SessionID: op.SessionID, AssistantMessageID: op.AssistantMessageID, Options: turn.Options}
	command.Call.Arguments = args
	for _, tool := range e.chat.Tools {
		if tool.Name == snapshot.ToolName {
			command.Tool = tool
			break
		}
	}
	if err := e.checkCurrentHarness(ctx, command); err != nil {
		if errors.Is(err, harness.ErrNotFound) || errors.Is(err, harness.ErrInvalid) {
			return ErrOperationConflict
		}
		return err
	}
	_, err := e.verifyAgentHarnessPreparedSource(ctx, command, prepared)
	if errors.Is(err, generation.RepositoryNotFound) || generation.IsCode(err, generation.ErrNotFound) || errors.Is(err, harness.ErrInvalid) {
		return ErrOperationConflict
	}
	return err
}

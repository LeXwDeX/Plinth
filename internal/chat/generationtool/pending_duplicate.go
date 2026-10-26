package generationtool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"sort"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ApprovalRequestKey is the explicit identity of "the same thing" that an
// approval asks for. Two approvals of one session with the same key ask for the
// same paid step, so while the earlier one is still pending a repeat of the
// call must not create a second card.
//
// The key covers, and only covers:
//   - the tool that was called (owner and session scope the lookup itself);
//   - the frozen capability, Offering ID and Offering revision;
//   - the frozen normalized input, where every asset ID is replaced by the
//     SHA-256 of the asset's bytes (a picture uploaded twice is one picture);
//   - the extra credits the input incurs, so a card that also asks to spend
//     extra credits (extra export formats) and one that does not are two cards;
//   - the input assets by parameter, position, role, media type, size and SHA-256
//     (never by asset ID);
//   - the earlier tasks by parameter/task ID, plus opt-in private source identity digest;
//   - the scene, manifest and source-object context the input was frozen under;
//   - any explained adjustments to recommended values.
//
// It deliberately leaves out the prepared-input ID, input hash and creation
// time (they differ on every prepare), the call ID, the turn and the idempotency
// key. It does not look at prompt wording beyond the normalized input: a
// reworded prompt is a different request.
func ApprovalRequestKey(tool string, prepared generation.PreparedInput) string {
	type asset struct {
		Parameter string `json:"parameter"`
		Index     int    `json:"index"`
		Role      string `json:"role"`
		MediaType string `json:"media_type"`
		Size      int64  `json:"size"`
		SHA256    string `json:"sha256"`
	}
	type dependency struct {
		Parameter      string `json:"parameter"`
		TaskID         string `json:"task_id"`
		IdentityDigest string `json:"identity_digest,omitempty"`
	}
	bySHA := make(map[string]string, len(prepared.Assets))
	assets := make([]asset, 0, len(prepared.Assets))
	for _, input := range prepared.Assets {
		bySHA[string(input.ID)] = "sha256:" + input.SHA256
		assets = append(assets, asset{input.Parameter, input.Index, input.Role, input.MediaType, input.Size, input.SHA256})
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].Parameter != assets[j].Parameter {
			return assets[i].Parameter < assets[j].Parameter
		}
		return assets[i].Index < assets[j].Index
	})
	dependencies := make([]dependency, 0, len(prepared.Dependencies))
	for _, item := range prepared.Dependencies {
		identityDigest := ""
		if item.SourceSessionID != "" || len(item.ProviderBinding) > 0 {
			if len(item.SourceSessionID) > 200 {
				return ""
			}
			var canonical json.RawMessage
			if len(item.ProviderBinding) > 0 {
				var err error
				canonical, err = generation.CanonicalDependencyBinding(item.ProviderBinding)
				if err != nil {
					return ""
				}
			}
			identity, err := json.Marshal(struct {
				Session string          `json:"session,omitempty"`
				Binding json.RawMessage `json:"binding,omitempty"`
			}{item.SourceSessionID, canonical})
			if err != nil {
				return ""
			}
			sum := sha256.Sum256(identity)
			identityDigest = hex.EncodeToString(sum[:])
		}
		dependencies = append(dependencies, dependency{Parameter: item.Parameter, TaskID: string(item.TaskID), IdentityDigest: identityDigest})
	}
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].Parameter != dependencies[j].Parameter {
			return dependencies[i].Parameter < dependencies[j].Parameter
		}
		return dependencies[i].TaskID < dependencies[j].TaskID
	})
	encoded, err := json.Marshal(struct {
		Tool                 string                           `json:"tool"`
		Capability           string                           `json:"capability"`
		OfferingID           string                           `json:"offering_id"`
		OfferingRevision     string                           `json:"offering_revision"`
		Input                any                              `json:"input"`
		ExtraCredits         int                              `json:"extra_credits"`
		Assets               []asset                          `json:"assets"`
		Dependencies         []dependency                     `json:"dependencies"`
		SceneID              string                           `json:"scene_id"`
		ManifestHash         string                           `json:"manifest_hash"`
		SourceObject         string                           `json:"source_object"`
		SourceRevision       int64                            `json:"source_revision"`
		ParameterAdjustments []generation.ParameterAdjustment `json:"parameter_adjustments,omitempty"`
		NativeQuote          *generation.NativeQuote          `json:"native_quote,omitempty"`
	}{tool, string(prepared.Capability), string(prepared.OfferingID), prepared.OfferingRevision,
		canonicalInput(prepared.NormalizedInput, bySHA), prepared.ExtraCredits, assets, dependencies, prepared.SceneID,
		prepared.ManifestHash, prepared.SourceSceneObjectID, prepared.SourceSceneRevision, prepared.ParameterAdjustments, prepared.NativeQuote})
	if err != nil {
		// Unreachable for the types above; an unkeyable request is never merged.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// canonicalInput decodes the normalized input (numbers kept exact) and swaps
// every string that is an input asset ID for that asset's content digest.
// json.Marshal then writes map keys in sorted order.
func canonicalInput(raw json.RawMessage, bySHA map[string]string) any {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(raw)
	}
	var walk func(any) any
	walk = func(node any) any {
		switch typed := node.(type) {
		case string:
			if replaced, ok := bySHA[typed]; ok {
				return replaced
			}
			return typed
		case []any:
			for index := range typed {
				typed[index] = walk(typed[index])
			}
			return typed
		case map[string]any:
			for key := range typed {
				typed[key] = walk(typed[key])
			}
			return typed
		default:
			return node
		}
	}
	return walk(value)
}

// approvalNeedsHuman reports whether the session's approval mode would leave
// this step waiting for a person (the only steps that get a card): a capability
// the manual policy lists, or any step that spends extra credits.
func (executor *Executor) approvalNeedsHuman(ctx context.Context, owner, session string,
	capability generation.Capability, extraCredits int) bool {
	return executor.approvalNeedsHumanSpend(ctx, owner, session, capability, extraCredits > 0)
}
func (executor *Executor) approvalNeedsHumanSpend(ctx context.Context, owner, session string, capability generation.Capability, extra bool) bool {
	if executor.grants == nil || executor.grants.Modes == nil {
		return true // without a mode reader every grant waits for a human
	}
	mode, err := executor.grants.Modes.SessionApprovalMode(ctx, owner, session)
	if err != nil {
		return false // unknown: the normal path reports the failure
	}
	return approvalpolicy.RequiresHuman(executor.grants.Policy, mode, string(capability)) ||
		approvalpolicy.RequiresHumanForExtraSpend(executor.grants.Policy, mode, extra)
}

// lockApprovals serializes card creation of one session, so two calls of one
// turn that ask for the same thing cannot both pass the duplicate check before
// either card exists. It is held until the first of them is detached. The
// returned release is safe to call more than once.
func (executor *Executor) lockApprovals(owner, session string) func() {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(owner + "\x00" + session))
	mutex := &executor.approvalLocks[hash.Sum32()%uint32(len(executor.approvalLocks))]
	mutex.Lock()
	return sync.OnceFunc(mutex.Unlock)
}

// pendingDuplicate finds a card of the same session that is still waiting for a
// person and asks for exactly what the new call asks for.
func (executor *Executor) pendingDuplicate(
	ctx context.Context, command chat.ToolCommand, prepared generation.PreparedInput,
) (Operation, bool, error) {
	key := ApprovalRequestKey(command.Call.Name, prepared)
	if key == "" {
		return Operation{}, false, nil
	}
	limit := executor.chat.PendingApprovals.MaximumListed
	if limit <= 0 {
		return Operation{}, false, nil
	}
	ops, err := executor.operations.ListPendingDetached(ctx, command.OwnerID, command.SessionID,
		limit, executor.grants.ExpiryCutoff())
	if err != nil {
		return Operation{}, false, err
	}
	preparedGateway := executor.gateway.(preparedGateway)
	for _, op := range ops {
		if op.Harness != turnHarnessBinding(command) || op.ToolName != command.Call.Name || op.OfferingID != prepared.OfferingID {
			continue
		}
		grant, err := executor.grants.Grants.GetExecutionGrant(ctx, op.OwnerID, op.GrantRequestID)
		if err != nil {
			return Operation{}, false, err
		}
		if !agentGrantMatchesOperation(grant, op) || grant.Status != executiongrant.Pending || executor.grants.Expired(grant) {
			continue
		}
		earlier, err := preparedGateway.GetPreparedInput(ctx, generation.OwnerID(op.OwnerID), op.PreparedInputID)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return Operation{}, false, err
			}
			continue // an unreadable earlier record is never merged into
		}
		if earlier.SessionID == command.SessionID && ApprovalRequestKey(op.ToolName, earlier) == key {
			return op, true, nil
		}
	}
	return Operation{}, false, nil
}

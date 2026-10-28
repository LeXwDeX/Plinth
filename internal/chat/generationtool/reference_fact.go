package generationtool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// ReferenceSourceFact freezes what a model step's reference pictures actually
// were, after a three-view group's views were replaced by its front view, the
// list was de-duplicated and cut to the Offering's limit. It is decided once,
// with the prepared input, and read back on every later approval or result; no
// part is inferred from today's three-view groups or from an image digest.
//
// Empty Identity means the fact is unknown (an operation written before this
// was kept). Such a fact makes no source or order claim.
type ReferenceSourceFact struct {
	Version int `json:"version"`
	// Selected is how many reference pictures the turn selected.
	Selected int `json:"selected"`
	// Resolved is how many remained after replacing three-view views and
	// removing repeated pictures, before the Offering's limit cut the list.
	Resolved int `json:"resolved"`
	// Frozen is how many the prepared input actually uses.
	Frozen int `json:"frozen"`
	// Replaced means a three-view view was replaced by its front view.
	Replaced bool `json:"replaced,omitempty"`
	// Deduped means a repeated picture was dropped.
	Deduped bool `json:"deduped,omitempty"`
	// AllFront means every frozen picture is the same verified front view.
	AllFront bool `json:"all_front,omitempty"`
	// Prefix means the frozen pictures are exactly the first Frozen pictures
	// of the turn's selection, so the Offering's "only the first N" text says
	// the truth.
	Prefix bool `json:"prefix,omitempty"`
	// FrontParameter and FrontIndex name where the front view landed in the
	// prepared input's reference parameter. They are meaningful only when
	// FrontTaskID and FrontAssetID name that verified source.
	FrontParameter string `json:"front_parameter,omitempty"`
	FrontIndex     int    `json:"front_index,omitempty"`
	// FrontTaskID and FrontAssetID are the verified three-view source pair.
	FrontTaskID  string `json:"front_task_id,omitempty"`
	FrontAssetID string `json:"front_asset_id,omitempty"`
	// Identity covers the original and retained source identities in order.
	// It never stores a URL or a data URI.
	Identity string `json:"identity,omitempty"`
	// Trusted marks a fact computed for this call's direct (unteathered)
	// result. It is never serialized: a stored fact is trusted only through
	// its Identity.
	Trusted bool `json:"-"`
}

func (fact ReferenceSourceFact) known() bool { return fact.Identity != "" || fact.Trusted }

// referenceOrigin is one picture the turn selected, named without storing the
// selected URL.
type referenceOrigin struct {
	Index   int
	TaskID  string
	AssetID string
	// Digest is the SHA-256 of a reference that does not name a platform task
	// asset (an upload address or a data URI); URL and content are not kept.
	Digest string
}

// referenceCandidate is one picture after replacing three-view views and
// de-duplication, before the Offering's limit.
type referenceCandidate struct {
	URL          string
	Origin       referenceOrigin
	FrontTaskID  string
	FrontAssetID string
}

// referenceSelection is the result of resolving the turn's pictures for an
// image-to-3D step.
type referenceSelection struct {
	Given      int
	Origins    []referenceOrigin
	Candidates []referenceCandidate
	Replaced   bool
	Deduped    bool
}

func (selection referenceSelection) urls() []string {
	urls := make([]string, 0, len(selection.Candidates))
	for _, candidate := range selection.Candidates {
		urls = append(urls, candidate.URL)
	}
	return urls
}

func referenceDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// referenceFact is the source fact of a step that has no prepared input yet
// (the legacy/harness path that submits directly). It cannot be persisted and
// carries no Identity; it is used only to word this call's result.
func referenceFact(selection referenceSelection, final []referenceCandidate) *ReferenceSourceFact {
	if len(final) == 0 {
		return nil
	}
	fact := &ReferenceSourceFact{
		Version:  1,
		Selected: selection.Given,
		Resolved: len(selection.Candidates),
		Frozen:   len(final),
		Replaced: selection.Replaced,
		Deduped:  selection.Deduped,
		Trusted:  true,
	}
	fronts := map[string]struct{}{}
	for _, candidate := range final {
		if candidate.FrontTaskID == "" || candidate.FrontAssetID == "" {
			continue
		}
		fronts[candidate.FrontTaskID+"\x00"+candidate.FrontAssetID] = struct{}{}
		if fact.FrontTaskID == "" {
			fact.FrontTaskID, fact.FrontAssetID = candidate.FrontTaskID, candidate.FrontAssetID
		}
	}
	fact.AllFront = len(fronts) == 1 && len(final) == 1
	fact.Prefix = !selection.Replaced && !selection.Deduped && fact.Frozen <= fact.Selected
	return fact
}

// preparedReferenceFact verifies that each retained picture is the prepared
// input's own asset, or a compression derived from it within the same owner,
// then freezes the source fact with an identity that covers the original and
// retained sources. It returns an error instead of a fact when that cannot be
// proved; the caller must not create a grant or a Task then.
func (executor *Executor) preparedReferenceFact(
	ctx context.Context,
	command chat.ToolCommand,
	offering generation.Offering,
	selection referenceSelection,
	final []referenceCandidate,
	resolved []generation.AssetID,
	prepared generation.PreparedInput,
) (*ReferenceSourceFact, error) {
	if prepared.Capability != generation.ImageTo3D || offering.ReferenceSelectionPolicy == "preserve" ||
		len(final) == 0 {
		return nil, nil
	}
	parameter, ok := referenceParameterOf(offering)
	if !ok {
		return nil, nil
	}
	if len(resolved) != len(final) {
		return nil, fmt.Errorf("reference source: resolved %d pictures for %d retained", len(resolved), len(final))
	}
	assets := make([]generation.PreparedInputAsset, 0, len(final))
	for _, asset := range prepared.Assets {
		if asset.Parameter == parameter.Name {
			assets = append(assets, asset)
		}
	}
	if len(assets) != len(final) {
		return nil, fmt.Errorf("reference source: prepared %d pictures for %d retained", len(assets), len(final))
	}
	deriver, _ := executor.gateway.(preparedInputDerivation)
	for index, asset := range assets {
		if asset.Index != index {
			return nil, fmt.Errorf("reference source: prepared picture %d is at index %d", index, asset.Index)
		}
		if asset.ID == resolved[index] {
			continue
		}
		if deriver == nil {
			return nil, fmt.Errorf("reference source: prepared picture %d is not the resolved picture", index)
		}
		source, derived, err := deriver.InputAssetDerivationSource(ctx, generation.OwnerID(command.OwnerID), asset.ID)
		if err != nil {
			return nil, fmt.Errorf("reference source: read derivation: %w", err)
		}
		if !derived || source != resolved[index] {
			return nil, fmt.Errorf("reference source: prepared picture %d is not derived from the resolved picture", index)
		}
	}
	if err := verifyPreparedReferenceValue(prepared.NormalizedInput, parameter, assets); err != nil {
		return nil, err
	}
	fact := referenceFact(selection, final)
	if fact == nil {
		return nil, nil
	}
	for index, candidate := range final {
		if candidate.FrontTaskID != "" && candidate.FrontAssetID != "" {
			fact.FrontParameter, fact.FrontIndex = parameter.Name, index
			break
		}
	}
	fact.Identity = referenceFactIdentity(selection, final, assets)
	fact.Trusted = false
	return fact, nil
}

// preparedInputDerivation is the owner-scoped proof that a prepared input
// asset is a compression derived from another input asset.
type preparedInputDerivation interface {
	InputAssetDerivationSource(context.Context, generation.OwnerID, generation.AssetID) (generation.AssetID, bool, error)
}

func referenceParameterOf(offering generation.Offering) (generation.Parameter, bool) {
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == generation.SemanticReferenceImage &&
			(parameter.Type == "asset" || parameter.Type == "asset-array") {
			return parameter, true
		}
	}
	return generation.Parameter{}, false
}

func verifyPreparedReferenceValue(raw json.RawMessage, parameter generation.Parameter, assets []generation.PreparedInputAsset) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("reference source: prepared input is not an object: %w", err)
	}
	encoded, present := values[parameter.Name]
	if !present {
		return fmt.Errorf("reference source: prepared input has no reference parameter")
	}
	var ids []string
	if parameter.Type == "asset" {
		var single string
		if err := json.Unmarshal(encoded, &single); err != nil {
			return fmt.Errorf("reference source: prepared reference is not one asset")
		}
		ids = []string{single}
	} else if err := json.Unmarshal(encoded, &ids); err != nil {
		return fmt.Errorf("reference source: prepared references are not a list")
	}
	if len(ids) != len(assets) {
		return fmt.Errorf("reference source: frozen input names %d pictures but the prepared assets name %d", len(ids), len(assets))
	}
	for index := range ids {
		if generation.AssetID(ids[index]) != assets[index].ID {
			return fmt.Errorf("reference source: frozen input picture %d does not match the prepared asset", index)
		}
	}
	return nil
}

// referenceFactIdentity hashes the ordered source identities and the retained
// content. It deliberately excludes copied asset IDs and any URL text, so two
// preparations of one call agree while two different source Tasks that happen
// to hold the same bytes do not.
func referenceFactIdentity(selection referenceSelection, final []referenceCandidate, assets []generation.PreparedInputAsset) string {
	type source struct {
		Index   int    `json:"index"`
		TaskID  string `json:"task_id,omitempty"`
		AssetID string `json:"asset_id,omitempty"`
		Digest  string `json:"digest,omitempty"`
	}
	type frozen struct {
		Parameter string `json:"parameter"`
		Index     int    `json:"index"`
		SHA256    string `json:"sha256"`
		TaskID    string `json:"task_id,omitempty"`
		AssetID   string `json:"asset_id,omitempty"`
		Digest    string `json:"digest,omitempty"`
	}
	origins := make([]source, 0, len(selection.Origins))
	for _, origin := range selection.Origins {
		origins = append(origins, source{Index: origin.Index, TaskID: origin.TaskID, AssetID: origin.AssetID, Digest: origin.Digest})
	}
	retained := make([]frozen, 0, len(final))
	for index, candidate := range final {
		asset := assets[index]
		retained = append(retained, frozen{Parameter: asset.Parameter, Index: asset.Index, SHA256: asset.SHA256,
			TaskID: candidate.FrontTaskID, AssetID: candidate.FrontAssetID,
			Digest: candidate.Origin.Digest})
	}
	value := struct {
		Version  int      `json:"version"`
		Selected int      `json:"selected"`
		Resolved int      `json:"resolved"`
		Frozen   int      `json:"frozen"`
		Origins  []source `json:"origins"`
		Retained []frozen `json:"retained"`
	}{1, selection.Given, len(selection.Candidates), len(final), origins, retained}
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// ReferenceSourceNote is the one sentence that says which pictures a step
// uses. A verified front view that is the only frozen picture gets the
// three-view note. A real prefix of the turn's selection gets the Offering's
// reference-limit note. A replacement or de-duplication that breaks that
// prefix claim gets a neutral count, never a guessed source. An unknown fact
// makes no claim.
func ReferenceSourceNote(tool catalog.ChatToolSchema, offering generation.Offering,
	frontNote string, fact ReferenceSourceFact, text func(string) string) string {
	if tool.ResultFields.ReferenceNote == "" || offering.ReferenceSelectionPolicy == "preserve" ||
		!fact.known() {
		return ""
	}
	if fact.AllFront && fact.Frozen == 1 && frontNote != "" {
		if text != nil {
			return text(frontNote)
		}
		return frontNote
	}
	if fact.Frozen < fact.Selected && fact.Prefix {
		return ReferenceLimitNote(tool, offering, fact.Frozen, fact.Selected, text)
	}
	if fact.Replaced || fact.Deduped {
		template := offering.ExecutionMessages.ReferenceSelectionNote
		if template == "" {
			return ""
		}
		model := OfferingModelName(offering)
		if text != nil {
			template, model = text(template), text(model)
		}
		return generation.ReferenceLimit{Model: model, Count: fact.Frozen, Total: fact.Selected}.Note(template)
	}
	return ""
}

// referenceNoteFor writes the step's source note from the fact it froze. A
// step that never had a source fact (a non-image-to-3D step, or a legacy
// operation) uses the older limit counts; an image-to-3D legacy operation
// makes no claim it cannot prove.
func (executor *Executor) referenceNoteFor(command chat.ToolCommand, offering generation.Offering,
	fact *ReferenceSourceFact, counts referenceUse) string {
	if command.Tool.ResultFields.ReferenceNote == "" || offering.ReferenceSelectionPolicy == "preserve" {
		return ""
	}
	text := executor.localizeText(command)
	if fact != nil {
		return ReferenceSourceNote(command.Tool, offering, executor.chat.ThreeView.ModelSourceNote, *fact, text)
	}
	if offering.Capability == generation.ImageTo3D {
		return ""
	}
	return ReferenceLimitNote(command.Tool, offering, counts.Used, counts.Given, text)
}

// sameReferenceSource compares source facts as stored, ignoring the transient
// trusted marker that is never serialized.
func sameReferenceSource(a, b ReferenceSourceFact) bool {
	a.Trusted, b.Trusted = false, false
	return a == b
}

// samePreparedOperation reports whether a stored operation is the same
// prepared request as the candidate, even when the two preparations copied the
// input into different asset IDs. The call, frozen content and source identity
// must all agree; only then may the candidate use the winner's grant.
func (executor *Executor) samePreparedOperation(
	ctx context.Context,
	command chat.ToolCommand,
	candidate generation.PreparedInput,
	fact *ReferenceSourceFact,
	requireSourceFact bool,
	stored Operation,
) (bool, error) {
	if stored.OwnerID != command.OwnerID || stored.SessionID != command.SessionID ||
		stored.AssistantMessageID != command.AssistantMessageID || stored.CallID != command.Call.ID ||
		stored.ToolName != command.Call.Name || stored.IdempotencyKey != command.IdempotencyKey() ||
		stored.Harness != turnHarnessBinding(command) {
		return false, nil
	}
	gateway, ok := executor.gateway.(preparedGateway)
	if !ok {
		return false, nil
	}
	storedPrepared, err := gateway.GetPreparedInput(ctx, generation.OwnerID(stored.OwnerID), stored.PreparedInputID)
	if err != nil {
		return false, nil
	}
	if string(storedPrepared.OwnerID) != stored.OwnerID || storedPrepared.SessionID != stored.SessionID ||
		storedPrepared.InputHash != stored.PreparedInputHash ||
		storedPrepared.OfferingID != stored.OfferingID || storedPrepared.OfferingRevision != stored.OfferingRevision {
		return false, nil
	}
	if candidate.Capability != storedPrepared.Capability || candidate.SessionID != storedPrepared.SessionID ||
		candidate.OfferingID != storedPrepared.OfferingID || candidate.OfferingRevision != storedPrepared.OfferingRevision ||
		candidate.SchemaVersion != storedPrepared.SchemaVersion ||
		candidate.SourceSceneRepresentationID != storedPrepared.SourceSceneRepresentationID {
		return false, nil
	}
	candidateKey := ApprovalRequestKey(command.Call.Name, candidate)
	if candidateKey == "" || candidateKey != ApprovalRequestKey(command.Call.Name, storedPrepared) {
		return false, nil
	}
	if fact == nil || !fact.known() {
		if stored.ReferenceSource.known() {
			return false, nil
		}
		// An image-to-3D step with references, outside preserve, must prove its
		// source before it can inherit a stored operation. A preserve step or a
		// step without pictures has no source fact by design.
		return !requireSourceFact, nil
	}
	return stored.ReferenceSource.known() && sameReferenceSource(*fact, stored.ReferenceSource), nil
}

// operationReferenceNote words a stored operation's reference source note. It
// reads only the operation's frozen fact; it never queries the session or a
// three-view group again. An image-to-3D operation without a fact makes no
// source claim.
func (executor *Executor) operationReferenceNote(command chat.ToolCommand, op Operation,
	prepared generation.PreparedInput, offering generation.Offering) string {
	var fact *ReferenceSourceFact
	if prepared.Capability == generation.ImageTo3D && op.ReferenceSource.known() {
		value := op.ReferenceSource
		fact = &value
	}
	return executor.referenceNoteFor(command, offering, fact,
		referenceUse{Used: op.ReferenceCount, Given: op.ReferenceTotal})
}

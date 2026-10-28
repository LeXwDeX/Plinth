package generationtool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #80: the stored source fact is the only input to a later result or card
// sentence. A legacy image-to-3D operation without a fact makes no claim.
func TestStoredReferenceSourceNote(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var tool catalog.ChatToolSchema
	for _, candidate := range application.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	if tool.ResultFields.ReferenceNote == "" {
		t.Fatal("image-to-3D tool has no reference note")
	}
	offering := generation.Offering{
		Capability:        generation.ImageTo3D,
		ExecutionMessages: application.Runtime.GenerationMessages,
		Parameters: []generation.Parameter{{
			Name: "reference_assets", Type: "asset-array", Semantic: generation.SemanticReferenceImage, MaxItems: 1,
		}},
	}
	executor := Executor{chat: application.Chat}
	command := chat.ToolCommand{Tool: tool}
	prepared := generation.PreparedInput{Capability: generation.ImageTo3D}
	limit := ReferenceLimitNote(tool, offering, 1, 2, nil)

	op := Operation{ReferenceCount: 1, ReferenceTotal: 2, ReferenceSource: ReferenceSourceFact{
		Version: 1, Selected: 2, Resolved: 2, Frozen: 1, Prefix: true, Identity: "stored-prefix"}}
	if note := executor.operationReferenceNote(command, op, prepared, offering); note != limit || limit == "" {
		t.Fatalf("stored prefix note = %q, want %q", note, limit)
	}

	legacy := Operation{ReferenceCount: 1, ReferenceTotal: 2}
	if note := executor.operationReferenceNote(command, legacy, prepared, offering); note != "" {
		t.Fatalf("legacy image-to-3D note = %q, want no claim", note)
	}

	front := ReferenceSourceFact{Version: 1, Selected: 2, Resolved: 1, Frozen: 1, Replaced: true,
		AllFront: true, FrontTaskID: "task-front", FrontAssetID: "asset-front", Identity: "stored-front"}
	op.ReferenceSource = front
	if note := executor.operationReferenceNote(command, op, prepared, offering); note != application.Chat.ThreeView.ModelSourceNote ||
		strings.Contains(note, "{model}") {
		t.Fatalf("stored front note = %q", note)
	}
}

func TestDeduplicatedReferenceSourceNoteEnglishCount(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	localizer, err := catalog.NewTextLocalizer(app)
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("hyper3d/rodin-gen-2.5/image-to-3d")
	if !ok {
		t.Fatal("missing Rodin offering")
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	for _, tc := range []struct {
		count int
		want  string
	}{{1, "reference picture count for this step is 1"}, {2, "reference picture count for this step is 2"}} {
		fact := ReferenceSourceFact{Selected: tc.count + 1, Resolved: tc.count, Frozen: tc.count, Deduped: true, Identity: "frozen"}
		note := ReferenceSourceNote(tool, offering, app.Chat.ThreeView.ModelSourceNote, fact, func(value string) string { return localizer.Text("en", value) })
		if !strings.Contains(note, tc.want) {
			t.Fatalf("count %d: %q", tc.count, note)
		}
	}
	offering.ExecutionMessages.ReferenceSelectionNote = ""
	fact := ReferenceSourceFact{Selected: 2, Resolved: 1, Frozen: 1, Deduped: true, Identity: "frozen"}
	if note := ReferenceSourceNote(tool, offering, app.Chat.ThreeView.ModelSourceNote, fact, nil); note != "" {
		t.Fatalf("older schema without a selection note made a claim: %q", note)
	}
}

// staticPreparedInputs is the smallest prepared-input reader a concurrency
// equivalence check needs.
type staticPreparedInputs map[generation.PreparedInputID]generation.PreparedInput

func (s staticPreparedInputs) PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error) {
	panic("unexpected prepare")
}
func (s staticPreparedInputs) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	panic("unexpected submit")
}
func (s staticPreparedInputs) GetPreparedInput(_ context.Context, owner generation.OwnerID,
	id generation.PreparedInputID) (generation.PreparedInput, error) {
	prepared, ok := s[id]
	if !ok || prepared.OwnerID != owner {
		return generation.PreparedInput{}, &generation.Error{Code: generation.ErrNotFound, Operation: "test"}
	}
	return prepared, nil
}

type staticGateway struct {
	staticPreparedInputs
	derivations map[generation.AssetID]generation.AssetID
}

func (g staticGateway) InputAssetDerivationSource(_ context.Context, _ generation.OwnerID,
	id generation.AssetID) (generation.AssetID, bool, error) {
	source, ok := g.derivations[id]
	return source, ok, nil
}

func (staticGateway) Offerings(generation.Capability) []generation.Offering { return nil }
func (staticGateway) OfferingAvailable(generation.OfferingID) bool          { return false }
func (staticGateway) Submit(context.Context, generation.SubmitCommand) (generation.Accepted, error) {
	panic("unexpected submit")
}
func (staticGateway) Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error) {
	panic("unexpected get")
}

// #80: two preparations of one call copy the input into different asset IDs.
// The content and the source fact decide whether the later call may reuse the
// stored operation; a different source never does.
func TestSamePreparedOperationIgnoresCopyIDs(t *testing.T) {
	sha := strings.Repeat("a", 64)
	stored := generation.PreparedInput{ID: "prepared-stored", OwnerID: "owner-a", SessionID: "session-a",
		Capability: generation.ImageTo3D, OfferingID: "fake/offering", OfferingRevision: "rev-a",
		SchemaVersion: "1", NormalizedInput: json.RawMessage(`{"reference_assets":["copy-stored"]}`),
		Assets:    []generation.PreparedInputAsset{{Parameter: "reference_assets", Index: 0, ID: "copy-stored", SHA256: sha, Size: 4, MediaType: "image/png"}},
		InputHash: "hash-stored"}
	candidate := stored
	candidate.ID, candidate.InputHash = "prepared-candidate", "hash-candidate"
	candidate.NormalizedInput = json.RawMessage(`{"reference_assets":["copy-candidate"]}`)
	candidate.Assets = []generation.PreparedInputAsset{{Parameter: "reference_assets", Index: 0, ID: "copy-candidate", SHA256: sha, Size: 4, MediaType: "image/png"}}
	tool := catalog.ChatToolSchema{Name: "create_model_3d_from_image", IdempotencyKeyTemplate: "agent:{session_id}:{call_id}"}
	command := chat.ToolCommand{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a",
		Call: agents.ToolCall{ID: "call-a", Name: tool.Name}, Tool: tool}
	op := Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a", CallID: "call-a",
		ToolName: tool.Name, IdempotencyKey: command.IdempotencyKey(), PreparedInputID: stored.ID,
		PreparedInputHash: stored.InputHash, OfferingID: stored.OfferingID, OfferingRevision: stored.OfferingRevision,
		ReferenceSource: ReferenceSourceFact{Version: 1, Selected: 1, Resolved: 1, Frozen: 1, Identity: "source-a"}}
	fact := op.ReferenceSource
	executor := Executor{gateway: staticGateway{staticPreparedInputs: staticPreparedInputs{stored.ID: stored}}}
	if equal, err := executor.samePreparedOperation(context.Background(), command, candidate, &fact, true, op); err != nil || !equal {
		t.Fatalf("same content/fact = %v %v, want true", equal, err)
	}
	other := fact
	other.Identity = "source-b"
	if equal, _ := executor.samePreparedOperation(context.Background(), command, candidate, &other, true, op); equal {
		t.Fatal("a different source fact reused the stored operation")
	}
	if equal, _ := executor.samePreparedOperation(context.Background(), command, candidate, nil, true, op); equal {
		t.Fatal("a missing source fact reused a known source")
	}
	op.ReferenceSource = ReferenceSourceFact{}
	if equal, _ := executor.samePreparedOperation(context.Background(), command, candidate, nil, false, op); !equal {
		t.Fatal("a preserve or picture-free operation without a fact did not match")
	}
}

// #80: a prepared input may hold a compressed copy of the resolved input
// asset. The source fact accepts it only through the owner-scoped derivation
// proof and keeps the front/selection facts unchanged.
func TestPreparedReferenceFactAcceptsAnOwnerDerivedCopy(t *testing.T) {
	owner := generation.OwnerID("owner-a")
	parameter := generation.Parameter{Name: "reference_assets", Type: "asset-array",
		Semantic: generation.SemanticReferenceImage, MaxItems: 1}
	offering := generation.Offering{Capability: generation.ImageTo3D,
		Parameters: []generation.Parameter{parameter}}
	selection := referenceSelection{Given: 1, Origins: []referenceOrigin{{Index: 0, Digest: "url"}},
		Candidates: []referenceCandidate{{URL: "/upload/one", Origin: referenceOrigin{Index: 0, Digest: "url"}}}}
	final := selection.Candidates
	prepared := generation.PreparedInput{ID: "prepared-derived", OwnerID: owner, SessionID: "session-a",
		Capability: generation.ImageTo3D, OfferingID: "fake/offering", OfferingRevision: "rev-a",
		SchemaVersion: "1", NormalizedInput: json.RawMessage(`{"reference_assets":["derived-copy"]}`),
		Assets: []generation.PreparedInputAsset{{Parameter: "reference_assets", Index: 0, ID: "derived-copy",
			SHA256: strings.Repeat("b", 64), Size: 3, MediaType: "image/jpeg"}}, InputHash: "hash-derived"}
	command := chat.ToolCommand{OwnerID: string(owner), Call: agents.ToolCall{ID: "call-a"}}
	executor := Executor{gateway: staticGateway{
		staticPreparedInputs: staticPreparedInputs{prepared.ID: prepared},
		derivations:          map[generation.AssetID]generation.AssetID{"derived-copy": "copy-resolved"},
	}}
	fact, err := executor.preparedReferenceFact(context.Background(), command, offering, selection, final,
		[]generation.AssetID{"copy-resolved"}, prepared)
	if err != nil || fact == nil || fact.Identity == "" || fact.Frozen != 1 || !fact.Prefix ||
		fact.Selected != 1 || fact.Resolved != 1 {
		t.Fatalf("derived fact = %+v err=%v", fact, err)
	}
}

func TestRodinPreparedReferenceFactFrontPositionMatchesIdentity(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := product.Get("hyper3d/rodin-gen-2.5/image-to-3d")
	if !ok || offering.ReferenceSelectionPolicy == "preserve" {
		t.Fatal("expected non-preserve Rodin offering")
	}
	parameter, ok := referenceParameterOf(offering)
	if !ok || parameter.Name != "reference_assets" || parameter.MaxItems < 2 {
		t.Fatalf("unexpected reference parameter: %+v", parameter)
	}
	frontA := referenceCandidate{FrontTaskID: "front-a", FrontAssetID: "asset-a"}
	frontB := referenceCandidate{FrontTaskID: "front-b", FrontAssetID: "asset-b"}
	ordinary := referenceCandidate{Origin: referenceOrigin{Digest: "ordinary"}}
	truncated := []referenceCandidate{ordinary, frontA}
	for len(truncated) < parameter.MaxItems {
		truncated = append(truncated, referenceCandidate{Origin: referenceOrigin{Digest: string(rune('a' + len(truncated)))}})
	}
	truncated = append(truncated, frontB)
	for _, tc := range []struct {
		name       string
		candidates []referenceCandidate
		retained   int
		index      int
		front      referenceCandidate
	}{
		{"a then b", []referenceCandidate{frontA, frontB}, 2, 0, frontA},
		{"b then a", []referenceCandidate{frontB, frontA}, 2, 0, frontB},
		{"ordinary then fronts", []referenceCandidate{ordinary, frontA, frontB}, 3, 1, frontA},
		{"last front truncated at Rodin limit", truncated, parameter.MaxItems, 1, frontA},
		{"only front truncated", []referenceCandidate{ordinary, frontA}, 1, 0, referenceCandidate{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection := referenceSelection{Given: len(tc.candidates), Candidates: tc.candidates, Replaced: true}
			final := tc.candidates[:tc.retained]
			ids := make([]generation.AssetID, len(final))
			assets := make([]generation.PreparedInputAsset, len(final))
			for i := range final {
				ids[i] = generation.AssetID(string(rune('a' + i)))
				assets[i] = generation.PreparedInputAsset{Parameter: parameter.Name, Index: i, ID: ids[i], SHA256: string(ids[i])}
			}
			input, err := json.Marshal(map[string][]generation.AssetID{parameter.Name: ids})
			if err != nil {
				t.Fatal(err)
			}
			prepared := generation.PreparedInput{Capability: generation.ImageTo3D, NormalizedInput: input, Assets: assets}
			executor := Executor{gateway: staticGateway{}}
			fact, err := executor.preparedReferenceFact(context.Background(), chat.ToolCommand{OwnerID: "owner"}, offering, selection, final, ids, prepared)
			if err != nil || fact == nil {
				t.Fatalf("prepared source fact: %+v, %v", fact, err)
			}
			wantParameter := parameter.Name
			if tc.front.FrontTaskID == "" {
				wantParameter = ""
			}
			if fact.FrontParameter != wantParameter || fact.FrontIndex != tc.index || fact.FrontTaskID != tc.front.FrontTaskID || fact.FrontAssetID != tc.front.FrontAssetID {
				t.Fatalf("front identity does not match position: %+v; want parameter=%q index=%d task=%q asset=%q", fact, wantParameter, tc.index, tc.front.FrontTaskID, tc.front.FrontAssetID)
			}
		})
	}
}

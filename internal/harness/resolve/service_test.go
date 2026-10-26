package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

type memoryStore struct {
	selection harness.Selection
	err       error
	writes    int
}

func (s *memoryStore) GetHarnessSelection(_ context.Context, owner, session string) (harness.Selection, error) {
	if s.err != nil {
		return harness.Selection{}, s.err
	}
	if owner != s.selection.OwnerID || session != s.selection.SessionID {
		return harness.Selection{}, harness.ErrNotFound
	}
	return s.selection, nil
}
func (s *memoryStore) CompareAndSwapHarnessSelection(_ context.Context, candidate harness.Selection, revision int64) (harness.Selection, error) {
	if s.err != nil {
		return harness.Selection{}, s.err
	}
	if revision != s.selection.Revision {
		return harness.Selection{}, harness.ErrConflict
	}
	s.writes++
	candidate.Revision = revision + 1
	s.selection = candidate
	return candidate, nil
}
func (s *memoryStore) GetHarnessSelectionVersion(context.Context, string, string, harness.Binding) (harness.Selection, error) {
	panic("unexpected version read")
}

type readGateway struct {
	task        generation.Task
	err         error
	unavailable map[generation.OfferingID]bool
	reads       int
	paid        int
}

func (g *readGateway) OfferingAvailable(id generation.OfferingID) bool { return !g.unavailable[id] }
func (g *readGateway) Get(_ context.Context, _ generation.OwnerID, _ generation.TaskID) (generation.Task, error) {
	g.reads++
	return g.task, g.err
}

// These are intentionally outside Gateway: the resolver must never invoke them.
func (g *readGateway) PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error) {
	g.paid++
	return generation.PreparedInput{}, nil
}
func (g *readGateway) SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error) {
	g.paid++
	return generation.Accepted{}, nil
}

func fixture(t *testing.T) (Service, *memoryStore, *readGateway) {
	t.Helper()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := c.Application().Chat
	store := &memoryStore{selection: harness.DefaultSelection("owner", "session")}
	gateway := &readGateway{task: generation.Task{ID: "model-task", OwnerID: "owner", SessionID: "session", State: generation.TaskSucceeded, Capability: generation.TextTo3D, Offering: generation.Offering{ID: "meshy/meshy-7.1/text-to-3d", ProviderID: "meshy"}, Assets: []generation.Asset{{ID: "model-asset", TaskID: "model-task", Role: "model", Variant: "glb", StorageKey: "saved/model.glb", MediaType: "model/gltf-binary", SHA256: "actual-hash", Size: 123}}}, unavailable: map[generation.OfferingID]bool{}}
	return Service{Store: store, Schema: chat.Harness, Tools: chat.Tools, Catalog: c, Gateway: gateway}, store, gateway
}
func choose(t *testing.T, s Service, operation string) harness.Selection {
	t.Helper()
	var op catalog.HarnessOperationSchema
	for _, v := range s.Schema.Operations {
		if v.ID == operation {
			op = v
		}
	}
	if op.ID == "" {
		t.Fatal("unknown fixture operation", operation)
	}
	offerings := s.Catalog.List(op.Capability)
	if len(offerings) == 0 {
		t.Fatal("missing fixture offering", operation)
	}
	o := offerings[0]
	selection := harness.DefaultSelection("owner", "session")
	selection.OperationID = operation
	selection.OfferingID = string(o.ID)
	selection.OfferingRevision = o.Revision
	selection.EnabledOperations = []string{operation}
	return selection
}
func modelSource() []harness.Source {
	return []harness.Source{{Parameter: "source_task", TaskID: "model-task", AssetID: "model-asset"}}
}

func TestFreeSelectionHasRealBindingAndClearsOperationFields(t *testing.T) {
	s, store, g := fixture(t)
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusFree || snapshot.Selection.Digest == "" {
		t.Fatalf("free %+v %v", snapshot, err)
	}
	selection := choose(t, s, "remesh")
	selection.OperationID = ""
	selection.Sources = modelSource()
	selection.FixedParameters = map[string]json.RawMessage{"bad": json.RawMessage(`true`)}
	selection.AllowPrerequisites = true
	updated, err := s.Update(context.Background(), "owner", "session", 0, selection)
	if err != nil {
		t.Fatal(err)
	}
	if updated.OfferingID != "" || len(updated.Sources) > 0 || len(updated.FixedParameters) > 0 || updated.AllowPrerequisites {
		t.Fatalf("free retained constraints %+v", updated)
	}
	if store.writes != 1 || g.paid != 0 || g.reads != 0 {
		t.Fatal("free selection invoked execution")
	}
}

func TestUpdateRejectsInvalidOperationOfferingRevisionAndParameters(t *testing.T) {
	cases := map[string]func(*harness.Selection){
		"unknown operation": func(s *harness.Selection) { s.OperationID = "invented" },
		"disabled active":   func(s *harness.Selection) { s.EnabledOperations = []string{"rigging"} },
		"unknown enabled":   func(s *harness.Selection) { s.EnabledOperations = append(s.EnabledOperations, "invented") },
		"unknown offering":  func(s *harness.Selection) { s.OfferingID = "invented" },
		"changed revision":  func(s *harness.Selection) { s.OfferingRevision = "changed" },
		"wrong capability": func(s *harness.Selection) {
			s.OfferingID = "meshy/processing/animation"
			s.OfferingRevision = "2026-09-30.1"
		},
		"unknown parameter": func(s *harness.Selection) {
			s.FixedParameters = map[string]json.RawMessage{"invented": json.RawMessage(`true`)}
		},
		// Meshy Remesh has no hidden parameter since #330; Tripo's remesh
		// (Decimate) hides its extra-credit consent.
		"hidden parameter": func(s *harness.Selection) {
			c, _ := catalog.LoadDefault()
			decimate, _ := c.Get("tripo/geometry/decimate-triangle")
			s.OfferingID, s.OfferingRevision = string(decimate.ID), decimate.Revision
			s.FixedParameters = map[string]json.RawMessage{"authorize_extra_credits": json.RawMessage(`true`)}
		},
		"fixed parameter":  func(s *harness.Selection) { s.FillableParameters = []string{"target_formats"} },
		"source parameter": func(s *harness.Selection) { s.FillableParameters = []string{"source_task"} },
		"invalid value": func(s *harness.Selection) {
			s.FixedParameters = map[string]json.RawMessage{"topology": json.RawMessage(`"invalid"`)}
		},
		"overlap": func(s *harness.Selection) {
			s.FixedParameters = map[string]json.RawMessage{"topology": json.RawMessage(`"quad"`)}
			s.FillableParameters = []string{"topology"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, store, g := fixture(t)
			candidate := choose(t, s, "remesh")
			mutate(&candidate)
			if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); !errors.Is(err, harness.ErrInvalid) {
				t.Fatalf("accepted invalid selection: %v", err)
			}
			if store.writes != 0 || g.paid != 0 {
				t.Fatal("invalid update had side effects")
			}
		})
	}
}

func TestSourceOwnershipSessionAndAssetAreEnforced(t *testing.T) {
	for _, name := range []string{"owner", "session", "asset", "asset-task", "asset-only", "parameter"} {
		t.Run(name, func(t *testing.T) {
			s, store, g := fixture(t)
			candidate := choose(t, s, "remesh")
			candidate.Sources = modelSource()
			switch name {
			case "owner":
				g.task.OwnerID = "other"
			case "session":
				g.task.SessionID = "other"
			case "asset":
				candidate.Sources[0].AssetID = "other"
			case "asset-task":
				g.task.Assets[0].TaskID = "other"
			case "asset-only":
				candidate.Sources[0].TaskID = ""
			case "parameter":
				candidate.Sources[0].Parameter = "topology"
			}
			if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); err == nil {
				t.Fatal("invalid source accepted")
			}
			if store.writes != 0 || g.paid != 0 {
				t.Fatal("invalid source wrote state")
			}
		})
	}
}

func TestSnapshotBindsRealSourceWithoutLeakingPrivateConfiguration(t *testing.T) {
	s, store, g := fixture(t)
	candidate := choose(t, s, "remesh")
	candidate.Sources = modelSource()
	candidate.FixedParameters = map[string]json.RawMessage{"topology": json.RawMessage(`"quad"`)}
	selected, err := s.Update(context.Background(), "owner", "session", 0, candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusReady {
		t.Fatalf("snapshot %+v %v", snapshot, err)
	}
	if string(snapshot.Input["source_task"]) != `"model-task"` || snapshot.Selection.Binding != selected.Binding {
		t.Fatalf("source not bound %+v", snapshot)
	}
	if len(snapshot.SourceFingerprints) != 1 || snapshot.SourceFingerprints[0].SHA256 != "actual-hash" || snapshot.SourceFingerprints[0].Size != 123 {
		t.Fatalf("fingerprint %+v", snapshot.SourceFingerprints)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(raw, &public); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tool", "offering", "operation"} {
		if _, ok := public[key]; ok {
			t.Fatal("private configuration serialized", key)
		}
	}
	if len(snapshot.ToolArguments) == 0 || snapshot.Capability != generation.Remesh {
		t.Fatal("frozen execution context missing")
	}
	if store.writes != 1 || g.paid != 0 {
		t.Fatal("snapshot invoked execution")
	}
}

func TestIncompatibleSourceCanProposeOnlyEnabledAvailablePrerequisites(t *testing.T) {
	s, _, g := fixture(t)
	candidate := choose(t, s, "animation")
	candidate.Sources = modelSource()
	candidate.AllowPrerequisites = true
	candidate.EnabledOperations = []string{"animation", "rigging"}
	candidate.FillableParameters = []string{"action_id"}
	if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusNeedsInput || !snapshot.CanPropose {
		t.Fatalf("proposal %+v %v", snapshot, err)
	}
	g.unavailable["meshy/processing/rigging"] = true
	snapshot, err = s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.CanPropose {
		t.Fatalf("unavailable prerequisite proposed %+v %v", snapshot, err)
	}
	g.unavailable["meshy/processing/rigging"] = false
	candidate.EnabledOperations = []string{"animation"}
	if _, err := s.Update(context.Background(), "owner", "session", 1, candidate); err != nil {
		t.Fatal(err)
	}
	snapshot, err = s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.CanPropose {
		t.Fatalf("disabled prerequisite proposed %+v %v", snapshot, err)
	}
}

func TestPrerequisitePathsRejectNativeRigAtBothProviderBoundaries(t *testing.T) {
	for _, nativeSource := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-source", true: "projected-goal"}[nativeSource], func(t *testing.T) {
			s, store, g := fixture(t)
			choice := choose(t, s, "animation")
			if choice.OfferingID != "meshy/processing/animation" {
				t.Fatal("fixture goal changed")
			}
			choice.Sources = modelSource()
			choice.AllowPrerequisites = true
			choice.EnabledOperations = []string{"animation", "rigging"}
			choice.FillableParameters = []string{"action_id"}
			rig, _ := s.Catalog.Get("tripo/processing/rigging")
			if !rig.Enabled || !g.OfferingAvailable(rig.ID) {
				t.Fatal("native Rig must remain enabled and available")
			}
			if nativeSource {
				g.task.Offering.ID = "tripo/tripo-v3.1-full/text-to-3d"
				g.task.Offering.ProviderID = "tripo"
			}
			_, bindErr := generation.BindModelSource(rig, g.task, "model-asset", nil)
			if (bindErr == nil) != nativeSource {
				t.Fatalf("first binding error=%v native=%v", bindErr, nativeSource)
			}
			goal, _ := s.Catalog.Get(generation.OfferingID(choice.OfferingID))
			if _, err := generation.BindProjectedModelSource(goal, rig); err == nil {
				t.Fatal("native Rig unexpectedly compatible with Meshy goal")
			}
			g.unavailable["meshy/processing/rigging"] = true
			if _, err := s.Update(context.Background(), "owner", "session", 0, choice); err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.Snapshot(context.Background(), "owner", "session")
			if err != nil || snapshot.CanPropose || len(snapshot.PrerequisitePaths) != 0 {
				t.Fatalf("incompatible native prerequisite advertised: %+v %v", snapshot, err)
			}
			if g.paid != 0 || store.writes != 1 {
				t.Fatal("path prediction created execution or changed selection")
			}
		})
	}
}

func TestRuntimeUnavailableRetainsExactSelectionAndDatabaseErrorsFailClosed(t *testing.T) {
	s, store, g := fixture(t)
	candidate := choose(t, s, "remesh")
	candidate.Sources = modelSource()
	g.unavailable[generation.OfferingID(candidate.OfferingID)] = true
	selected, err := s.Update(context.Background(), "owner", "session", 0, candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusUnavailable || snapshot.Selection.Binding != selected.Binding || snapshot.Selection.OfferingID != candidate.OfferingID {
		t.Fatalf("unavailable selection %+v %v", snapshot, err)
	}
	dbFailure := errors.New("synthetic database failure")
	store.err = dbFailure
	if _, err := s.Snapshot(context.Background(), "owner", "session"); !errors.Is(err, dbFailure) {
		t.Fatal("storage error swallowed", err)
	}
	store.err = nil
	g.err = dbFailure
	if _, err := s.Snapshot(context.Background(), "owner", "session"); !errors.Is(err, dbFailure) {
		t.Fatal("task lookup error swallowed", err)
	}
}

func TestRequiredInputAndFillableBoundaries(t *testing.T) {
	s, store, g := fixture(t)
	candidate := choose(t, s, "animation")
	candidate.Sources = modelSource()
	g.task.Capability = generation.Rigging
	g.task.Offering.ID = "meshy/processing/rigging"
	if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusNeedsInput || len(snapshot.MissingParameters) != 1 || snapshot.MissingParameters[0] != "action_id" {
		t.Fatalf("missing action %+v %v", snapshot, err)
	}
	// #187 D: only a parameter is missing, so the tool stays callable and
	// answers which input is missing instead of disappearing.
	if !snapshot.CallableWithMissing {
		t.Fatalf("parameter gap hid the tool %+v", snapshot)
	}
	candidate.FillableParameters = []string{"action_id"}
	if _, err := s.Update(context.Background(), "owner", "session", 1, candidate); err != nil {
		t.Fatal(err)
	}
	snapshot, err = s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusReady || len(snapshot.MissingParameters) != 1 {
		t.Fatalf("fillable required input %+v %v", snapshot, err)
	}
	candidate.Sources = nil
	if _, err := s.Update(context.Background(), "owner", "session", 2, candidate); err != nil {
		t.Fatal(err)
	}
	snapshot, err = s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusNeedsInput || snapshot.CallableWithMissing {
		t.Fatalf("fillable bypassed missing source %+v %v", snapshot, err)
	}
	if store.writes != 3 || g.paid != 0 {
		t.Fatal("readiness created work")
	}
}

func TestSourceReadinessAndFailureClassification(t *testing.T) {
	for _, name := range []string{"running", "missing", "no-model", "missing-asset", "missing-hash", "database"} {
		t.Run(name, func(t *testing.T) {
			s, _, g := fixture(t)
			candidate := choose(t, s, "remesh")
			candidate.Sources = modelSource()
			if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("database unavailable")
			switch name {
			case "running":
				g.task.State = generation.TaskRunning
			case "missing":
				g.err = &generation.Error{Code: generation.ErrNotFound}
			case "no-model":
				g.task.Assets[0].StorageKey = ""
			case "missing-asset":
				g.task.Assets = nil
			case "missing-hash":
				g.task.Assets[0].SHA256 = ""
			case "database":
				g.err = failure
			}
			snapshot, err := s.Snapshot(context.Background(), "owner", "session")
			if name == "database" {
				if !errors.Is(err, failure) {
					t.Fatal("database failure hidden", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "missing-hash" {
				if snapshot.Status != StatusReady || len(snapshot.SourceFingerprints) != 1 || snapshot.SourceFingerprints[0].SHA256 != "" {
					t.Fatalf("hash was fabricated %+v", snapshot)
				}
				return
			}
			if snapshot.Status != StatusNeedsInput {
				t.Fatalf("unusable source ready %+v", snapshot)
			}
		})
	}
}

func TestFrozenSnapshotSerializationKeepsArgumentsAndSourceIdentity(t *testing.T) {
	s, _, g := fixture(t)
	candidate := choose(t, s, "animation")
	candidate.Sources = modelSource()
	candidate.AllowPrerequisites = true
	candidate.EnabledOperations = []string{"animation", "rigging"}
	candidate.FillableParameters = []string{"action_id"}
	if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); err != nil {
		t.Fatal(err)
	}
	frozen, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.SourceFingerprints) != 1 || !frozen.CanPropose {
		t.Fatalf("incompatible goal lost original source %+v", frozen)
	}
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var recovered Snapshot
	if err := json.Unmarshal(raw, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Capability != generation.Animation || recovered.OfferingRevision != candidate.OfferingRevision || string(recovered.ToolArguments["step"]) != `"animation"` || recovered.SourceFingerprints[0].SHA256 != "actual-hash" {
		t.Fatalf("frozen identities lost %+v", recovered)
	}
	recovered.ToolArguments["step"] = json.RawMessage(`"remesh"`)
	g.task.Capability = generation.Rigging
	g.task.Offering.ID = "meshy/processing/rigging"
	current, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	if string(current.ToolArguments["step"]) != `"animation"` || string(frozen.ToolArguments["step"]) != `"animation"` {
		t.Fatal("snapshot mutated registry or another checkpoint")
	}
	if frozen.Status != StatusNeedsInput || current.Status != StatusReady {
		t.Fatal("new read rewrote original frozen status")
	}
}

func TestConsentAndReferenceAssetsCannotBeSelectedAsPlainParameters(t *testing.T) {
	s, store, g := fixture(t)
	conversion, ok := s.Catalog.Get("tripo/tripo-v3.1/format-conversion")
	if !ok {
		t.Fatal("fixture conversion missing")
	}
	selection := harness.DefaultSelection("owner", "session")
	selection.OperationID = "format-conversion"
	selection.EnabledOperations = []string{"format-conversion"}
	selection.OfferingID = string(conversion.ID)
	selection.OfferingRevision = conversion.Revision
	selection.FixedParameters = map[string]json.RawMessage{"authorize_extra_credits": json.RawMessage(`true`)}
	if _, err := s.Update(context.Background(), "owner", "session", 0, selection); !errors.Is(err, harness.ErrInvalid) {
		t.Fatal("consent edited as parameter", err)
	}
	selection = choose(t, s, "retexture")
	selection.FillableParameters = []string{"style_asset"}
	if _, err := s.Update(context.Background(), "owner", "session", 0, selection); !errors.Is(err, harness.ErrInvalid) {
		t.Fatal("reference asset accepted", err)
	}
	if store.writes != 0 || g.paid != 0 {
		t.Fatal("unsupported parameter caused side effects")
	}
}

func TestChangedOfferingRevisionIsUnavailableWithoutSubstitution(t *testing.T) {
	s, store, g := fixture(t)
	candidate := choose(t, s, "remesh")
	candidate.Sources = modelSource()
	if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); err != nil {
		t.Fatal(err)
	}
	// This is a historical stored selection after its Offering revision changed.
	historical := store.selection
	historical.OfferingRevision = "older-revision"
	historical, err := harness.NormalizeSelection(historical)
	if err != nil {
		t.Fatal(err)
	}
	store.selection = historical
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusUnavailable || snapshot.Selection.OfferingRevision != "older-revision" || snapshot.Selection.OfferingID != candidate.OfferingID {
		t.Fatalf("stale revision was replaced %+v %v", snapshot, err)
	}
	if store.writes != 1 || g.paid != 0 {
		t.Fatal("revision mismatch created work")
	}
}

func TestPrerequisiteProposalRequiresThePlanTool(t *testing.T) {
	s, _, _ := fixture(t)
	candidate := choose(t, s, "animation")
	candidate.Sources = modelSource()
	candidate.AllowPrerequisites = true
	candidate.EnabledOperations = []string{"animation", "rigging"}
	candidate.FillableParameters = []string{"action_id"}
	if _, err := s.Update(context.Background(), "owner", "session", 0, candidate); err != nil {
		t.Fatal(err)
	}
	tools := []catalog.ChatToolSchema{}
	for _, tool := range s.Tools {
		if tool.Name != s.Schema.PrerequisiteTool {
			tools = append(tools, tool)
		}
	}
	s.Tools = tools
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.CanPropose {
		t.Fatalf("proposal advertised without tool %+v %v", snapshot, err)
	}
}

func TestQualifiedHarnessWithoutStaticReasonStillFailsClosedWhenRuntimeUnavailable(t *testing.T) {
	for _, operation := range []string{"uv-unwrap", "resize", "analyze-printability", "multi-color-print"} {
		t.Run(operation, func(t *testing.T) {
			s, _, gateway := fixture(t)
			candidate := choose(t, s, operation)
			gateway.unavailable[generation.OfferingID(candidate.OfferingID)] = true
			selected, err := s.Update(context.Background(), "owner", "session", 0, candidate)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.Snapshot(context.Background(), "owner", "session")
			if err != nil || snapshot.Status != StatusUnavailable || snapshot.Reason != s.Schema.UnavailableMessage || snapshot.Selection.Binding != selected.Binding || snapshot.Selection.OfferingID != candidate.OfferingID || gateway.paid != 0 {
				t.Fatalf("unavailable wiring bypassed: %+v error=%v paid=%d", snapshot, err, gateway.paid)
			}
		})
	}
}

package generation_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

func fidelityFixtureGLB(level string) []byte {
	document := `{"asset":{"version":"2.0"},"nodes":[{}],"skins":[{"joints":[0]}]`
	if level == "animation" {
		document += `,"buffers":[{"byteLength":32}],"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":8},{"buffer":0,"byteOffset":8,"byteLength":24}],"accessors":[{"bufferView":0,"componentType":5126,"type":"SCALAR","count":2},{"bufferView":1,"componentType":5126,"type":"VEC3","count":2}],"animations":[{"channels":[{"sampler":0,"target":{"node":0,"path":"translation"}}],"samplers":[{"input":0,"output":1}]}]`
	}
	document += `}`
	data := []byte(document)
	for len(data)%4 != 0 {
		data = append(data, ' ')
	}
	body := make([]byte, 20+len(data))
	copy(body, "glTF")
	binary.LittleEndian.PutUint32(body[4:], 2)
	binary.LittleEndian.PutUint32(body[12:], uint32(len(data)))
	copy(body[16:], "JSON")
	copy(body[20:], data)
	if level == "animation" {
		bin := make([]byte, 40)
		binary.LittleEndian.PutUint32(bin, 32)
		copy(bin[4:], "BIN\x00")
		body = append(body, bin...)
	}
	binary.LittleEndian.PutUint32(body[8:], uint32(len(body)))
	return body
}

type fidelityCatalog struct {
	generation.Catalog
	level string
}

func (c fidelityCatalog) configure(o generation.Offering) generation.Offering {
	o.Outputs = append([]generation.AssetRequirement(nil), o.Outputs...)
	for i := range o.Outputs {
		if o.Outputs[i].Role == "model" && o.Outputs[i].Variant == "glb" {
			o.Outputs[i].Fidelity = c.level
			o.Outputs[i].MediaTypes = []string{"model/gltf-binary"}
		}
	}
	return o
}
func (c fidelityCatalog) Get(id generation.OfferingID) (generation.Offering, bool) {
	o, ok := c.Catalog.Get(id)
	return c.configure(o), ok
}
func (c fidelityCatalog) Normalize(cap generation.Capability, id generation.OfferingID, input json.RawMessage) (generation.Offering, json.RawMessage, error) {
	o, p, err := c.Catalog.Normalize(cap, id, input)
	return c.configure(o), p, err
}

type fidelityPayloadAdapter struct {
	generation.ProviderPort
	level string
}

func (a fidelityPayloadAdapter) OpenAsset(ctx context.Context, r generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	if r.Claim.Variant == "glb" && a.level != "" {
		data := fidelityFixtureGLB(a.level)
		return io.NopCloser(bytes.NewReader(data)), generation.AssetMetadata{MediaType: "model/gltf-binary", Size: int64(len(data))}, nil
	}
	return a.ProviderPort.OpenAsset(ctx, r)
}

func TestMaterializationEnforcesDeclaredFidelityWithoutRetry(t *testing.T) {
	for _, tc := range []struct{ name, requirement, payload, reason string }{
		{"undeclared static", "", "", ""},
		{"declared static", "skeleton", "", "fidelity_skeleton_missing"},
		{"rigged valid", "skeleton", "skeleton", ""},
		{"rigged not animated", "animation", "skeleton", "fidelity_animation_missing"},
		{"animation static", "animation", "", "fidelity_skeleton_missing"},
		{"animated valid", "animation", "animation", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newConfiguredHarness(t, fidelityPayloadAdapter{ProviderPort: completeWithBrokenOptional{}, level: tc.payload}, nil, func(d *generation.Dependencies) {
				d.Catalog = fidelityCatalog{Catalog: d.Catalog, level: tc.requirement}
			})
			a := submitMaterializationEvidenceTask(t, h)
			task := h.runUntilTerminal(t, "owner-a", a.TaskID)
			if tc.reason == "" {
				if task.State != generation.TaskSucceeded {
					t.Fatalf("state=%s failure=%#v", task.State, task.Failure)
				}
				return
			}
			if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Class != generation.FailureIntegrity || task.Failure.Retryable || task.MaterializeFailures != 1 || len(task.Assets) != 0 {
				t.Fatalf("state=%s failure=%#v failures=%d assets=%d", task.State, task.Failure, task.MaterializeFailures, len(task.Assets))
			}
			if task.Failure.UpstreamCode != "asset_fidelity_rejected" || !strings.Contains(task.Failure.UpstreamExcerpt, "reason="+tc.reason) {
				t.Fatalf("failure=%#v", task.Failure)
			}
			events := materializationEvidenceEvents(t, h.repository, a.TaskID)
			if countEvents(events, "materialization.retry_scheduled") != 0 {
				t.Fatal("fidelity failure retried")
			}
			if _, err := h.module.Get(context.Background(), "other-owner", a.TaskID); err == nil {
				t.Fatal("cross-owner task readable")
			}
		})
	}
}

func TestMockRiggingAndAnimationPassDeclaredFidelity(t *testing.T) {
	h := newHarness(t, mock.New(), nil)
	rigged := h.runUntilTerminal(t, "owner-a", submitRigging(t, h))
	if rigged.State != generation.TaskSucceeded {
		t.Fatalf("rigging failure=%#v", rigged.Failure)
	}
	a, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{OwnerID: "owner-a", Capability: generation.Animation, OfferingID: "meshy/processing/animation", Input: []byte(`{"source_task":"` + string(rigged.ID) + `","action_id":3}`)})
	if err != nil {
		t.Fatal(err)
	}
	animated := h.runUntilTerminal(t, "owner-a", a.TaskID)
	if animated.State != generation.TaskSucceeded {
		t.Fatalf("animation failure=%#v", animated.Failure)
	}
}

// #265: the default Catalog declares skeleton fidelity on both the GLB and the
// binary FBX of Tripo rigging. The mock must pass the same materialization
// check, or every dev-stack Tripo rigging fails with fidelity_skeleton_missing.
func TestMockTripoRiggingPassesDeclaredFidelity(t *testing.T) {
	h := newHarness(t, mock.New(), nil)
	model, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{OwnerID: "owner-a", Capability: generation.TextTo3D, OfferingID: "tripo/tripo-v3.1-full/text-to-3d", Input: []byte(`{"prompt":"a knight","authorize_extra_credits":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	source := h.runUntilTerminal(t, "owner-a", model.TaskID)
	if source.State != generation.TaskSucceeded {
		t.Fatalf("model failure=%#v", source.Failure)
	}
	rig, err := h.submitApproved(t, context.Background(), generation.SubmitCommand{OwnerID: "owner-a", Capability: generation.Rigging, OfferingID: "tripo/processing/rigging", Input: []byte(`{"source_task":"` + string(source.ID) + `","authorize_extra_credits":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	rigged := h.runUntilTerminal(t, "owner-a", rig.TaskID)
	if rigged.State != generation.TaskSucceeded {
		t.Fatalf("tripo rigging failure=%#v", rigged.Failure)
	}
	variants := map[string]bool{}
	for _, asset := range rigged.Assets {
		variants[asset.Variant] = true
	}
	if !variants["glb"] || !variants["fbx"] {
		t.Fatalf("rigging assets = %v", variants)
	}
}

type storedFidelityAdapter struct {
	generation.ProviderPort
	store generation.AssetStore
	level string
}

func (a storedFidelityAdapter) Start(ctx context.Context, r generation.StartRequest) (generation.Observation, error) {
	data := validGLB("static stored")
	if a.level != "" {
		data = fidelityFixtureGLB(a.level)
	}
	asset, err := a.store.Put(ctx, generation.AssetWrite{AssetID: "stored-fidelity", OwnerID: "owner-a", TaskID: r.TaskID, AttemptID: r.AttemptID, Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Body: bytes.NewReader(data)})
	if err != nil {
		return generation.Observation{}, err
	}
	observation, err := completeMeshyFormats(r.Offering.ID, generation.Observation{Phase: generation.UpstreamCompleted, Artifacts: []generation.ArtifactClaim{{ID: "stored-fidelity", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", StoredAsset: &asset}}})
	if err != nil {
		return observation, err
	}
	for i := range observation.Artifacts {
		claim := &observation.Artifacts[i]
		if claim.Variant != "glb" {
			claim.StoredAsset = nil
		}
	}
	return observation, nil
}

type uncheckedFidelityStore struct{ generation.AssetStore }

func TestStoredAssetCannotBypassFidelityAndCheckerMustExist(t *testing.T) {
	for _, tc := range []struct {
		name, payload, reason string
		hideChecker           bool
	}{
		{"static stored", "", "fidelity_skeleton_missing", false},
		{"rigged stored", "skeleton", "", false},
		{"checker missing", "skeleton", "fidelity_unsupported", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := local.New(filepath.Join(t.TempDir(), "assets"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			var exposed generation.AssetStore = store
			if tc.hideChecker {
				exposed = uncheckedFidelityStore{store}
			}
			adapter := storedFidelityAdapter{ProviderPort: completeWithBrokenOptional{}, store: store, level: tc.payload}
			h := newConfiguredHarness(t, adapter, exposed, func(d *generation.Dependencies) { d.Catalog = fidelityCatalog{Catalog: d.Catalog, level: "skeleton"} })
			a := submitMaterializationEvidenceTask(t, h)
			task := h.runUntilTerminal(t, "owner-a", a.TaskID)
			if tc.reason == "" {
				if task.State != generation.TaskSucceeded {
					t.Fatalf("state=%s failure=%#v events=%v", task.State, task.Failure, eventTypes(materializationEvidenceEvents(t, h.repository, a.TaskID)))
				}
				return
			}
			if task.State != generation.TaskFailed || task.Failure == nil || task.Failure.Retryable || task.MaterializeFailures != 1 || !strings.Contains(task.Failure.UpstreamExcerpt, "reason="+tc.reason) {
				t.Fatalf("state=%s reason=%#v", task.State, task.Failure)
			}
		})
	}
}

// flakyFidelityStore fails the first fidelity inspection with an operational
// error (for example descriptor exhaustion), then delegates to the real store.
type flakyFidelityStore struct {
	*local.Store
	failures *int
}

func (s flakyFidelityStore) CheckFidelity(ctx context.Context, asset generation.Asset, level string) error {
	if *s.failures > 0 {
		*s.failures--
		return errors.New("too many open files")
	}
	return s.Store.CheckFidelity(ctx, asset, level)
}

// Review P1: an inspection that could not read a valid stored file is not
// evidence against the paid upstream result. It must keep the retryable
// materialization path instead of failing the Task permanently.
func TestFidelityInspectionUnavailableRetriesMaterialization(t *testing.T) {
	store, err := local.New(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	failures := 1
	h := newConfiguredHarness(t, fidelityPayloadAdapter{ProviderPort: completeWithBrokenOptional{}, level: "skeleton"}, flakyFidelityStore{Store: store, failures: &failures}, func(d *generation.Dependencies) {
		d.Catalog = fidelityCatalog{Catalog: d.Catalog, level: "skeleton"}
	})
	a := submitMaterializationEvidenceTask(t, h)
	task := h.runUntilTerminal(t, "owner-a", a.TaskID)
	if task.State != generation.TaskSucceeded {
		t.Fatalf("state=%s failure=%#v", task.State, task.Failure)
	}
	events := materializationEvidenceEvents(t, h.repository, a.TaskID)
	if failures != 0 || countEvents(events, "materialization.retry_scheduled") != 1 {
		t.Fatalf("failures left=%d retries=%d", failures, countEvents(events, "materialization.retry_scheduled"))
	}
}

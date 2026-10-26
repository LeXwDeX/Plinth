package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func assetTaskParameter(t *testing.T) Parameter {
	t.Helper()
	var p Parameter
	if err := json.Unmarshal([]byte(`{"name":"source_task","type":"task","semantic":"source-task","task_source_kind":"assets","task_offerings":["local/forward"],"task_capabilities":["format-conversion"],"media_types":["model/gltf+json"],"extensions":[".gltf"],"max_bytes":1024}`), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func assetTaskSource() Task {
	digest := sha256.Sum256([]byte("saved model"))
	return Task{ID: "source", OwnerID: "owner", State: TaskSucceeded, Capability: FormatConversion,
		Offering: Offering{ID: "local/forward", ProviderID: "stopped-provider", ModelID: "forward"},
		Assets:   []Asset{{ID: "selected", TaskID: "source", Role: "model", Variant: "gltf", StorageKey: "saved/model.gltf", MediaType: "model/gltf+json", Size: 11, SHA256: hex.EncodeToString(digest[:])}}}
}

func TestTaskSourceCannotBindToNativeOnlyUploadParameter(t *testing.T) {
	var p Parameter
	if err := json.Unmarshal([]byte(`{"name":"source_asset","type":"asset","semantic":"source-asset","uploaded_model":{"profile_id":"static-glb-v1","native_only":true}}`), &p); err != nil {
		t.Fatal(err)
	}
	if binding, err := BindModelSource(Offering{Parameters: []Parameter{p}}, assetTaskSource(), "", nil); !IsCode(err, ErrInvalidInput) {
		t.Fatalf("Task output bound to native-upload-only route: %+v %v", binding, err)
	}
}

func TestAssetsTaskBindingRejectsAmbiguousOrIncompatibleSources(t *testing.T) {
	for _, name := range []string{"valid cross provider", "valid unrelated asset", "wrong offering", "wrong capability", "not succeeded", "wrong task asset", "missing key", "missing hash", "bad hash", "zero bytes", "too large", "wrong media", "wrong extension", "ambiguous", "chosen wrong", "chosen ambiguous", "same provider required"} {
		t.Run(name, func(t *testing.T) {
			p, source, chosen := assetTaskParameter(t), assetTaskSource(), AssetID("")
			switch name {
			case "valid unrelated asset":
				source.Assets = append([]Asset{{ID: "unrelated", Role: "thumbnail"}}, source.Assets...)
			case "wrong offering":
				source.Offering.ID = "other/forward"
			case "wrong capability":
				source.Capability = TextTo3D
			case "not succeeded":
				source.State = TaskRunning
			case "wrong task asset":
				source.Assets[0].TaskID = "other"
			case "missing key":
				source.Assets[0].StorageKey = ""
			case "missing hash":
				source.Assets[0].SHA256 = ""
			case "bad hash":
				source.Assets[0].SHA256 = strings.Repeat("z", 64)
			case "zero bytes":
				source.Assets[0].Size = 0
			case "too large":
				source.Assets[0].Size = 1025
			case "wrong media":
				source.Assets[0].MediaType = "model/gltf-binary"
			case "wrong extension":
				source.Assets[0].Variant = "glb"
			case "ambiguous", "chosen ambiguous":
				other := source.Assets[0]
				other.ID = "second"
				source.Assets = append(source.Assets, other)
				if name == "chosen ambiguous" {
					chosen = "selected"
				}
			case "chosen wrong":
				chosen = "other"
			case "same provider required":
				p.SameProvider = true
			}
			binding, err := BindModelSource(Offering{ProviderID: "local", Parameters: []Parameter{p}}, source, chosen, []string{"glb"})
			valid := strings.HasPrefix(name, "valid")
			if valid {
				if err != nil || binding.Value != "source" || binding.Parameter != "source_task" {
					t.Fatalf("binding=%+v err=%v", binding, err)
				}
			} else if !IsCode(err, ErrInvalidInput) {
				t.Fatalf("invalid source bound: %+v %v", binding, err)
			}
		})
	}
}

type taskAssetReadStore struct {
	AssetStore
	body          string
	err           error
	closeErr      error
	opens, closes int
	selected      AssetID
}
type taskAssetReadBody struct {
	io.Reader
	store *taskAssetReadStore
}

func (r taskAssetReadBody) Close() error { r.store.closes++; return r.store.closeErr }
func (s *taskAssetReadStore) Open(_ context.Context, a Asset) (io.ReadCloser, error) {
	s.opens++
	s.selected = a.ID
	if s.err != nil {
		return nil, s.err
	}
	return taskAssetReadBody{strings.NewReader(s.body), s}, nil
}

func TestAssetsTaskDependenciesVerifyOwnedBytesWithoutAnUpstreamJob(t *testing.T) {
	for _, name := range []string{"valid", "other owner", "missing source", "no attempt", "failed attempt", "legacy empty attempt", "wrong offering", "ambiguous", "nil store", "open failure", "close failure", "changed bytes", "long bytes"} {
		t.Run(name, func(t *testing.T) {
			repo := dependencySourceRepository{source: Aggregate{Task: assetTaskSource(), CurrentAttempt: &Attempt{State: AttemptUpstreamSucceeded}}}
			registry := &dependencySourceRegistry{missing: true}
			store := &taskAssetReadStore{body: "saved model"}
			m := &Module{repository: repo, providers: registry, assets: store}
			switch name {
			case "other owner":
				repo.source.Task.OwnerID = "other"
			case "missing source":
				repo.err = RepositoryNotFound
			case "no attempt":
				repo.source.CurrentAttempt = nil
			case "failed attempt":
				repo.source.CurrentAttempt.State = AttemptFailed
			case "legacy empty attempt":
				repo.source.CurrentAttempt.State = ""
			case "wrong offering":
				repo.source.Task.Offering.ID = "other"
			case "ambiguous":
				other := repo.source.Task.Assets[0]
				other.ID = "second"
				repo.source.Task.Assets = append(repo.source.Task.Assets, other)
			case "nil store":
				m.assets = nil
			case "open failure":
				store.err = errors.New("private storage key")
			case "close failure":
				store.closeErr = errors.New("private close failure")
			case "changed bytes":
				store.body = "changed mdl"
			case "long bytes":
				store.body += " extra"
			}
			m.repository = repo
			deps, err := m.resolveTaskDependencies(context.Background(), "owner", Offering{ProviderID: "local", Parameters: []Parameter{assetTaskParameter(t)}}, json.RawMessage(`{"source_task":"source"}`), "")
			if name == "valid" {
				dep := deps["source_task"]
				if err != nil || dep.TaskID != "source" || dep.OfferingID != "local/forward" || dep.UpstreamJob != (UpstreamJob{}) || len(dep.Assets) != 1 || dep.Assets[0].ID != "selected" || store.opens != 1 || store.closes != 1 {
					t.Fatalf("deps=%+v store=%+v err=%v", deps, store, err)
				}
			} else if err == nil {
				t.Fatalf("invalid dependency accepted: %+v", deps)
			}
			if registry.offering.ID != "" {
				t.Fatal("assets dependency consulted stopped source provider")
			}
			if store.opens > 0 && store.err == nil && store.closes != 1 {
				t.Fatalf("reader not closed: %+v", store)
			}
		})
	}
}

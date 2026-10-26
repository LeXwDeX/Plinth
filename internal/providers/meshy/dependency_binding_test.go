package meshy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func bindingPrototypeFixture() (generation.Offering, generation.Asset) {
	minimum := 1
	o := generation.Offering{ID: "meshy/keycap/prototype", Revision: "binding-v1", ProviderID: "meshy", ProviderOptions: json.RawMessage(`{"endpoint":"keycap/v1/prototype","api_version":"creative-lab","task_type":"creative-lab-keycap-prototype","result_identity":{"value_field":"candidate_ids","paired_field":"image_urls","role":"image","variant":"1"},"result_assets":[{"path":"image_urls","mode":"array","role":"image"}]}`), Outputs: []schema.AssetRequirement{{Role: "image", Variant: "1", Minimum: &minimum, Delivery: "required", RetainDependencyIdentity: true}}}
	asset := generation.Asset{ID: "preview", TaskID: "public-task", AttemptID: "source-attempt", Role: "image", Variant: "1", SHA256: strings.Repeat("a", 64), Size: 100}
	return o, asset
}

func validBindingEvidence(t *testing.T) (*Adapter, generation.DependencyBindingRequest) {
	t.Helper()
	o, asset := bindingPrototypeFixture()
	a := &Adapter{statusMapping: &generation.StatusMapping{UpstreamStatus: map[string]schema.StatusRule{"succeeded": {Phase: "completed"}}}}
	var response taskResponse
	_ = json.Unmarshal([]byte(`{"id":"prototype-1","type":"creative-lab-keycap-prototype","status":"SUCCEEDED","candidate_ids":["candidate-A"],"image_urls":["https://example.invalid/preview.png"]}`), &response)
	observation, err := a.observation(generation.ObserveRequest{TaskID: "public-task", AttemptID: "source-attempt", Offering: o, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-1"}}, response)
	if err != nil {
		t.Fatal(err)
	}
	observation.Artifacts[0].StoredAsset = &asset
	return a, generation.DependencyBindingRequest{TaskID: "public-task", AttemptID: "source-attempt", Offering: o, Job: generation.UpstreamJob{ProviderID: "meshy", RemoteID: "prototype-1"}, Claims: observation.Artifacts, Assets: []generation.Asset{asset}}
}

func TestFrozenSourceBindingRejectsMissingConflictingOrRematerializedEvidence(t *testing.T) {
	for _, name := range []string{"valid", "missing-envelope", "duplicate-envelope", "wrong-claim", "wrong-task", "new-attempt", "wrong-revision", "wrong-job", "wrong-slot", "foreign-asset", "old-asset-attempt", "duplicate-asset", "no-asset", "malformed-sha", "empty-size", "extra-envelope-key", "missing-stored", "wrong-stored-id", "wrong-stored-hash", "wrong-stored-attempt", "wrong-stored-size", "wrong-stored-key"} {
		t.Run(name, func(t *testing.T) {
			a, r := validBindingEvidence(t)
			switch name {
			case "missing-envelope":
				r.Claims[0].Metadata = nil
			case "duplicate-envelope":
				r.Claims = append(r.Claims, r.Claims[0])
			case "wrong-claim":
				r.Claims[0].ID = "different"
			case "wrong-task":
				r.TaskID = "other"
			case "new-attempt":
				r.AttemptID = "next"
			case "wrong-revision":
				r.Offering.Revision = "new"
			case "wrong-job":
				r.Job.RemoteID = "other"
			case "wrong-slot":
				r.Claims[0].Variant = "2"
			case "foreign-asset":
				r.Assets[0].TaskID = "other"
			case "old-asset-attempt":
				r.Assets[0].AttemptID = "old"
			case "duplicate-asset":
				r.Assets = append(r.Assets, r.Assets[0])
			case "no-asset":
				r.Assets = nil
			case "malformed-sha":
				r.Assets[0].SHA256 = "bad"
			case "empty-size":
				r.Assets[0].Size = 0
			case "missing-stored":
				r.Claims[0].StoredAsset = nil
			case "wrong-stored-id":
				copy := *r.Claims[0].StoredAsset
				copy.ID = "other"
				r.Claims[0].StoredAsset = &copy
			case "wrong-stored-hash":
				copy := *r.Claims[0].StoredAsset
				copy.SHA256 = strings.Repeat("b", 64)
				r.Claims[0].StoredAsset = &copy
			case "wrong-stored-attempt":
				copy := *r.Claims[0].StoredAsset
				copy.AttemptID = "other"
				r.Claims[0].StoredAsset = &copy
			case "wrong-stored-size":
				copy := *r.Claims[0].StoredAsset
				copy.Size++
				r.Claims[0].StoredAsset = &copy
			case "wrong-stored-key":
				copy := *r.Claims[0].StoredAsset
				copy.StorageKey = "other"
				r.Claims[0].StoredAsset = &copy
			case "extra-envelope-key":
				r.Claims[0].Metadata = append(r.Claims[0].Metadata[:len(r.Claims[0].Metadata)-1], []byte(`,"extra":true}`)...)
			}
			binding, err := a.ResolveDependencyBinding(r)
			if (err == nil) != (name == "valid") {
				t.Fatalf("binding=%s error=%v", binding, err)
			}
		})
	}
}

func TestFrozenSourceCandidateNeverSelectsAgainAndRejectsChangedPreview(t *testing.T) {
	a, r := validBindingEvidence(t)
	binding, err := a.ResolveDependencyBinding(r)
	if err != nil {
		t.Fatal(err)
	}
	dependency := generation.TaskDependency{TaskID: r.TaskID, OfferingID: r.Offering.ID, ProviderID: "meshy", UpstreamJob: r.Job, ProviderBinding: binding, Assets: r.Assets}
	for _, name := range []string{"restored", "missing", "extra-key", "changed-bytes", "rematerialized"} {
		t.Run(name, func(t *testing.T) {
			d := dependency
			d.Assets = append([]generation.Asset(nil), dependency.Assets...)
			switch name {
			case "missing":
				d.ProviderBinding = nil
			case "extra-key":
				d.ProviderBinding = append(append([]byte(nil), binding[:len(binding)-1]...), []byte(`,"extra":true}`)...)
			case "changed-bytes":
				d.Assets[0].SHA256 = strings.Repeat("b", 64)
			case "rematerialized":
				d.Assets[0].ID = "new-preview"
			}
			body := map[string]any{"source": "public-task"}
			err := a.projectInput(context.Background(), generation.StartRequest{Offering: sourceResultOffering(), TaskDependencies: map[string]generation.TaskDependency{"source": d}}, body)
			if (err == nil) != (name == "restored") {
				t.Fatalf("error=%v", err)
			}
			if err == nil && body["candidate_id"] != "candidate-A" {
				t.Fatal("candidate drift")
			}
		})
	}
}

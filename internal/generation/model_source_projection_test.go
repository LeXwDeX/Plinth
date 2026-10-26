package generation

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func projectedSourcePair() (Offering, Offering) {
	one := 1
	return Offering{ID: "target/reverse", ProviderID: "target", Parameters: []Parameter{{Name: "source_task", Type: "task", Semantic: schema.SemanticSourceTask, TaskSourceKind: schema.TaskSourceAssets, TaskOfferings: []OfferingID{"source/forward"}, TaskCapabilities: []Capability{FormatConversion}, MediaTypes: []string{"model/gltf+json"}, Extensions: []string{".gltf"}, MaxBytes: 1024}}}, Offering{ID: "source/forward", ProviderID: "source", Capability: FormatConversion, Outputs: []AssetRequirement{{Role: "model", Variant: "gltf", MediaTypes: []string{"model/gltf+json"}, MaxBytes: 1024, Minimum: &one}}}
}

func TestProjectedModelSourceAssetsContractRejectsUnprovedOutputs(t *testing.T) {
	for _, name := range []string{"valid cross provider", "valid same provider", "valid default minimum", "wrong offering", "wrong capability", "same provider required", "missing source id", "missing output", "multiple models", "optional output", "multiple minimum", "on demand", "dynamic variant", "missing variant", "wrong extension", "wrong MIME", "mixed MIME", "missing MIME", "unbounded output", "unbounded input", "oversize output", "missing extension"} {
		t.Run(name, func(t *testing.T) {
			target, source := projectedSourcePair()
			switch name {
			case "valid same provider":
				target.ProviderID = source.ProviderID
			case "valid default minimum":
				source.Outputs[0].Minimum = nil
			case "wrong offering":
				source.ID = "other"
			case "wrong capability":
				source.Capability = TextTo3D
			case "same provider required":
				target.Parameters[0].SameProvider = true
			case "missing source id":
				source.ID = ""
			case "missing output":
				source.Outputs = nil
			case "multiple models":
				source.Outputs = append(source.Outputs, source.Outputs[0])
			case "optional output":
				zero := 0
				source.Outputs[0].Minimum = &zero
			case "multiple minimum":
				two := 2
				source.Outputs[0].Minimum = &two
			case "on demand":
				source.Outputs[0].Delivery = schema.AssetDeliveryOnDemand
			case "dynamic variant":
				source.Outputs[0].VariantFrom = "formats"
			case "missing variant":
				source.Outputs[0].Variant = ""
			case "wrong extension":
				source.Outputs[0].Variant = "glb"
			case "wrong MIME":
				source.Outputs[0].MediaTypes = []string{"model/gltf-binary"}
			case "mixed MIME":
				source.Outputs[0].MediaTypes = append(source.Outputs[0].MediaTypes, "application/octet-stream")
			case "missing MIME":
				source.Outputs[0].MediaTypes = nil
			case "unbounded output":
				source.Outputs[0].MaxBytes = 0
			case "unbounded input":
				target.Parameters[0].MaxBytes = 0
			case "oversize output":
				source.Outputs[0].MaxBytes = 1025
			case "missing extension":
				target.Parameters[0].Extensions = nil
			}
			before, _ := json.Marshal([]Offering{target, source})
			got, err := BindProjectedModelSource(target, source)
			valid := name == "valid cross provider" || name == "valid same provider" || name == "valid default minimum"
			if valid {
				if err != nil || got.Parameter != "source_task" || got.Role != schema.SemanticSourceTask {
					t.Fatalf("contract=%+v err=%v", got, err)
				}
			} else if !IsCode(err, ErrInvalidInput) {
				t.Fatalf("unproved output accepted: %+v %v", got, err)
			}
			after, _ := json.Marshal([]Offering{target, source})
			if string(before) != string(after) {
				t.Fatal("declaration mutated")
			}
		})
	}
}

func TestProjectedModelSourceKeepsLegacyAndNativeOnlyBoundaries(t *testing.T) {
	target, source := projectedSourcePair()
	target.Parameters[0].TaskSourceKind = ""
	target.ProviderID = source.ProviderID
	source.Outputs = nil // Legacy job dependencies carry the job, not a predicted file.
	got, err := BindProjectedModelSource(target, source)
	if err != nil || got.Role != schema.SemanticSourceTask {
		t.Fatalf("legacy same-provider rejected: %+v %v", got, err)
	}
	target.ProviderID = "other"
	if _, err := BindProjectedModelSource(target, source); !IsCode(err, ErrInvalidInput) {
		t.Fatal("legacy cross-provider accepted")
	}
	target, source = projectedSourcePair()
	target.Parameters[0].Name = "source_asset"
	target.Parameters[0].Type = "asset"
	target.Parameters[0].Semantic = schema.SemanticSourceAsset
	target.Parameters[0].UploadedModel = &schema.UploadedModel{ProfileID: "static-glb-v1"}
	got, err = BindProjectedModelSource(target, source)
	if err != nil || got.Role != schema.SemanticSourceAsset {
		t.Fatalf("declared file preview rejected: %+v %v", got, err)
	}
	target.Parameters[0].UploadedModel.NativeOnly = true
	if _, err := BindProjectedModelSource(target, source); !IsCode(err, ErrInvalidInput) {
		t.Fatal("Task output predicted as native upload")
	}
}

func TestProjectedSourceAssetRetainsSelectableLegacyOutputs(t *testing.T) {
	target := Offering{ProviderID: "target", Parameters: []Parameter{{Name: "input_model", Type: "asset", Semantic: schema.SemanticSourceAsset, MediaTypes: []string{"model/gltf-binary"}, Extensions: []string{"glb"}, MaxBytes: 1}}}
	// Legacy asset-bearing routes choose a real output later. Their prediction
	// must not require a unique variant or invent an output-size guarantee.
	source := Offering{ID: "legacy/source", ProviderID: "other", Capability: TextTo3D, Outputs: []AssetRequirement{{Role: "model", Variant: "obj", MediaTypes: []string{"application/octet-stream"}}, {Role: "model", Variant: "glb", MediaTypes: []string{"model/gltf-binary"}}}}
	got, err := BindProjectedModelSource(target, source)
	if err != nil || got.Parameter != "input_model" || got.Role != schema.SemanticSourceAsset {
		t.Fatalf("legacy selectable file rejected: %+v %v", got, err)
	}
	target.Parameters[0].MediaTypes = []string{"model/stl"}
	if _, err := BindProjectedModelSource(target, source); !IsCode(err, ErrInvalidInput) {
		t.Fatal("disjoint legacy output MIME accepted")
	}
}

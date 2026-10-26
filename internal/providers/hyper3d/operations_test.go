package hyper3d_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
)

func processingOffering(endpoint string, capability generation.Capability) generation.Offering {
	parameters := []generation.Parameter{
		{Name: "model_file", Type: "asset", WireName: "model", MaxBytes: 1 << 20, MediaTypes: []string{"model/gltf-binary", "model/obj"}},
		{Name: "reference", Type: "asset", WireName: "image", MaxBytes: 1 << 20, MediaTypes: []string{"image/png"}},
	}
	if endpoint == "bang" {
		parameters = []generation.Parameter{
			{Name: "source_task", Type: "task", WireName: "asset_id", SameProvider: true, TaskCapabilities: []generation.Capability{generation.TextTo3D, generation.ImageTo3D}},
			{Name: "model_file", Type: "asset", WireName: "model", MaxBytes: 1 << 20, MediaTypes: []string{"model/gltf-binary", "model/obj"}},
			{Name: "references", Type: "asset", WireName: "image", MaxBytes: 1 << 20, MediaTypes: []string{"image/png"}},
			{Name: "prompt", Type: "text", WireName: "prompt"},
			{Name: "instruction", Type: "text", WireName: "instruction"},
		}
	} else {
		for i := range parameters {
			parameters[i].Required = true
		}
	}
	return generation.Offering{
		ProviderID: "hyper3d", ModelID: "hyper3d-processing", Capability: capability,
		ProviderOptions: json.RawMessage(`{"endpoint":"` + endpoint + `","observe_endpoint":"status","download_endpoint":"download","request_template":{"geometry_file_format":"glb"}}`),
		Parameters:      parameters,
		Outputs:         []generation.AssetRequirement{{Role: "model", MediaTypes: []string{"model/gltf-binary"}}},
	}
}

func TestBangNativeModeUsesOnlyDeliveredRodinUUID(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/bang" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("asset_id"); got != "rodin-top-uuid" {
			t.Errorf("asset_id = %q", got)
		}
		if r.FormValue("prompt") != "" || len(r.MultipartForm.File) != 0 {
			t.Errorf("native Bang mode unexpectedly included fields/files: %#v", r.MultipartForm.Value)
		}
		_, _ = io.WriteString(w, `{"uuid":"bang-task","jobs":{"subscription_key":"bang-subscription"}}`)
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	offering := processingOffering("bang", generation.Capability("mesh-segmentation"))
	req := generation.StartRequest{
		TaskID: "bang", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input: json.RawMessage(`{"source_task":"source"}`),
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {
			TaskID: "source", Capability: generation.TextTo3D, OfferingID: "rodin", ModelID: "rodin-2.5-business-alias", ProviderID: "hyper3d",
			UpstreamJob: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "rodin-top-uuid"},
		}},
	}
	got, err := port.Start(context.Background(), req)
	if err != nil || got.Job == nil || got.Job.RemoteID != "bang-task" {
		t.Fatalf("Start = %#v, %v", got, err)
	}
	bad := req
	bad.TaskDependencies = map[string]generation.TaskDependency{"source_task": {ProviderID: "tripo", UpstreamJob: generation.UpstreamJob{ProviderID: "tripo", RemoteID: "foreign-id"}}}
	if _, err := port.Start(context.Background(), bad); err == nil {
		t.Fatal("cross-provider task id was accepted as Hyper3D asset_id")
	}
}

func TestBangUploadModeSendsOwnedModelAndSingularReferenceImage(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/bang" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		assertFile(t, r.MultipartForm, "model", "model.glb", "image-model-7")
		assertFile(t, r.MultipartForm, "image", "guide.png", "image-guide-9")
		if len(r.MultipartForm.File["image"]) != 1 || len(r.MultipartForm.File["images"]) != 0 {
			t.Fatalf("Bang image parts = %#v", r.MultipartForm.File)
		}
		if r.FormValue("asset_id") != "" {
			t.Error("upload mode included native asset_id")
		}
		_, _ = io.WriteString(w, `{"uuid":"bang-upload","jobs":{"subscription_key":"bang-subscription"}}`)
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	offering := processingOffering("bang", generation.Capability("mesh-segmentation"))
	got, err := port.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "bang", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input: json.RawMessage(`{"model_file":["asset-model"],"references":["asset-guide"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"model_file": {{ID: "model-7", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}},
			"references": {{ID: "guide-9", OwnerID: "owner", OriginalFilename: "guide.png", MediaType: "image/png"}},
		},
	})
	if err != nil || got.Job == nil {
		t.Fatalf("Start = %#v, %v", got, err)
	}
}

func TestBangModelOnlyOfferingDoesNotRequireAssetID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		assertFile(t, r.MultipartForm, "model", "model.glb", "image-model")
		assertFile(t, r.MultipartForm, "image", "reference.png", "image-reference")
		if r.FormValue("asset_id") != "" || r.FormValue("prompt") != "" {
			t.Fatalf("unexpected model-only parameters: %#v", r.MultipartForm.Value)
		}
		_, _ = io.WriteString(w, `{"uuid":"bang-model-only","jobs":{"subscription_key":"bang-subscription"}}`)
	}))
	t.Cleanup(server.Close)
	offering := processingOffering("bang", generation.Capability("mesh-segmentation"))
	for i := range offering.Parameters {
		if offering.Parameters[i].WireName == "asset_id" {
			offering.Parameters = append(offering.Parameters[:i], offering.Parameters[i+1:]...)
			break
		}
	}
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	got, err := port.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "bang-model-only", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input: json.RawMessage(`{"model_file":["asset-model"],"references":["asset-reference"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"model_file": {{ID: "model", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}},
			"references": {{ID: "reference", OwnerID: "owner", OriginalFilename: "reference.png", MediaType: "image/png"}},
		},
	})
	if err != nil || got.Job == nil {
		t.Fatalf("Start = %#v, %v", got, err)
	}
}

func TestBangUploadRequiresImageUnlessMaterialNoneAndNoInstruction(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("material") != "None" || r.FormValue("instruction") != "" {
			t.Fatalf("material-none request included wrong fields: %#v", r.MultipartForm.Value)
		}
		_, _ = io.WriteString(w, `{"uuid":"bang-no-image","jobs":{"subscription_key":"bang-subscription"}}`)
	}))
	t.Cleanup(server.Close)
	offering := processingOffering("bang", generation.Capability("mesh-segmentation"))
	offering.Parameters = append(offering.Parameters, generation.Parameter{
		Name: "material", Type: "select", WireName: "material", Default: json.RawMessage(`"None"`),
		Options: []generation.ParameterOption{{Value: "PBR"}, {Value: "Shaded"}, {Value: "All"}, {Value: "None"}},
	})
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	request := generation.StartRequest{
		OwnerID: "owner", TaskID: "bang-no-image", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input: json.RawMessage(`{"model_file":["asset-model"],"material":"None"}`),
		InputAssets: map[string][]generation.InputAsset{
			"model_file": {{ID: "model", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}},
		},
	}
	if got, err := port.Start(context.Background(), request); err != nil || got.Job == nil {
		t.Fatalf("None material without instruction should not need image: %#v, %v", got, err)
	}
	instruction := request
	instruction.Input = json.RawMessage(`{"model_file":["asset-model"],"material":"None","instruction":"split the body"}`)
	if _, err := port.Start(context.Background(), instruction); err == nil {
		t.Fatal("non-empty instruction without image was accepted")
	}
	pbr := request
	pbr.Input = json.RawMessage(`{"model_file":["asset-model"],"material":"PBR"}`)
	if _, err := port.Start(context.Background(), pbr); err == nil {
		t.Fatal("PBR material without image was accepted")
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid requests reached upstream: %d", calls.Load())
	}
}

func TestTextureRequiresAndUploadsBothOwnedFiles(t *testing.T) {
	var calls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v2/rodin_texture_only" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		assertFile(t, r.MultipartForm, "model", "model.glb", "image-model-7")
		assertFile(t, r.MultipartForm, "image", "reference.png", "image-reference-11")
		_, _ = io.WriteString(w, `{"uuid":"texture-task","jobs":{"subscription_key":"texture-subscription"}}`)
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	offering := processingOffering("rodin_texture_only", generation.Retexture)
	got, err := port.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "texture", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input: json.RawMessage(`{"model_file":["asset-model"],"reference":["asset-reference"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"model_file": {{ID: "model-7", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}},
			"reference":  {{ID: "reference-11", OwnerID: "owner", OriginalFilename: "reference.png", MediaType: "image/png"}},
		},
	})
	if err != nil || got.Job == nil {
		t.Fatalf("Start = %#v, %v", got, err)
	}
	got, err = port.Start(context.Background(), generation.StartRequest{
		OwnerID: "owner", TaskID: "texture", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input:       json.RawMessage(`{"model_file":["asset-model"]}`),
		InputAssets: map[string][]generation.InputAsset{"model_file": {{ID: "model-7", OwnerID: "owner"}}},
	})
	if err == nil || got.Job != nil {
		t.Fatal("Texture accepted a missing locally owned reference image")
	}
	wrongOwner := generation.StartRequest{
		OwnerID: "owner", TaskID: "texture", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input: json.RawMessage(`{"model_file":["asset-model"],"reference":["asset-reference"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"model_file": {{ID: "model-7", OwnerID: "owner", OriginalFilename: "model.glb", MediaType: "model/gltf-binary"}},
			"reference":  {{ID: "reference-11", OwnerID: "other-owner", OriginalFilename: "reference.png", MediaType: "image/png"}},
		},
	}
	if _, err := port.Start(context.Background(), wrongOwner); err == nil {
		t.Fatal("Texture accepted a file owned by another user")
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid source reached upstream: POST count=%d", calls.Load())
	}
}

func TestBangMissingSubmitIdentifiersIsUnknownAndNeverReplayed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"uuid":"ambiguous-without-subscription"}`)
	}))
	t.Cleanup(server.Close)
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	offering := processingOffering("bang", generation.Capability("mesh-segmentation"))
	result, err := port.Start(context.Background(), generation.StartRequest{
		TaskID: "bang", AttemptID: "attempt", SubmissionKey: "submission", Offering: offering,
		Input: json.RawMessage(`{"source_task":"source"}`),
		TaskDependencies: map[string]generation.TaskDependency{"source_task": {
			TaskID: "source", Capability: generation.ImageTo3D, OfferingID: "rodin", ModelID: "rodin-2.5-business-alias", ProviderID: "hyper3d",
			UpstreamJob: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "rodin-top-uuid"},
		}},
	})
	var providerErr *generation.ProviderError
	if err == nil || !errors.As(err, &providerErr) || providerErr.Failure.Class != generation.FailureSubmissionUnknown || !providerErr.Failure.Ambiguous {
		t.Fatalf("Start error = %v", err)
	}
	if result.Job != nil || calls.Load() != 1 {
		t.Fatalf("result=%#v submit calls=%d", result, calls.Load())
	}
}

func TestProcessingOfferingsRequireMatchingLocalInputContract(t *testing.T) {
	for name, value := range map[string]generation.Offering{
		"bang": func() generation.Offering {
			v := processingOffering("bang", generation.Capability("mesh-segmentation"))
			v.Outputs = nil
			return v
		}(),
		"texture": func() generation.Offering {
			v := processingOffering("rodin_texture_only", generation.Retexture)
			v.Parameters = v.Parameters[:1]
			return v
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := hyper3d.ValidateOffering(value); err == nil {
				t.Fatal("invalid source schema was accepted")
			}
		})
	}
}

func TestBangUsesMeshSegmentationCapability(t *testing.T) {
	correct := processingOffering("bang", generation.Capability("mesh-segmentation"))
	if err := hyper3d.ValidateOffering(correct); err != nil {
		t.Fatalf("Bang with the existing mesh-segmentation capability was rejected: %v", err)
	}
	misclassified := processingOffering("bang", generation.Remesh)
	if err := hyper3d.ValidateOffering(misclassified); err == nil {
		t.Fatal("Bang was accepted as remesh")
	}
}

func TestEndpointFieldAndEveryDeclaredMediaTypeAreValidated(t *testing.T) {
	validBang := processingOffering("bang", generation.Capability("mesh-segmentation"))
	for i := range validBang.Parameters {
		switch validBang.Parameters[i].WireName {
		case "model":
			validBang.Parameters[i].MediaTypes = append(validBang.Parameters[i].MediaTypes, "model/gltf+json")
		case "image":
			validBang.Parameters[i].MediaTypes = append(validBang.Parameters[i].MediaTypes, "image/not-real")
		}
	}
	if err := hyper3d.ValidateOffering(validBang); err == nil {
		t.Fatal("Bang accepted an unsupported member of a declared media list")
	}

	for _, wire := range []string{"quality", "uhd_texture", "addons", "mesh_mode", "quality_override"} {
		offering := processingOffering("rodin_texture_only", generation.Retexture)
		offering.Parameters = append(offering.Parameters, generation.Parameter{Name: wire, Type: "text", WireName: wire})
		if err := hyper3d.ValidateOffering(offering); err == nil {
			t.Fatalf("Texture accepted parameter %q from another endpoint or an unsupported field", wire)
		}
	}
	for _, wire := range []string{"hd_texture", "texture_delight", "texture_mode", "escore", "reference_scale", "seed"} {
		offering := processingOffering("bang", generation.Capability("mesh-segmentation"))
		offering.Parameters = append(offering.Parameters, generation.Parameter{Name: wire, Type: "text", WireName: wire})
		if err := hyper3d.ValidateOffering(offering); err == nil {
			t.Fatalf("Bang accepted Texture-only or incorrectly typed field %q", wire)
		}
	}
}

func assertFile(t *testing.T, form *multipart.Form, field, filename, body string) {
	t.Helper()
	files := form.File[field]
	if len(files) != 1 || files[0].Filename != filename {
		t.Fatalf("%s files = %#v", field, files)
	}
	file, err := files[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != body {
		t.Fatalf("%s bytes = %q, %v", field, data, err)
	}
}

func TestBangNativeSourceRespectsDeclaredOfferingAllowlist(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"uuid":"unexpected","jobs":{"subscription_key":"unexpected"}}`)
	}))
	defer server.Close()
	port := adapter(t, server.URL+"/api/v2", server.Client(), imageInputs{})
	step := processingOffering("bang", generation.Capability("mesh-segmentation"))
	step.Parameters[0].TaskOfferings = []generation.OfferingID{"hyper3d/rodin-gen-2.5/text-to-3d"}
	_, err := port.Start(context.Background(), generation.StartRequest{Offering: step, Input: json.RawMessage(`{"source_task":"source"}`), TaskDependencies: map[string]generation.TaskDependency{"source_task": {TaskID: "source", ProviderID: "hyper3d", Capability: generation.TextTo3D, OfferingID: "unqualified-future-offering", ModelID: "legitimate-business-alias", UpstreamJob: generation.UpstreamJob{ProviderID: "hyper3d", RemoteID: "remote"}}}})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("unqualified native source reached dispatch: err=%v calls=%d", err, calls.Load())
	}
}

func TestBangFixedMaterialMayRequireReferenceImageInSchema(t *testing.T) {
	step := processingOffering("bang", generation.Capability("mesh-segmentation"))
	step.Parameters[2].Required = true
	if err := hyper3d.ValidateOffering(step); err != nil {
		t.Fatalf("a narrower required-image declaration must be accepted: %v", err)
	}
}

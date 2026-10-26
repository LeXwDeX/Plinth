package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestHyper3DConnectionAndRodinOfferings(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	connection := connectionByID(t, taskCatalog.Application().Runtime.ProviderConnections, "hyper3d")
	if connection.Protocol != "hyper3d.rodin" || connection.AdapterKey != "hyper3d.rodin.v1" {
		t.Fatalf("unexpected Hyper3D connection: %#v", connection)
	}
	wantCapabilities := []schema.Capability{schema.TextTo3D, schema.ImageTo3D, schema.Capability("mesh-segmentation"), schema.Retexture}
	if !reflect.DeepEqual(connection.Capabilities, wantCapabilities) {
		t.Fatalf("Hyper3D connection capabilities = %v, want %v", connection.Capabilities, wantCapabilities)
	}
	setting := taskCatalog.Application().Runtime.Configuration.Settings[connection.APIKeySetting]
	if !setting.Secret || setting.Default != "" {
		t.Fatal("Hyper3D key must remain an unset secret")
	}
	public, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "hyper3d_api_key") || strings.Contains(string(public), "hyper3d_base_url") {
		t.Fatal("Hyper3D runtime connection leaked into public schema")
	}
	for _, capability := range []schema.Capability{schema.TextTo3D, schema.ImageTo3D} {
		id := schema.OfferingID("hyper3d/rodin-gen-2.5/" + string(capability))
		offering, ok := taskCatalog.Get(id)
		if !ok {
			t.Fatalf("missing Offering %s", id)
		}
		// #188 route B: enabled with the exact signed-download host seen in a
		// real response; no wildcard or parent domain.
		if !offering.Enabled || offering.NativePricing == nil || offering.NativePricing.Unit != "hyper3d.credit" ||
			len(offering.AssetHosts) != 1 || offering.AssetHosts[0] != "file.hyper3d.com" {
			t.Fatalf("Rodin Offering contract: %#v", offering)
		}
		if len(offering.Outputs) != 2 || offering.Outputs[0].Role != "model" || offering.Outputs[0].Variant != "glb" ||
			offering.Outputs[0].Delivery != "required" || len(offering.Outputs[0].MediaTypes) != 1 ||
			offering.Outputs[0].MediaTypes[0] != "model/gltf-binary" ||
			offering.Outputs[1].Role != "thumbnail" || offering.Outputs[1].Delivery != "background" ||
			len(offering.Outputs[1].MediaTypes) != 1 || offering.Outputs[1].MediaTypes[0] != "image/webp" {
			t.Fatalf("Rodin outputs must be one required GLB plus a background preview: %#v", offering.Outputs)
		}
		var options struct {
			RequestTemplate map[string]any   `json:"request_template"`
			DownloadFiles   []map[string]any `json:"download_files"`
		}
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		// extreme-high texture without HighPack is 6K (Gen-2.5 texture table),
		// which keeps the main GLB inside the generated-glb-v1 profile; HighPack
		// would make it 12K (about 164 MB with 151M-pixel maps).
		if options.RequestTemplate["texture_mode"] != "extreme-high" || options.RequestTemplate["addons"] != nil ||
			options.RequestTemplate["uhd_texture"] != nil || len(options.DownloadFiles) != 2 ||
			options.DownloadFiles[0]["name_suffix"] != "_pbr.glb" || options.DownloadFiles[1]["name"] != "preview.webp" {
			t.Fatalf("Rodin wire/download selection: %s", offering.ProviderOptions)
		}
		listed := false
		for _, visible := range taskCatalog.List(capability) {
			listed = listed || visible.ID == id
		}
		if !listed {
			t.Fatalf("enabled Offering %s missing from live catalog", id)
		}
	}
	if _, _, err := taskCatalog.Normalize(schema.TextTo3D, "hyper3d/rodin-gen-2.5/text-to-3d", []byte(`{"prompt":"cat","authorize_extra_credits":true}`)); err != nil {
		t.Fatalf("enabled Rodin text Offering rejected a prompt: %v", err)
	}
	if _, _, err := taskCatalog.Normalize(schema.ImageTo3D, "hyper3d/rodin-gen-2.5/image-to-3d", []byte(`{"prompt":"cat","authorize_extra_credits":true}`)); err == nil {
		t.Fatal("Rodin image Offering accepted a task without reference images")
	}
}

func TestHyper3DRoutesFailClosed(t *testing.T) {
	for _, field := range []string{"endpoint", "observe_endpoint", "download_endpoint"} {
		t.Run(field, func(t *testing.T) {
			payload := mutatedProviderRoute(t, "hyper3d/rodin-gen-2.5/text-to-3d", func(offering map[string]any) {
				offering["provider_options"].(map[string]any)[field] = "https://other.invalid/submit"
			})
			if _, err := catalog.Load(bytes.NewReader(payload)); err == nil {
				t.Fatalf("accepted unsafe %s", field)
			}
		})
	}
}

func TestHyper3DNativeQuotesMatchWireCandidates(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"hyper3d/rodin-gen-2.5/text-to-3d", "hyper3d/rodin-gen-2.5/image-to-3d", "hyper3d/bang/rodin-task", "hyper3d/bang/local-model", "hyper3d/texture/local-model"} {
		o, ok := c.Get(schema.OfferingID(id))
		if !ok {
			t.Fatal(id)
		}
		rodin := strings.Contains(id, "rodin-gen-2.5")
		if !o.Enabled || !o.CreditsMetered || o.NativePricing == nil || o.NativePricing.Unit != "hyper3d.credit" || o.EstimatedCredits != 0 || o.Cost != nil {
			t.Fatalf("invalid enabled native price contract: %+v", o)
		}
		q, err := o.EvaluateNativeQuote(json.RawMessage(`{"authorize_extra_credits":true}`))
		if err != nil {
			t.Fatal(err)
		}
		want, base := "0.5", "0.5"
		switch {
		case rodin:
			want = "3"
			if q.ExtraAmount != "2.5" || len(q.Components) != 2 {
				t.Fatalf("incomplete additive quote: %+v", q)
			}
		case strings.HasPrefix(id, "hyper3d/texture/"):
			// The historical Basic/extreme-high request consumed 2. Keep that
			// current schema estimate distinct from the still-unqualified High
			// request; its actual charge needs a separate live receipt.
			want, base = "2", "2"
			if q.ExtraAmount != "0" || len(q.Components) != 0 {
				t.Fatalf("Texture quote must be one fixed amount: %+v", q)
			}
		}
		if q.TotalAmount != want || q.BaseAmount != base {
			t.Fatalf("%s quote=%+v", id, q)
		}
		if _, _, err = c.Normalize(o.Capability, o.ID, json.RawMessage(`{}`)); err == nil {
			t.Fatal("disabled Bang candidate or input-less Rodin task executable")
		}
	}
	native, _ := c.Get("hyper3d/bang/rodin-task")
	source := native.Parameters[0]
	if source.Type != "task" || source.TaskSourceKind != "upstream_job" || !source.SameProvider || !source.SameSession || len(source.TaskOfferings) != 2 {
		t.Fatal("Bang native source is not narrowly qualified")
	}
	local, _ := c.Get("hyper3d/bang/local-model")
	if local.Parameters[0].UploadedModel == nil || local.Parameters[0].UploadedModel.ProfileID != "static-glb-v1" || !local.Parameters[1].Required {
		t.Fatal("local READY model/reference contract missing")
	}
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Offerings []schema.Offering `json:"offerings"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, o := range payload.Offerings {
		if o.ProviderID == "hyper3d" && o.Capability == schema.Retexture && o.ID != "hyper3d/texture/local-model" {
			t.Fatalf("unqualified Hyper3D Texture Offering registered: %s", o.ID)
		}
	}
}

// The historical direct samples establish the PBR GLB shape, not public
// qualification. The current Texture candidate uses the highest documented
// pack resolution (extreme-high + High = 12K); the earlier Basic/6K sample
// does not qualify this revision. All candidates remain disabled.
func TestHyper3DProcessingCandidatesDeclareQualifiedOutputContract(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	templates := map[string]map[string]any{
		"hyper3d/bang/rodin-task":     {"geometry_file_format": "glb", "material": "PBR", "resolution": "High", "mesh_mode": "Raw", "quality_override": float64(2000000), "strength": float64(5)},
		"hyper3d/bang/local-model":    {"geometry_file_format": "glb", "material": "PBR", "resolution": "High", "mesh_mode": "Raw", "quality_override": float64(2000000), "strength": float64(5)},
		"hyper3d/texture/local-model": {"geometry_file_format": "glb", "material": "PBR", "resolution": "High", "texture_mode": "extreme-high", "hd_texture": true},
	}
	for id, template := range templates {
		o, ok := c.Get(schema.OfferingID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		wantRevision := "2026-10-21.187.2"
		if id == "hyper3d/bang/rodin-task" {
			wantRevision = "2026-10-21.187.3"
		}
		if !o.Enabled || o.Default || o.Revision != wantRevision || len(o.AssetHosts) != 1 || o.AssetHosts[0] != "file.hyper3d.com" {
			t.Fatalf("%s must be enabled at its accepted revision with the qualified host: %+v", id, o)
		}
		if strings.HasPrefix(id, "hyper3d/bang/") && o.Capability != schema.Capability("mesh-segmentation") {
			t.Fatalf("%s must expose mesh segmentation, not remesh: %s", id, o.Capability)
		}
		if strings.HasPrefix(id, "hyper3d/bang/") {
			wantName, wantEnglish := "Bang 部件拆分与重建（Rodin 来源）", "Bang part splitting and reconstruction (Rodin source)"
			if strings.HasSuffix(id, "/local-model") {
				wantName, wantEnglish = "Bang 部件拆分与重建（本地模型）", "Bang part splitting and reconstruction (local model)"
			}
			if o.ModelName != wantName || c.Application().Localization.Bundles["en"].OfferingTexts[wantName] != wantEnglish {
				t.Fatalf("%s localized name mismatch: %q / %q", id, o.ModelName, c.Application().Localization.Bundles["en"].OfferingTexts[wantName])
			}
		}
		if len(o.Outputs) != 1 || o.Outputs[0].Role != "model" || o.Outputs[0].Variant != "glb" || o.Outputs[0].Delivery != "required" ||
			len(o.Outputs[0].MediaTypes) != 1 || o.Outputs[0].MediaTypes[0] != "model/gltf-binary" {
			t.Fatalf("%s outputs must be one required GLB: %#v", id, o.Outputs)
		}
		var options struct {
			Endpoint        string              `json:"endpoint"`
			RequestTemplate map[string]any      `json:"request_template"`
			DownloadFiles   []map[string]string `json:"download_files"`
		}
		if err := json.Unmarshal(o.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		if len(options.DownloadFiles) != 1 || len(options.DownloadFiles[0]) != 1 || options.DownloadFiles[0]["name_suffix"] != "_pbr.glb" {
			t.Fatalf("%s must select only the PBR pack: %s", id, o.ProviderOptions)
		}
		if !reflect.DeepEqual(options.RequestTemplate, template) {
			t.Fatalf("%s wire template = %#v", id, options.RequestTemplate)
		}
	}
	texture, _ := c.Get("hyper3d/texture/local-model")
	if texture.Capability != schema.Retexture || texture.Domain != "processing" || len(texture.Parameters) != 3 {
		t.Fatalf("Texture candidate shape: %+v", texture)
	}
	model, image, prompt := texture.Parameters[0], texture.Parameters[1], texture.Parameters[2]
	if model.Type != "asset" || model.WireName != "model" || !model.Required || model.UploadedModel == nil || model.UploadedModel.ProfileID != "static-glb-v1" ||
		model.MaxBytes != 10<<20 || len(model.MediaTypes) != 1 || model.MediaTypes[0] != "model/gltf-binary" {
		t.Fatalf("Texture model input must be one owned static GLB: %+v", model)
	}
	if image.Type != "asset" || image.WireName != "image" || !image.Required || image.MaxBytes <= 0 {
		t.Fatalf("Texture reference image must be required: %+v", image)
	}
	if prompt.Type != "text" || prompt.WireName != "prompt" || prompt.Required {
		t.Fatalf("Texture prompt must stay optional text: %+v", prompt)
	}
	if _, _, err := c.Normalize(schema.Retexture, texture.ID, json.RawMessage(`{"source_asset":"a","reference_image":"b"}`)); err != nil {
		t.Fatalf("enabled Texture Offering must normalize its file inputs: %v", err)
	}
	if _, _, err := c.Normalize(schema.Retexture, texture.ID, json.RawMessage(`{"source_asset":"a"}`)); err == nil {
		t.Fatal("enabled Texture Offering accepted a missing reference image")
	}
}

// TestHyper3DBangAndTextureEnabledAtAcceptedRevisions pins #187 enablement to
// exactly what the isolated public-Task acceptance (2026-10-21) froze: the
// private acceptance Catalog differed from this default only in these three
// enabled bits, so the revisions, wire, host, outputs and native quotes below
// are the accepted ones. A later change needs a new revision and acceptance.
func TestHyper3DBangAndTextureEnabledAtAcceptedRevisions(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]struct{ revision, input, total string }{
		"hyper3d/bang/rodin-task":     {"2026-10-21.187.3", `{"source_task":"source"}`, "0.5"},
		"hyper3d/bang/local-model":    {"2026-10-21.187.2", `{"source_asset":"model","reference_image":"image"}`, "0.5"},
		"hyper3d/texture/local-model": {"2026-10-21.187.2", `{"source_asset":"model","reference_image":"image"}`, "2"},
	} {
		offering, ok := product.Get(schema.OfferingID(id))
		if !ok || !offering.Enabled || offering.Default || offering.Revision != want.revision {
			t.Fatalf("%s enablement/revision: %+v", id, offering)
		}
		listed := false
		for _, candidate := range product.List(offering.Capability) {
			listed = listed || candidate.ID == offering.ID
		}
		if !listed {
			t.Fatalf("%s is not listed as an executable Offering", id)
		}
		quote, err := offering.EvaluateNativeQuote(json.RawMessage(want.input))
		if err != nil || quote.TotalAmount != want.total || quote.ExtraAmount != "0" {
			t.Fatalf("%s quote=%+v err=%v", id, quote, err)
		}
	}
}

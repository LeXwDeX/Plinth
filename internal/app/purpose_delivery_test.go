package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

// #278: what a Tripo task must deliver follows the purpose that chose its
// format set. The whole stack runs against the fake upstream (the real
// Catalog, the kernel pinning the required slots at submission, the Tripo
// adapter, the asset store with its delivery checks): a game task requires and
// delivers the GLB and the FBX only (and asks for the advanced export: PNG
// textures at 2K and the origin at the bottom centre), a print task the GLB,
// STL and 3MF, and a display task still all six formats. The display and print
// tasks keep the upstream-default export, so their conversion requests are the
// plain {input, format} and their prices are the basic ones.

type convertRequestLog struct {
	mu          sync.Mutex
	converts    []map[string]any
	generations []map[string]any
}

func (l *convertRequestLog) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.converts, l.generations = nil, nil
}

func newRecordingFakeUpstream(t *testing.T) (*httptest.Server, *convertRequestLog) {
	t.Helper()
	fake, err := registry.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1, ArtifactOrigin: "https://" + fakeAssetHost})
	if err != nil {
		t.Fatal(err)
	}
	log := &convertRequestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			log.mu.Lock()
			switch {
			case strings.HasSuffix(r.URL.Path, "/models/convert"):
				log.converts = append(log.converts, body)
			case strings.HasSuffix(r.URL.Path, "/generation/text-to-model"):
				log.generations = append(log.generations, body)
			}
			log.mu.Unlock()
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, log
}

// submitPurposeTask is submitApproved with the purpose's reason for every value
// it moves off an advised default: the kernel refuses such an adjustment
// without one, and a purpose supplies it (provenance "profile:<id>").
func submitPurposeTask(t *testing.T, h *acmeHarness, input json.RawMessage, key string) generation.Accepted {
	t.Helper()
	ctx := context.Background()
	var values map[string]json.RawMessage
	if err := json.Unmarshal(input, &values); err != nil {
		t.Fatal(err)
	}
	reasons, sources := map[string]string{}, map[string]string{}
	for _, name := range []string{"output_formats", "export_pivot_bottom_center", "export_texture_size", "export_texture_format"} {
		if _, adjusted := values[name]; adjusted {
			reasons[name], sources[name] = "the purpose asks for these", "profile:test"
		}
	}
	prepared, err := h.module.PrepareInput(ctx, generation.PrepareInputCommand{
		OwnerID: "owner-acme", SessionID: "acme-session", Capability: generation.TextTo3D,
		OfferingID: "tripo/tripo-v3.1-full/text-to-3d", Input: input,
		ParameterReasons: reasons, ParameterSources: sources,
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := "acme:" + string(prepared.ID)
	grants := executiongrant.Service{Grants: h.repository, Prepared: h.repository}
	principal := executiongrant.Principal{OwnerID: "owner-acme", ActorID: "acme-test-human", Source: executiongrant.HumanBrowser}
	grant, err := grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: "acme-session", OfferingID: string(prepared.OfferingID),
		OfferingRevision: prepared.OfferingRevision, PreparedInputID: string(prepared.ID),
		PreparedInputHash: prepared.InputHash, IdempotencyKey: key, OperationID: operationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Decide(ctx, principal, grant.RequestID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	accepted, err := h.module.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: "owner-acme", SessionID: "acme-session", PreparedInputID: prepared.ID,
		IdempotencyKey: key, OperationID: operationID, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return accepted
}

func TestPurposeFormatSetsDecideTheRequiredAndDeliveredSlots(t *testing.T) {
	server, log := newRecordingFakeUpstream(t)
	fakeURL, _ := url.Parse(server.URL)
	settings := map[string]string{
		"tripo_base_url": server.URL + "/tripo/v3",
		"tripo_api_key":  fakeUpstreamKeyPrefix + "test-key",
	}
	taskCatalog, err := loadCatalogForShape("", upstreamShape{origin: fakeURL})
	if err != nil {
		t.Fatal(err)
	}
	harness := newAcmeHarnessWithTransport(t, taskCatalog, settings, fakeShapeHTTPTransport(fakeURL))

	allFormats := []string{"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"}
	for _, tc := range []struct {
		name    string
		changes string
		formats []string
		// options are the export fields each conversion request must carry besides
		// input and format: none for the basic conversion.
		options map[string]any
		// quote is the frozen price: the base task plus the conversions.
		quote, conversions string
		// texture is the format of the texture the stored FBX embeds, when there is one.
		texture string
	}{
		// What display, web/AR and every task that asks for nothing special get:
		// the plain conversion and the price this Offering always had.
		{"display keeps all six on the basic conversion", ``, allFormats, nil, "90", "30", "jpeg"},
		{"game asks for the GLB and FBX with the advanced export",
			`,"output_formats":["GLTF","FBX"],"export_texture_format":"PNG","export_texture_size":"2k","export_pivot_bottom_center":true`,
			[]string{"GLTF", "FBX"}, map[string]any{"texture_format": "PNG", "texture_size": float64(2048), "pivot_to_center_bottom": true}, "80", "20", "png"},
		{"print keeps STL and 3MF next to the GLB on the basic conversion", `,"output_formats":["GLTF","STL","3MF"]`,
			[]string{"GLTF", "STL", "3MF"}, nil, "75", "15", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log.reset()
			input := `{"prompt":"a plain cube","authorize_extra_credits":true` + tc.changes + `}`
			accepted := submitPurposeTask(t, harness, json.RawMessage(input), "purpose-"+strings.ReplaceAll(tc.name, " ", "-"))
			task := harness.runUntilTerminal(t, "owner-acme", accepted.TaskID)
			if task.State != generation.TaskSucceeded {
				t.Fatalf("state = %s, failure = %#v", task.State, task.Failure)
			}

			// The Task's own Offering pins which converted-model slots it requires.
			wantVariants := make([]string, 0, len(tc.formats))
			for _, format := range tc.formats {
				wantVariants = append(wantVariants, "converted."+strings.ToLower(format))
			}
			slices.Sort(wantVariants)
			var required, delivered []string
			for _, output := range task.Offering.Outputs {
				if output.Role == "converted_model" && output.Minimum != nil && *output.Minimum == 1 {
					required = append(required, output.Variant)
				}
			}
			for _, asset := range task.Assets {
				if asset.Role == "converted_model" {
					delivered = append(delivered, asset.Variant)
				}
			}
			slices.Sort(required)
			slices.Sort(delivered)
			if !slices.Equal(required, wantVariants) {
				t.Errorf("required converted slots = %v, want %v", required, wantVariants)
			}
			if !slices.Equal(delivered, wantVariants) {
				t.Errorf("delivered converted assets = %v, want %v", delivered, wantVariants)
			}
			modelAssets := 0
			for _, asset := range task.Assets {
				if asset.Role == "model" {
					modelAssets++
				}
			}
			if modelAssets != 1 {
				t.Errorf("model assets = %d, want the one GLB", modelAssets)
			}

			// Only the selected formats were exported, each with the declared options.
			log.mu.Lock()
			defer log.mu.Unlock()
			sent := map[string]bool{}
			for _, body := range log.converts {
				format, _ := body["format"].(string)
				sent[format] = true
				// input and format, plus only the options the purpose moved off the
				// upstream defaults (the FBX preset stays on its default, so is never sent).
				if len(body) != 2+len(tc.options) {
					t.Errorf("%s export = %v, want input, format and %v", format, body, tc.options)
				}
				for field, value := range tc.options {
					if body[field] != value {
						t.Errorf("%s export %s = %v, want %v", format, field, body[field], value)
					}
				}
			}
			if len(log.converts) != len(tc.formats) {
				t.Errorf("exports = %d (%v), want %d", len(log.converts), sent, len(tc.formats))
			}
			for _, format := range tc.formats {
				if !sent[format] {
					t.Errorf("%s was never exported", format)
				}
			}
			if len(log.generations) != 1 {
				t.Fatalf("generation requests = %d", len(log.generations))
			}
			for _, name := range []string{"output_formats", "authorize_extra_credits", "export_texture_size", "export_pivot_bottom_center",
				"export_fbx_preset", "texture_format", "texture_size", "pivot_to_center_bottom", "fbx_preset"} {
				if _, sent := log.generations[0][name]; sent {
					t.Errorf("the generation request carries %s", name)
				}
			}

			// The frozen price: the basic conversions unless the purpose opted into the
			// advanced export (5 or 10 credits each).
			if task.NativeQuote == nil || task.NativeQuote.TotalAmount != tc.quote {
				t.Errorf("frozen quote = %+v, want a total of %s", task.NativeQuote, tc.quote)
			} else {
				charged := ""
				for _, charge := range task.NativeQuote.Components {
					if charge.ID == "format_conversions" {
						charged = charge.Amount
					}
				}
				if charged != tc.conversions {
					t.Errorf("conversions are priced %q, want %s", charged, tc.conversions)
				}
			}

			// The stored FBX embeds the texture format that was asked for (JPEG, the
			// upstream default, when nothing was), read from its stored bytes.
			markers := map[string]string{"png": "\x89PNG\r\n\x1a\n", "jpeg": "\xff\xd8\xff"}
			for _, asset := range task.Assets {
				if asset.Variant != "converted.fbx" {
					continue
				}
				content, err := harness.module.OpenAsset(context.Background(), "owner-acme", task.ID, asset.ID)
				if err != nil {
					t.Fatalf("the delivered FBX cannot be opened: %v", err)
				}
				data, _ := io.ReadAll(content.Body)
				_ = content.Body.Close()
				for format, marker := range markers {
					if bytes.Contains(data, []byte(marker)) != (format == tc.texture) {
						t.Errorf("the delivered FBX %s a %s texture, want %s", map[bool]string{true: "carries", false: "lacks"}[bytes.Contains(data, []byte(marker))], format, tc.texture)
					}
				}
			}
		})
	}
}

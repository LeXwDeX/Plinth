package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestProductSchemaIsPubliclyProjectedForRuntimeAndFrontend(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"user"},
		func(application *catalog.ApplicationSchema) {
			application.Canvas.Frontend.Export.PaddingPixels = 73
			// An opaque artboard colour lets the media type be one without alpha (#271).
			application.Canvas.Frontend.Export.ArtboardBackgroundColor = "#123456"
			application.Canvas.Frontend.Export.OutputMediaType = "image/custom"
			application.Canvas.Frontend.Export.Frame.LineDash = []float64{9, 3}
		},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/schema", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("ETag") == "" {
		t.Fatal("ETag is empty")
	}
	if response.Header().Get("Cache-Control") !=
		"public, max-age=60, stale-while-revalidate=300" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var body struct {
		Schema struct {
			Revision     string `json:"revision"`
			Capabilities []struct {
				ID        string `json:"id"`
				Label     string `json:"label"`
				LabelCN   string `json:"label_cn"`
				LegacyAPI *struct {
					Path string `json:"path"`
				} `json:"legacy_api"`
			} `json:"capabilities"`
			Authentication struct {
				Messages struct {
					InvalidCredentials string `json:"invalid_credentials"`
				} `json:"messages"`
				Login struct {
					Title string `json:"title"`
				} `json:"login"`
			} `json:"authentication"`
			Runtime struct {
				HTTP struct {
					Routes struct {
						Tasks string `json:"tasks"`
					} `json:"routes"`
				} `json:"http"`
			} `json:"runtime"`
			Canvas struct {
				Frontend struct {
					Export struct {
						PaddingPixels           int    `json:"padding_pixels"`
						OutputMediaType         string `json:"output_media_type"`
						ArtboardBackgroundColor string `json:"artboard_background_color"`
						Frame                   struct {
							LineDash []float64 `json:"line_dash"`
						} `json:"frame"`
					} `json:"export"`
				} `json:"frontend"`
			} `json:"canvas"`
		} `json:"schema"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Schema.Revision == "" ||
		len(body.Schema.Capabilities) != 25 ||
		body.Schema.Capabilities[0].Label == "" ||
		body.Schema.Capabilities[0].LabelCN == "" ||
		body.Schema.Capabilities[0].LegacyAPI == nil ||
		body.Schema.Runtime.HTTP.Routes.Tasks == "" ||
		body.Schema.Authentication.Messages.InvalidCredentials != "用户名或密码错误" ||
		body.Schema.Authentication.Login.Title != "Plinth" ||
		body.Schema.Canvas.Frontend.Export.PaddingPixels != 73 ||
		body.Schema.Canvas.Frontend.Export.OutputMediaType != "image/custom" ||
		body.Schema.Canvas.Frontend.Export.ArtboardBackgroundColor != "#123456" ||
		len(body.Schema.Canvas.Frontend.Export.Frame.LineDash) != 2 ||
		body.Schema.Canvas.Frontend.Export.Frame.LineDash[0] != 9 ||
		body.Schema.Canvas.Frontend.Export.Frame.LineDash[1] != 3 {
		t.Fatalf("schema = %#v", body.Schema)
	}
	rigChecks := 0
	for _, capability := range body.Schema.Capabilities {
		if capability.ID != "rig-check" {
			continue
		}
		rigChecks++
		if capability.Label != "Rig Check" || capability.LabelCN != "绑骨检查" || capability.LegacyAPI != nil {
			t.Fatalf("Rig Check projection = %#v", capability)
		}
	}
	if rigChecks != 1 {
		t.Fatalf("Rig Check capability count = %d, want 1", rigChecks)
	}
	// Disabled geometry Offerings still publish their capability vocabulary;
	// this must not invent a legacy write route or drop localized labels.
	for id, labels := range map[string][2]string{
		"mesh-segmentation": {"Mesh Segmentation", "模型分割"},
		"mesh-completion":   {"Mesh Part Completion", "部件补全"},
	} {
		count := 0
		for _, capability := range body.Schema.Capabilities {
			if capability.ID == id {
				count++
				if capability.Label != labels[0] || capability.LabelCN != labels[1] || capability.LegacyAPI != nil {
					t.Fatalf("geometry capability projection = %#v", capability)
				}
			}
		}
		if count != 1 {
			t.Fatalf("geometry capability %q count = %d, want 1", id, count)
		}
	}
}

func TestOfferingsProjectionExposesSameProviderTaskDependencies(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/offerings", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Offerings []struct {
			ID         string `json:"id"`
			Parameters []struct {
				Name         string          `json:"name"`
				Type         string          `json:"type"`
				SameProvider bool            `json:"same_provider"`
				Required     bool            `json:"required"`
				MinItems     int             `json:"min_items"`
				Default      json.RawMessage `json:"default"`
				Options      []struct {
					Value string `json:"value"`
				} `json:"options"`
			} `json:"parameters"`
		} `json:"offerings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	formatContractFound := false
	for _, offering := range body.Offerings {
		for _, parameter := range offering.Parameters {
			if parameter.Type == "task" {
				declared[offering.ID] = parameter.SameProvider
			}
			if offering.ID == "meshy/meshy-7.1/text-to-3d" && parameter.Name == "target_formats" {
				want := []string{"glb", "fbx", "obj", "stl", "usdz", "3mf"}
				got := make([]string, 0, len(parameter.Options))
				for _, option := range parameter.Options {
					got = append(got, option.Value)
				}
				var defaults []string
				if err := json.Unmarshal(parameter.Default, &defaults); err != nil {
					t.Fatalf("decode Meshy 7.1 target_formats default: %v", err)
				}
				formats := []string{"glb", "obj", "fbx", "stl", "usdz", "3mf"}
				formatContractFound = parameter.Required && parameter.MinItems == 6 &&
					len(defaults) == 6 && slices.Equal(got, want) && slices.Equal(defaults, formats)
			}
		}
	}
	if !formatContractFound {
		t.Fatal("Meshy 7.1 text-to-3d must publicly require and default to all six official export formats")
	}
	for _, id := range []string{
		"meshy/meshy-7.1/text-to-3d",
		"meshy/processing/remesh",
		"meshy/processing/retexture",
		"meshy/processing/rigging",
		"meshy/processing/animation",
	} {
		if !declared[id] {
			t.Errorf("offering %q does not declare same_provider", id)
		}
	}
}

func TestPublicProductSchemaDoesNotExposeServerEnvironmentBindings(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/schema", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	var body struct {
		Schema struct {
			Runtime map[string]json.RawMessage `json:"runtime"`
		} `json:"schema"`
	}
	if err := json.NewDecoder(bytes.NewReader(response.Body.Bytes())).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body.Schema.Runtime["configuration"]; exists {
		t.Fatal("public Product Schema exposed server runtime configuration")
	}
	for _, key := range []string{"coordinator", "provider_accounts", "task_deletion", "task_audit"} {
		if _, exists := body.Schema.Runtime[key]; exists {
			t.Fatalf("public Product Schema exposed server runtime %s", key)
		}
	}
	if bytes.Contains(response.Body.Bytes(), []byte("APP_ENV")) ||
		bytes.Contains(response.Body.Bytes(), []byte("AUTH_TOKENS_JSON")) {
		t.Fatal("public Product Schema exposed environment source names")
	}
}

func TestProductSchemaRouteComesFromProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		[]string{"user"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.Routes.ProductSchema = "/configured-product-schema"
		},
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/configured-product-schema",
		nil,
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

// A count-priced Offering names the parameter its estimate is multiplied by, so
// the client can show cost x count without a second price table; an Offering
// priced per request names none.
func TestOfferingsProjectionNamesTheCountParameterOfCountPricedOfferings(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/offerings?capability=text-to-image", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Offerings []struct {
			ID                            string `json:"id"`
			EstimatedCreditsUnitParameter string `json:"estimated_credits_unit_parameter"`
		} `json:"offerings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	units := map[string]string{}
	for _, offering := range body.Offerings {
		units[offering.ID] = offering.EstimatedCreditsUnitParameter
	}
	if units["openai/gpt-image-2/text-to-image"] != "n" {
		t.Fatalf("gpt-image-2 unit parameter = %q, want n (units = %#v)", units["openai/gpt-image-2/text-to-image"], units)
	}
	if units["gemini/gemini-3-pro-image/text-to-image"] != "" {
		t.Fatalf("a per-request Offering must not name a unit parameter: %#v", units)
	}
}

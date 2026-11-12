package hyper3d_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
)

// #279: Rodin's game-relevant knobs (topology, face count, T/A pose, normal
// bake, seed) are Schema parameters. These tests drive the real Adapter with
// the shipped Offerings against the Hyper3D fake, whose Gen-2.5 value rules are
// constructed from the official page
// (https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5, read
// 2026-11-12), and read the form the fake received. Nothing here calls the real
// upstream: the Quad texture resolution and billing stay unverified.

// formPart is one part of a received submission, in wire order.
type formPart struct {
	name, value string
	file        bool
}

func (part formPart) String() string {
	if part.file {
		return part.name + "=<file>"
	}
	return part.name + "=" + part.value
}

func partStrings(parts []formPart) []string {
	result := make([]string, len(parts))
	for index, part := range parts {
		result[index] = part.String()
	}
	return result
}

func parseSubmission(contentType string, raw []byte) ([]formPart, error) {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	reader := multipart.NewReader(bytes.NewReader(raw), parameters["boundary"])
	var parts []formPart
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return parts, nil
		}
		if err != nil {
			return nil, err
		}
		if part.FileName() != "" {
			parts = append(parts, formPart{name: part.FormName(), file: true})
			continue
		}
		value, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		parts = append(parts, formPart{name: part.FormName(), value: string(value)})
	}
}

// rodinFake is the Hyper3D fake behind a recorder that keeps every Gen-2.5
// submission it received and the status of every exchange.
type rodinFake struct {
	t       *testing.T
	product *catalog.Catalog
	server  *httptest.Server

	mu        sync.Mutex
	forms     [][]formPart
	exchanges []exchange
}

func newRodinFake(t *testing.T) *rodinFake {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fake, err := fakeupstream.New(fakeupstream.Config{}, hyper3dapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	recorded := &rodinFake{t: t, product: product}
	recorded.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		if r.Method == http.MethodPost && r.URL.Path == hyper3dapiv2.Prefix+"/rodin" {
			raw, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		fake.ServeHTTP(recorder, r)
		recorded.mu.Lock()
		defer recorded.mu.Unlock()
		recorded.exchanges = append(recorded.exchanges, exchange{path: r.URL.Path, status: recorder.status})
		if raw != nil {
			parts, err := parseSubmission(r.Header.Get("Content-Type"), raw)
			if err != nil {
				t.Errorf("unreadable submission: %v", err)
			}
			recorded.forms = append(recorded.forms, parts)
		}
	}))
	t.Cleanup(recorded.server.Close)
	return recorded
}

func (f *rodinFake) port() *hyper3d.Adapter {
	f.t.Helper()
	for _, connection := range f.product.Application().Runtime.ProviderConnections {
		if connection.ID != "hyper3d" {
			continue
		}
		port, err := hyper3d.New(hyper3d.Config{
			BaseURL: f.server.URL + hyper3dapiv2.Prefix, APIKey: "synthetic-key",
			AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix,
			StatusMapping: connection.StatusMapping, ErrorMapping: connection.ErrorMapping,
			HTTPClient: f.server.Client(), AllowHTTPAssets: true, InputAssets: imageInputs{},
		})
		if err != nil {
			f.t.Fatal(err)
		}
		return port
	}
	f.t.Fatal("no hyper3d connection")
	return nil
}

// rodinInputJSON is a minimal valid input with the extra values merged in.
func rodinInputJSON(t *testing.T, id string, extra map[string]any) json.RawMessage {
	t.Helper()
	input := map[string]any{"prompt": "a small robot", "authorize_extra_credits": true}
	if strings.HasSuffix(id, "/image-to-3d") {
		input["reference_assets"] = []string{"view-0"}
	}
	for name, value := range extra {
		input[name] = value
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// start submits input to the Offering. A normalized input has passed the
// Schema; a raw one reaches the fake as given.
func (f *rodinFake) start(id string, input json.RawMessage, normalize bool) (generation.Observation, error) {
	f.t.Helper()
	offering, ok := f.product.Get(generation.OfferingID(id))
	if !ok {
		f.t.Fatalf("missing Offering %s", id)
	}
	if normalize {
		var err error
		if _, input, err = f.product.Normalize(offering.Capability, offering.ID, input); err != nil {
			f.t.Fatalf("%s: the Schema refused the input: %v", id, err)
		}
	}
	request := generation.StartRequest{
		OwnerID: "owner", TaskID: "task", AttemptID: "attempt", SubmissionKey: "once", Offering: offering, Input: input,
	}
	if offering.Capability == generation.ImageTo3D {
		request.InputAssets = map[string][]generation.InputAsset{"reference_assets": {
			{ID: "view-0", OwnerID: "owner", MediaType: "image/png", OriginalFilename: "view-0.png"},
		}}
	}
	return f.port().Start(context.Background(), request)
}

func (f *rodinFake) submissions() [][]formPart {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]formPart(nil), f.forms...)
}

func (f *rodinFake) only() []formPart {
	f.t.Helper()
	forms := f.submissions()
	if len(forms) != 1 {
		f.t.Fatalf("the fake received %d Gen-2.5 submissions, want 1", len(forms))
	}
	return forms[0]
}

var rodinOfferings = []string{"hyper3d/rodin-gen-2.5/text-to-3d", "hyper3d/rodin-gen-2.5/image-to-3d"}

// The display profile is unchanged by the new parameters: the request the
// shipped Offerings send by default is, part for part and in order, the one the
// fixed template sent. The expectation is the template of the Issue's evidence
// written out here, and the real recorded wire of the reviewed samples.
func TestRodinDisplayRequestIsTheRecordedWireOfTheOldTemplate(t *testing.T) {
	legacy := []string{
		"geometry_file_format=glb", "material=PBR", "mesh_mode=Raw", "quality=high", "quality_override=2000000",
		"texture_mode=extreme-high", "tier=Gen-2.5-Extreme-High",
	}
	for _, test := range []struct{ id, sample string }{
		{"hyper3d/rodin-gen-2.5/text-to-3d", "rodin-text-to-3d"},
		{"hyper3d/rodin-gen-2.5/image-to-3d", "rodin-image-to-3d"},
	} {
		t.Run(test.id, func(t *testing.T) {
			f := newRodinFake(t)
			exchanges, _ := loadRodinLiveSample(t, test.sample)
			var recorded []string
			prompt := ""
			for _, entry := range exchanges {
				if entry.Request.Method != http.MethodPost || entry.Request.Path != "/api/v2/rodin" {
					continue
				}
				for _, part := range entry.Request.Body.Multipart.Parts {
					switch {
					case part.Media != nil:
						recorded = append(recorded, part.Name+"=<file>")
					case part.Name == "prompt":
						prompt = part.Value
						recorded = append(recorded, "prompt="+part.Value)
					default:
						recorded = append(recorded, part.Name+"="+part.Value)
					}
				}
				break
			}
			if prompt == "" || len(recorded) < len(legacy)+1 {
				t.Fatalf("the sample holds no Rodin submission: %v", recorded)
			}

			if _, err := f.start(test.id, rodinInputJSON(t, test.id, map[string]any{"prompt": prompt}), true); err != nil {
				t.Fatal(err)
			}
			got := partStrings(f.only())
			want := append([]string(nil), legacy...)
			if strings.HasSuffix(test.id, "/image-to-3d") {
				want = append(want, "images=<file>")
			}
			want = append(want, "prompt="+prompt)
			if !slices.Equal(got, want) {
				t.Fatalf("display request = %v\nwant the old template's %v", got, want)
			}
			if !slices.Equal(got, recorded) {
				t.Fatalf("display request = %v\nrecorded real wire   = %v", got, recorded)
			}
			// Neither the new switches nor a seed are on the wire unless chosen.
			for _, part := range got {
				for _, name := range []string{"TAPose=", "quad_normal=", "seed="} {
					if strings.HasPrefix(part, name) {
						t.Fatalf("display request carries %s", part)
					}
				}
			}
		})
	}
}

// Quad, the face count, TAPose, quad_normal and seed reach the wire as the
// Schema values, once each, with the two template fields keeping their place;
// the fake (which enforces the documented ranges) accepts every combination the
// Schema lets through, including the shipped game purposes' overrides.
func TestRodinGameParametersReachTheWire(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	overrides := map[string]map[string]any{}
	for _, profile := range product.Application().Chat.Model3DUsage.Profiles {
		if profile.ID != "game_character" && profile.ID != "game_prop" {
			continue
		}
		for _, id := range rodinOfferings {
			decoded := map[string]any{}
			for name, raw := range profile.ParameterOverrides[id] {
				var value any
				if err := json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				decoded[name] = value
			}
			overrides[profile.ID+" "+id] = decoded
		}
	}
	// The game purposes take the 2K texture mode (the highest one that fits their
	// 2048 texture budget), written out here so the wire cases below cannot
	// follow an override that lost it.
	for key, decoded := range overrides {
		if decoded["texture_mode"] != "high" {
			t.Fatalf("%s must take the 2K texture mode: %v", key, decoded)
		}
	}
	for _, id := range rodinOfferings {
		cases := map[string]map[string]any{
			"game_character purpose":   overrides["game_character "+id],
			"game_prop purpose":        overrides["game_prop "+id],
			"every switch and a seed":  {"mesh_mode": "Quad", "quality_override": 10000, "TAPose": true, "quad_normal": true, "seed": 42},
			"the 2K texture alone":     {"texture_mode": "high"},
			"the display texture":      {"texture_mode": "extreme-high"},
			"Quad alone":               {"mesh_mode": "Quad"},
			"the Quad ceiling":         {"mesh_mode": "Quad", "quality_override": 200000},
			"a seed alone":             {"seed": 0},
			"Raw with a smaller count": {"quality_override": 60000},
			"the pose switch alone":    {"TAPose": true},
		}
		for name, values := range cases {
			t.Run(id+"/"+name, func(t *testing.T) {
				if len(values) == 0 {
					t.Fatal("no values for the case")
				}
				f := newRodinFake(t)
				started, err := f.start(id, rodinInputJSON(t, id, values), true)
				if err != nil || started.Job == nil {
					t.Fatalf("the fake refused a Schema-valid request: %+v %v", started, err)
				}
				got := f.only()
				seen := map[string]int{}
				for _, part := range got {
					seen[part.name]++
				}
				for name, count := range seen {
					if count != 1 {
						t.Fatalf("%s is on the wire %d times: %v", name, count, partStrings(got))
					}
				}
				value := func(name string) (string, bool) {
					for _, part := range got {
						if part.name == name {
							return part.value, true
						}
					}
					return "", false
				}
				mode, count, texture := "Raw", "2000000", "extreme-high"
				if v, ok := values["mesh_mode"]; ok {
					mode = v.(string)
				}
				if v, ok := values["quality_override"]; ok {
					count = formatWire(v)
				} else if mode == "Quad" {
					count = "200000" // the Schema default follows the Quad ceiling
				}
				if v, ok := values["texture_mode"]; ok {
					texture = v.(string)
				}
				for field, want := range map[string]string{"mesh_mode": mode, "quality_override": count, "texture_mode": texture} {
					if v, ok := value(field); !ok || v != want {
						t.Fatalf("%s = %q (%v), want %q in %v", field, v, ok, want, partStrings(got))
					}
				}
				for _, field := range []string{"TAPose", "quad_normal", "seed"} {
					want, chosen := values[field]
					v, sent := value(field)
					switch {
					case chosen && want == false:
						if sent {
							t.Fatalf("%s=false must stay off the wire: %v", field, partStrings(got))
						}
					case chosen:
						if !sent || v != formatWire(want) {
							t.Fatalf("%s = %q (%v), want %v in %v", field, v, sent, want, partStrings(got))
						}
					case sent:
						t.Fatalf("%s was not chosen but is on the wire: %v", field, partStrings(got))
					}
				}
				// The three template-bound fields keep their place between material
				// and tier (the recorded wire); the switches and the seed follow the
				// prompt.
				names := make([]string, len(got))
				for index, part := range got {
					names[index] = part.name
				}
				if !slices.Equal(names[:7], []string{
					"geometry_file_format", "material", "mesh_mode", "quality", "quality_override", "texture_mode", "tier",
				}) {
					t.Fatalf("template fields moved: %v", names)
				}
			})
		}
	}
}

func formatWire(value any) string {
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case string:
		return typed
	}
	return fmt.Sprint(value)
}

// A value the Schema would refuse still meets the fake's documented rules when
// it is sent as it is: the Quad ceiling and the seed range come back as the
// documented INVALID_REQUEST, which the connection maps to invalid_input, after
// exactly one create exchange (no polling, no replay).
func TestRodinFakeRejectsValuesTheDocumentationRulesOut(t *testing.T) {
	for _, id := range rodinOfferings {
		for name, extra := range map[string]map[string]any{
			"Quad above its ceiling":         {"mesh_mode": "Quad", "quality_override": 300000},
			"Quad with the display count":    {"mesh_mode": "Quad", "quality_override": 2000000},
			"a count below the lowest":       {"quality_override": 499},
			"a count above the highest":      {"quality_override": 2000001},
			"a seed above the range":         {"seed": 65536},
			"an unknown topology":            {"mesh_mode": "quad"},
			"an unknown texture mode":        {"texture_mode": "ultra"},
			"a switch that is not a boolean": {"TAPose": "yes"},
		} {
			t.Run(id+"/"+name, func(t *testing.T) {
				f := newRodinFake(t)
				_, err := f.start(id, rodinInputJSON(t, id, extra), false)
				var providerErr *generation.ProviderError
				if !errors.As(err, &providerErr) || providerErr.Failure == nil {
					t.Fatalf("the fake accepted %v: %v", extra, err)
				}
				failure := providerErr.Failure
				if failure.Class != generation.FailureInvalidInput || failure.UpstreamCode != "INVALID_REQUEST" || failure.Retryable || failure.Ambiguous {
					t.Fatalf("failure = %+v", failure)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.exchanges) != 1 || f.exchanges[0].status != http.StatusCreated {
					t.Fatalf("exchanges = %+v, want one HTTP 201 create", f.exchanges)
				}
			})
		}
	}
}

// A template field that a parameter takes over must keep the template's value as
// that parameter's default, and the parameter may not remap its value on the
// way out: otherwise the display request would no longer be the template's.
func TestRodinOfferingValidationKeepsTemplateAndParameterTogether(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range rodinOfferings {
		base, ok := product.Get(generation.OfferingID(id))
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if err := hyper3d.ValidateOffering(base); err != nil {
			t.Fatalf("%s: shipped Offering refused: %v", id, err)
		}
		change := func(edit func(parameters map[string]*generation.Parameter)) error {
			offering := base
			offering.Parameters = append([]generation.Parameter(nil), base.Parameters...)
			parameters := map[string]*generation.Parameter{}
			for index := range offering.Parameters {
				parameters[offering.Parameters[index].Name] = &offering.Parameters[index]
			}
			edit(parameters)
			return hyper3d.ValidateOffering(offering)
		}
		for name, test := range map[string]struct {
			edit func(map[string]*generation.Parameter)
			want string
		}{
			"a topology default that differs": {func(p map[string]*generation.Parameter) {
				p["mesh_mode"].Default = json.RawMessage(`"Quad"`)
			}, "mesh_mode"},
			"a count default that differs": {func(p map[string]*generation.Parameter) {
				p["quality_override"].Default = json.RawMessage(`1000000`)
			}, "quality_override"},
			"a remapped topology": {func(p map[string]*generation.Parameter) {
				p["mesh_mode"].WireValues = map[string]json.RawMessage{"Quad": json.RawMessage(`"Raw"`)}
			}, "wire_values"},
			"a texture default that differs": {func(p map[string]*generation.Parameter) {
				p["texture_mode"].Default = json.RawMessage(`"high"`)
			}, "texture_mode"},
			"a templated count": {func(p map[string]*generation.Parameter) {
				p["quality_override"].WireTemplate = json.RawMessage(`"$value"`)
			}, "wire_template"},
		} {
			if err := change(test.edit); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s %s: error = %v, want one naming %q", id, name, err, test.want)
			}
		}
		// A parameter without a default is fine: the template value is its fallback.
		if err := change(func(p map[string]*generation.Parameter) { p["quality_override"].Default = nil }); err != nil {
			t.Fatalf("%s: a parameter without a default was refused: %v", id, err)
		}
	}
}

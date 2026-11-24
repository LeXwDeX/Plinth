package schemaimages_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	geminiimagesv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/gemini/images-v1"
	jimengapiv3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/jimeng/api-v3"
	openaiimagesv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/images-v1"
)

// The 2D fakes enforce the documented parameter contract and draw pictures at
// the requested size (issues I17-I19). These tests drive the real Adapter and
// the shipped Offerings against them, so a request the Offerings can legally
// send is never refused by the stricter fake, and a request the Catalog would
// refuse is refused by the fake too: the Catalog and the fake read the same
// official numbers and may not drift apart.

type fakeStack struct {
	server *httptest.Server
}

func newFakeStack(t *testing.T) fakeStack {
	t.Helper()
	fake, err := fakeupstream.New(fakeupstream.Config{}, openaiimagesv1.Version(), geminiimagesv1.Version(), jimengapiv3.Version())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fakeStack{server: server}
}

func (s fakeStack) base(c imageConnection) string {
	if c.name == "jimeng" {
		return s.server.URL + "/jimeng"
	}
	return s.server.URL + c.prefix
}

// solidPNG is a real opaque PNG of the given size (the references an Offering
// sends must be readable pictures now).
func solidPNG(width, height int) []byte {
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 90, G: 140, B: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}

// holedMaskPNG is an alpha mask with a transparent (editable) middle square.
func holedMaskPNG(width, height int) []byte {
	mask := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x >= width/4 && x < width*3/4 && y >= height/4 && y < height*3/4 {
				continue
			}
			mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, mask); err != nil {
		panic(err)
	}
	return encoded.Bytes()
}

// startOffering sends one request through the Adapter to the fake and returns
// what it delivered, or the failure.
func (s fakeStack) startOffering(t *testing.T, c imageConnection, id generation.OfferingID, raw string, store officialAssetStore, inputs map[string][]generation.InputAsset) (generation.Observation, error) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, input := normalizedInput(t, product, id, raw)
	adapter, err := schemaimages.New(schemaimages.Config{
		BaseURL: s.base(c), APIKey: "test-key", HTTPClient: s.server.Client(),
		ErrorMapping: declaredImageMapping(t, c.connection), InputAssets: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter.Start(t.Context(), generation.StartRequest{
		TaskID: "task", AttemptID: "attempt", OwnerID: "owner", SubmissionKey: "submission",
		Offering: offering, Input: input, InputAssets: inputs,
	})
}

func pictureOf(t *testing.T, claim generation.ArtifactClaim) (image.Config, string, image.Image) {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(claim.Inline))
	if err != nil {
		t.Fatalf("the delivered picture does not decode: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(claim.Inline))
	if err != nil {
		t.Fatal(err)
	}
	return config, format, decoded
}

func references(id generation.AssetID, count int) []generation.InputAsset {
	out := make([]generation.InputAsset, count)
	for index := range out {
		out[index] = generation.InputAsset{ID: id, MediaType: "image/png"}
	}
	return out
}

func assetNames(id string, count int) string {
	names := make([]string, count)
	for index := range names {
		names[index] = fmt.Sprintf("%q", id)
	}
	return "[" + strings.Join(names, ",") + "]"
}

// I17: through the shipped Offerings, each picture arrives at the size the
// request named, not at a placeholder's.
func TestOfferingsDeliverThePictureAtTheRequestedSize(t *testing.T) {
	stack := newFakeStack(t)
	openai, gemini, jimeng := imageConnections[0], imageConnections[1], imageConnections[2]
	store := officialAssetStore{"ref": solidPNG(32, 32), "mask": holedMaskPNG(32, 32)}
	for _, tc := range []struct {
		name          string
		c             imageConnection
		offering      generation.OfferingID
		input         string
		inputs        map[string][]generation.InputAsset
		count         int
		format        string
		width, height int
	}{
		{"openai default", openai, "openai/gpt-image-2/text-to-image", `{"prompt":"a cube"}`, nil, 1, "png", 2048, 2048},
		{"openai 3:2", openai, "openai/gpt-image-2/text-to-image", `{"prompt":"a cube","size":"1536x1024"}`, nil, 1, "png", 1536, 1024},
		{"openai wide 4K", openai, "openai/gpt-image-2/text-to-image", `{"prompt":"a cube","size":"3840x2160","quality":"low"}`, nil, 1, "png", 3840, 2160},
		{"openai n=3", openai, "openai/gpt-image-2/text-to-image", `{"prompt":"a cube","size":"1024x1024","n":3}`, nil, 3, "png", 1024, 1024},
		{"openai edit", openai, "openai/gpt-image-2/image-to-image", `{"prompt":"edit","reference_assets":["ref","ref"],"size":"1536x1024"}`,
			map[string][]generation.InputAsset{"reference_assets": references("ref", 2)}, 1, "png", 1536, 1024},
		{"gemini default", gemini, "gemini/gemini-3-pro-image/text-to-image", `{"prompt":"a cube"}`, nil, 1, "jpeg", 4096, 4096},
		{"gemini 16:9 2K", gemini, "gemini/gemini-3-pro-image/text-to-image", `{"prompt":"a cube","aspect_ratio":"16:9","image_size":"2K"}`, nil, 1, "jpeg", 2752, 1536},
		{"gemini edit", gemini, "gemini/gemini-3-pro-image/image-to-image", `{"prompt":"edit","reference_assets":["ref","ref","ref"],"aspect_ratio":"3:4","image_size":"1K"}`,
			map[string][]generation.InputAsset{"reference_assets": references("ref", 3)}, 1, "png", 896, 1200},
		{"jimeng default", jimeng, "jimeng/seedream-4-5/text-to-image", `{"prompt":"a cube"}`, nil, 1, "jpeg", 4096, 4096},
		{"jimeng wide", jimeng, "jimeng/seedream-4-5/text-to-image", `{"prompt":"a cube","size":"5504x3040"}`, nil, 1, "jpeg", 5504, 3040},
		// The Offering maps the preset 2560x1440 to its 4K-class twin 5504x3040.
		{"jimeng group", jimeng, "jimeng/seedream-4-5/text-to-image", `{"prompt":"a cube","size":"2560x1440","sequential_image_generation":"auto","max_images":2}`, nil, 2, "jpeg", 5504, 3040},
		{"jimeng edit", jimeng, "jimeng/seedream-4-5/image-to-image", `{"prompt":"edit","reference_assets":["ref","ref"],"size":"4704x3520"}`,
			map[string][]generation.InputAsset{"reference_assets": references("ref", 2)}, 1, "jpeg", 4704, 3520},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation, err := stack.startOffering(t, tc.c, tc.offering, tc.input, store, tc.inputs)
			if err != nil {
				t.Fatalf("the fake refused a request the Offering sends: %v", err)
			}
			if len(observation.Artifacts) != tc.count {
				t.Fatalf("delivered %d pictures, want %d", len(observation.Artifacts), tc.count)
			}
			for index, claim := range observation.Artifacts {
				config, format, _ := pictureOf(t, claim)
				if format != tc.format || config.Width != tc.width || config.Height != tc.height {
					t.Fatalf("picture %d is %s %dx%d, want %s %dx%d", index, format, config.Width, config.Height, tc.format, tc.width, tc.height)
				}
			}
		})
	}
}

// I19: background=transparent reaches the fake as written and comes back as an
// alpha PNG; a masked edit keeps the picture's pixels outside the mask.
func TestOpenAIOfferingsDeliverTransparencyAndMaskedEdits(t *testing.T) {
	stack := newFakeStack(t)
	openai := imageConnections[0]
	store := officialAssetStore{"ref": solidPNG(32, 32), "mask": holedMaskPNG(32, 32)}
	observation, err := stack.startOffering(t, openai, "openai/gpt-image-2/text-to-image",
		`{"prompt":"a cube","size":"1024x1024","background":"transparent"}`, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	config, format, decoded := pictureOf(t, observation.Artifacts[0])
	if format != "png" || config.Width != 1024 {
		t.Fatalf("picture = %s %v", format, config)
	}
	if _, _, _, alpha := decoded.At(2, 2).RGBA(); alpha != 0 {
		t.Fatalf("a transparent background has alpha %d", alpha)
	}
	// The default background stays opaque.
	observation, err = stack.startOffering(t, openai, "openai/gpt-image-2/text-to-image", `{"prompt":"a cube","size":"1024x1024"}`, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, decoded = pictureOf(t, observation.Artifacts[0]); decoded == nil {
		t.Fatal("no picture")
	} else if _, _, _, alpha := decoded.At(2, 2).RGBA(); alpha != 0xffff {
		t.Fatalf("an opaque background has alpha %d", alpha)
	}
	// A masked edit: Plinth sends the mask beside the references.
	observation, err = stack.startOffering(t, openai, "openai/gpt-image-2/image-to-image",
		`{"prompt":"edit","reference_assets":["ref"],"mask":"mask","n":2}`, store, map[string][]generation.InputAsset{
			"reference_assets": references("ref", 1), "mask": {{ID: "mask", MediaType: "image/png"}},
		})
	if err != nil {
		t.Fatalf("the fake refused the masked edit the Offering sends: %v", err)
	}
	if len(observation.Artifacts) != 2 {
		t.Fatalf("delivered %d candidates, want 2", len(observation.Artifacts))
	}
	for index, claim := range observation.Artifacts {
		config, _, decoded := pictureOf(t, claim)
		if config.Width != 32 || config.Height != 32 {
			t.Fatalf("candidate %d is %dx%d, want the picture's 32x32", index, config.Width, config.Height)
		}
		got := color.NRGBAModel.Convert(decoded.At(1, 1)).(color.NRGBA)
		if got.R != 90 || got.G != 140 || got.B != 200 {
			t.Fatalf("candidate %d changed a pixel outside the mask: %+v", index, got)
		}
	}
	if bytes.Equal(observation.Artifacts[0].Inline, observation.Artifacts[1].Inline) {
		t.Fatal("the two masked candidates are identical")
	}
}

// I18: a reference list at the Offering's limit is accepted by the fake, one
// over it is refused, and the limit the Offering declares is the official one.
func TestFakeReferenceLimitsMatchTheOfferings(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	stack := newFakeStack(t)
	store := officialAssetStore{"ref": solidPNG(32, 32)}
	for _, tc := range []struct {
		c        imageConnection
		offering generation.OfferingID
		limit    int
	}{
		{imageConnections[0], "openai/gpt-image-2/image-to-image", 16},
		{imageConnections[1], "gemini/gemini-3-pro-image/image-to-image", 14},
		{imageConnections[2], "jimeng/seedream-4-5/image-to-image", 14},
	} {
		t.Run(string(tc.offering), func(t *testing.T) {
			offering := officialOffering(t, product, tc.offering)
			declared := 0
			for _, parameter := range offering.Parameters {
				if parameter.Name == "reference_assets" {
					declared = parameter.MaxItems
				}
			}
			if declared != tc.limit {
				t.Fatalf("the Offering declares %d references, the official limit is %d", declared, tc.limit)
			}
			raw := fmt.Sprintf(`{"prompt":"edit","reference_assets":%s}`, assetNames("ref", tc.limit))
			if _, err := stack.startOffering(t, tc.c, tc.offering, raw, store,
				map[string][]generation.InputAsset{"reference_assets": references("ref", tc.limit)}); err != nil {
				t.Fatalf("%d references (the Offering's limit): %v", tc.limit, err)
			}
			// The fake refuses one more, though Plinth would never send it: lift
			// the Offering's own bound on a copy to get the extra picture past it.
			offeringCopy := offering
			offeringCopy.Parameters = append([]generation.Parameter(nil), offering.Parameters...)
			for index := range offeringCopy.Parameters {
				if offeringCopy.Parameters[index].Name == "reference_assets" {
					offeringCopy.Parameters[index].MaxItems = tc.limit + 1
				}
			}
			adapter, err := schemaimages.New(schemaimages.Config{
				BaseURL: stack.base(tc.c), APIKey: "test-key", HTTPClient: stack.server.Client(),
				ErrorMapping: declaredImageMapping(t, tc.c.connection), InputAssets: store,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, input := normalizedInput(t, product, tc.offering, fmt.Sprintf(`{"prompt":"edit","reference_assets":%s}`, assetNames("ref", tc.limit)))
			_, startErr := adapter.Start(t.Context(), generation.StartRequest{
				TaskID: "task", AttemptID: "attempt", OwnerID: "owner", SubmissionKey: "submission",
				Offering: offeringCopy, Input: input,
				InputAssets: map[string][]generation.InputAsset{"reference_assets": references("ref", tc.limit+1)},
			})
			if startErr == nil {
				t.Fatalf("the fake accepted %d references", tc.limit+1)
			}
		})
	}
}

// The Catalog's custom-size rule and the fake's are the same rule: on a grid of
// sizes around every bound, the fake answers 200 exactly where the Catalog
// accepts the size.
func TestFakeSizeRulesAgreeWithTheCatalog(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	stack := newFakeStack(t)
	edges := []int{1, 16, 480, 512, 624, 640, 720, 1008, 1024, 1280, 1440, 1536, 1920, 2048, 2560, 3000, 3840, 3856, 4000, 4096, 4097, 4112, 8192}
	var grid [][2]int
	for _, width := range edges {
		for _, height := range edges {
			grid = append(grid, [2]int{width, height})
		}
	}
	// A size the Catalog accepts is drawn at full size by the fake, and Seedream
	// sizes reach 16 million pixels, so its probes sit right at each bound
	// instead of covering a grid.
	seedreamBounds := [][2]int{
		{2560, 1440}, {2559, 1440}, {1920, 1920}, {1919, 1920}, {4096, 4096}, {4097, 4096}, {16384, 1024}, {16400, 1024},
		{8192, 512}, {8208, 512}, {512, 8192}, {512, 8208}, {3750, 1250}, {1000, 1000}, {9000, 500}, {1, 1}, {2048, 2048}, {1024, 3600},
	}
	for _, tc := range []struct {
		c        imageConnection
		offering generation.OfferingID
		sizes    [][2]int
		request  func(size string) map[string]any
	}{
		{imageConnections[0], "openai/gpt-image-2/text-to-image", grid, func(size string) map[string]any {
			return map[string]any{"model": openaiimagesv1.Model, "prompt": "x", "size": size, "quality": "low"}
		}},
		{imageConnections[2], "jimeng/seedream-4-5/text-to-image", seedreamBounds, func(size string) map[string]any {
			return map[string]any{"model": jimengapiv3.Model, "prompt": "x", "size": size, "response_format": "b64_json"}
		}},
	} {
		t.Run(tc.c.name, func(t *testing.T) {
			var constraint *generation.ImageSizeConstraint
			for _, parameter := range officialOffering(t, product, tc.offering).Parameters {
				if parameter.Name == "size" {
					constraint = parameter.ImageSize
				}
			}
			if constraint == nil {
				t.Fatal("the Offering declares no custom size constraint")
			}
			endpoint := stack.base(tc.c) + "/images/generations"
			if tc.c.name == "jimeng" {
				endpoint = stack.base(tc.c) + "/api/v3/images/generations"
			}
			accepted := 0
			for _, pair := range tc.sizes {
				width, height := pair[0], pair[1]
				size := fmt.Sprintf("%dx%d", width, height)
				want := constraint.Check(int64(width), int64(height)) == nil
				body, _ := json.Marshal(tc.request(size))
				request, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer test-key")
				response, err := stack.server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				if got := response.StatusCode == http.StatusOK; got != want {
					t.Errorf("size %s: the fake answers %d, the Catalog accepts it = %v", size, response.StatusCode, want)
				}
				if want {
					accepted++
				}
			}
			if accepted == 0 {
				t.Fatal("the grid contains no size the Catalog accepts")
			}
		})
	}
}

// Every size option the Offerings list is a size the fake draws, and every
// documented Gemini option is accepted.
func TestFakeAcceptsEveryOfferingSizeOption(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	stack := newFakeStack(t)
	for _, tc := range []struct {
		c        imageConnection
		offering generation.OfferingID
		param    string
		build    func(value string) string
	}{
		{imageConnections[1], "gemini/gemini-3-pro-image/text-to-image", "aspect_ratio", func(v string) string { return fmt.Sprintf(`{"prompt":"x","aspect_ratio":%q,"image_size":"1K"}`, v) }},
		{imageConnections[1], "gemini/gemini-3-pro-image/text-to-image", "image_size", func(v string) string { return fmt.Sprintf(`{"prompt":"x","image_size":%q}`, v) }},
		{imageConnections[2], "jimeng/seedream-4-5/text-to-image", "size", func(v string) string { return fmt.Sprintf(`{"prompt":"x","size":%q}`, v) }},
	} {
		offering := officialOffering(t, product, tc.offering)
		for _, parameter := range offering.Parameters {
			if parameter.Name != tc.param {
				continue
			}
			if len(parameter.Options) == 0 {
				t.Fatalf("%s %s declares no options", tc.offering, tc.param)
			}
			for _, option := range parameter.Options {
				value := fmt.Sprint(option.Value)
				if tc.param == "image_size" && value == "4K" {
					continue // 4096x4096 is drawn by the default-size test; keep this one light
				}
				t.Run(string(tc.offering)+"/"+tc.param+"/"+value, func(t *testing.T) {
					if _, err := stack.startOffering(t, tc.c, tc.offering, tc.build(value), officialAssetStore{}, nil); err != nil {
						t.Fatalf("the fake refused the Offering's own option: %v", err)
					}
				})
			}
		}
	}
}

// I20: every code the Catalog maps for the Jimeng connection can be produced by
// the fake with the code intact, so each mapped code has a regression path.
func TestFakeProducesEveryCodeTheJimengCatalogMaps(t *testing.T) {
	stack := newFakeStack(t)
	mapping := declaredImageMapping(t, "jimeng-images")
	jimeng := imageConnections[2]
	for code := range mapping.UpstreamCodeMap {
		t.Run(code, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"model": jimengapiv3.Model, "prompt": "x", "size": "2048x2048"})
			request, _ := http.NewRequest(http.MethodPost, stack.base(jimeng)+"/api/v3/images/generations", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer test-key")
			request.Header.Set(jimengapiv3.UpstreamCodeHeader, code)
			response, err := stack.server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var decoded struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Error.Code != code {
				t.Fatalf("the fake answered code %q (HTTP %d) for the mapped code %q", decoded.Error.Code, response.StatusCode, code)
			}
		})
	}
}

// I20: Ark's newest 429, a request burst protection, reads as a retryable rate
// limit, and the model-access codes as the upstream denying service.
func TestJimengConnectionReadsTheBurstAndModelAccessCodes(t *testing.T) {
	jimeng := imageConnections[2]
	body := func(code string) string {
		return fmt.Sprintf(`{"error":{"code":%q,"message":"m","param":"","type":"t"}}`, code)
	}
	runStubbedAnswers(t, jimeng, func(url string) string { return url }, []stubbedAnswer{
		{"RequestBurstTooFast 429", 429, body("RequestBurstTooFast"), generation.FailureRateLimited, true},
		{"UnsupportedModel 404", 404, body("UnsupportedModel"), generation.FailureUpstreamDenied, false},
		{"ModelAccountIpmRateLimitExceeded 429", 429, body("ModelAccountIpmRateLimitExceeded"), generation.FailureRateLimited, true},
	})
	mapping := declaredImageMapping(t, "jimeng-images")
	if rule, ok := mapping.UpstreamCodeMap["RequestBurstTooFast"]; !ok || rule.Class != string(generation.FailureRateLimited) || !rule.Retryable {
		t.Fatalf("RequestBurstTooFast = %+v, want a declared retryable rate limit", rule)
	}
}

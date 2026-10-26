package meshy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var han = regexp.MustCompile(`\p{Han}`)

func imageFixture(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func imageRequest(raw []byte) generation.InputValidationRequest {
	p := schema.Parameter{Name: "source_image", Type: "asset", Required: true, WireName: "image_url", MaxBytes: 20000000, MediaTypes: []string{"image/png", "image/jpeg", "image/webp"}, ImageInput: &schema.ImageInputConstraint{MinWidth: 32, MinHeight: 32, MaxPixels: 178956970}}
	o := generation.Offering{ProviderID: "meshy", Capability: "creative-lab-prototype", Parameters: []schema.Parameter{p}, ProviderOptions: json.RawMessage(`{"endpoint":"keychain/v1/prototype","api_version":"creative-lab","task_type":"creative-lab-keychain-prototype","inline_model_input":{"max_bytes":20000000},"result_assets":[{"path":"image_urls","role":"image","mode":"array"}]}`)}
	digest := sha256.Sum256(raw)
	asset := generation.InputAsset{ID: "owned-image", OwnerID: "owner", StorageKey: "source", MediaType: "image/png", Size: int64(len(raw)), SHA256: hex.EncodeToString(digest[:])}
	return generation.InputValidationRequest{OwnerID: "owner", Offering: o, Input: json.RawMessage(`{"source_image":"owned-image"}`), InputAssets: map[string][]generation.InputAsset{"source_image": {asset}}}
}

func TestImageInputValidatesDimensionsIdentityAndFrozenBytes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		mutate        func(*generation.InputValidationRequest)
		valid         bool
	}{
		{"minimum", 32, 32, nil, true}, {"wide", 64, 32, nil, true}, {"too-narrow", 31, 32, nil, false}, {"too-short", 32, 31, nil, false},
		{"pixel-limit", 33, 32, func(r *generation.InputValidationRequest) { r.Offering.Parameters[0].ImageInput.MaxPixels = 1024 }, false},
		{"foreign-owner", 32, 32, func(r *generation.InputValidationRequest) { r.OwnerID = "foreign" }, false},
		{"wrong-id", 32, 32, func(r *generation.InputValidationRequest) { r.Input = json.RawMessage(`{"source_image":"other"}`) }, false},
		{"wrong-mime", 32, 32, func(r *generation.InputValidationRequest) { r.InputAssets["source_image"][0].MediaType = "image/jpeg" }, false},
		{"wrong-digest", 32, 32, func(r *generation.InputValidationRequest) {
			r.InputAssets["source_image"][0].SHA256 = strings.Repeat("0", 64)
		}, false},
		{"wrong-size", 32, 32, func(r *generation.InputValidationRequest) { r.InputAssets["source_image"][0].Size++ }, false},
		{"too-large", 32, 32, func(r *generation.InputValidationRequest) { r.Offering.Parameters[0].MaxBytes = 1 }, false},
		{"invalid-contract", 32, 32, func(r *generation.InputValidationRequest) { r.Offering.Parameters[0].ImageInput.MinWidth = 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := imageFixture(t, tc.width, tc.height)
			request := imageRequest(raw)
			if tc.mutate != nil {
				tc.mutate(&request)
			}
			a := &Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}
			if err := a.ValidateInput(context.Background(), request); (err == nil) != tc.valid {
				t.Fatalf("err=%v valid=%v", err, tc.valid)
			}
		})
	}
}

func TestImageInputStartSendsTheValidatedBytesAndAccepts202(t *testing.T) {
	raw := imageFixture(t, 32, 32)
	request := imageRequest(raw)
	inputs := &profiledInputs{bodies: [][]byte{raw}}
	calls := 0
	a, err := New(Config{BaseURL: "https://meshy.invalid", APIKey: "fixture-key", InputAssets: inputs, HTTPClient: &http.Client{Transport: profileTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/openapi/creative-lab/keychain/v1/prototype" {
			t.Fatalf("wrong request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["image_url"] != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(raw) {
			t.Fatalf("wrong source bytes/body: %#v", body)
		}
		return &http.Response{StatusCode: 202, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"result":"created-prototype"}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Start(context.Background(), generation.StartRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets, SubmissionKey: "one-create"})
	if err != nil || got.Job == nil || got.Job.RemoteID != "created-prototype" || calls != 1 || inputs.opens != 1 {
		t.Fatalf("got=%+v err=%v calls=%d reads=%d", got, err, calls, inputs.opens)
	}
}

func TestImageInputMutationAfterPrepareStopsBeforePost(t *testing.T) {
	raw := imageFixture(t, 32, 32)
	changed := imageFixture(t, 33, 32)
	request := imageRequest(raw)
	a := &Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw, changed}}}
	if err := a.ValidateInput(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	// No HTTP client: any accidental dispatch would panic.
	_, err := a.Start(context.Background(), generation.StartRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets})
	if err == nil {
		t.Fatal("changed bytes accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.ValidateInput(cancelled, request); err != context.Canceled {
		t.Fatalf("cancel err=%v", err)
	}
}

// #358: a reachable synchronous input refusal carries the Offering's Schema
// copy, keeps the size/dimension values, and leaves the original diagnostic
// in Cause. English output has no Chinese.
func TestImageInputRefusalUsesSchemaCopy(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	raw := imageFixture(t, 32, 32)
	for _, test := range []struct {
		name    string
		mutate  func(*generation.InputValidationRequest)
		reason  schema.InputRefusalReason
		english []string
	}{
		{"too large", func(r *generation.InputValidationRequest) { r.Offering.Parameters[0].MaxBytes = int64(len(raw)) - 1 },
			schema.InputRefusalTooLarge, []string{"The source file is"}},
		{"media type", func(r *generation.InputValidationRequest) {
			r.InputAssets["source_image"][0].MediaType = "image/gif"
		}, schema.InputRefusalMediaType, []string{"image/gif", "image/png"}},
		{"dimensions", func(r *generation.InputValidationRequest) { r.Offering.Parameters[0].ImageInput.MinWidth = 64 },
			schema.InputRefusalDimensions, []string{"32×32", "64×32"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := imageRequest(raw)
			request.Offering.ExecutionMessages = application.Runtime.GenerationMessages
			test.mutate(&request)
			err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), request)
			refusal := generation.UserFacingError(err)
			if refusal == nil || !refusal.UserFacing {
				t.Fatalf("error = %v", err)
			}
			if refusal.DetailTemplate == "" {
				t.Fatal("refusal has no Schema template")
			}
			if refusal.Cause == nil || refusal.Cause.Error() != imageInputDiagnostic.Error() {
				t.Fatalf("Cause = %v", refusal.Cause)
			}
			english := refusal.LocalizedDetail(func(text string) string { return texts.Text("en", text) })
			if han.MatchString(english) {
				t.Fatalf("English refusal still has Chinese: %q", english)
			}
			for _, value := range test.english {
				if !strings.Contains(english, value) {
					t.Fatalf("English refusal %q lost %q", english, value)
				}
			}
			if request.Input == nil || string(request.Input) != `{"source_image":"owned-image"}` {
				t.Fatalf("input changed: %s", request.Input)
			}
		})
	}
}

// #358: a limit is rounded down and the refused size up, so an oversized file
// never reads as equal to its limit.
func TestImageInputRefusalRoundsLimitDown(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw := imageFixture(t, 32, 32)
	request := imageRequest(raw)
	request.Offering.ExecutionMessages = product.Application().Runtime.GenerationMessages
	request.InputAssets["source_image"][0].Size = 10_485_761
	request.Offering.Parameters[0].MaxBytes = 10_485_760
	err = (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), request)
	refusal := generation.UserFacingError(err)
	if refusal == nil {
		t.Fatalf("error = %v", err)
	}
	if len(refusal.DetailValues) != 4 || refusal.DetailValues[1] != "10.49 MB" ||
		refusal.DetailValues[3] != "10.48 MB" {
		t.Fatalf("values = %v", refusal.DetailValues)
	}
}

// #358: changed frozen bytes remain an integrity conflict (409), not a new
// input (400).
func TestImageInputMutationKeepsConflict(t *testing.T) {
	raw := imageFixture(t, 32, 32)
	changed := append(append([]byte(nil), raw...), 0)
	request := imageRequest(raw)
	a := &Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw, changed}}}
	if err := a.ValidateInput(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	err := a.ValidateInput(context.Background(), request)
	if !generation.IsCode(err, generation.ErrConflict) {
		t.Fatalf("error = %v", err)
	}
	if refusal := generation.UserFacingError(err); refusal != nil {
		t.Fatalf("integrity conflict became a user input refusal: %+v", refusal)
	}
}

// #358: an invalid frozen image_input contract is a server declaration error,
// not a user source refusal.
func TestImageInputContractFailureStaysInternal(t *testing.T) {
	raw := imageFixture(t, 32, 32)
	request := imageRequest(raw)
	request.Offering.Parameters[0].ImageInput.MinWidth = 0
	err := (&Adapter{inputAssets: &profiledInputs{bodies: [][]byte{raw}}}).ValidateInput(context.Background(), request)
	if !generation.IsCode(err, generation.ErrUnavailable) || generation.UserFacingError(err) != nil {
		t.Fatalf("error = %v", err)
	}
}

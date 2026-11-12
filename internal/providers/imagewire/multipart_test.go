package imagewire_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
	"github.com/LeXwDeX/Plinth/internal/providers/uploadname"
)

type fixtureAssets struct {
	data []byte
	err  error
	urls []string
}

func (assets *fixtureAssets) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	return nil, nil
}

func (assets *fixtureAssets) Open(_ context.Context, asset generation.InputAsset) (io.ReadCloser, error) {
	assets.urls = append(assets.urls, string(asset.ID))
	if assets.err != nil {
		return nil, assets.err
	}
	return io.NopCloser(bytes.NewReader(assets.data)), nil
}

func multipartOffering() generation.Offering {
	protocol := generation.ImageProtocol{
		Endpoint:        "images/edits",
		RequestEncoding: "multipart",
		RequestTemplate: json.RawMessage(`{"model":"$model"}`),
	}
	encoded, _ := json.Marshal(protocol)
	return generation.Offering{
		ID:              "configured/offering",
		ModelID:         "configured-model",
		ProviderOptions: encoded,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "text"},
			{Name: "n", Type: "number"},
			{Name: "quality", Type: "select", OmitWhen: []generation.ParameterCondition{{
				Parameter: "n",
				Operator:  "equals",
				Value:     json.RawMessage(`0`),
			}}},
			{Name: "reference_assets", Type: "asset-array", WireName: "image[]", MaxBytes: 64},
			{Name: "mask", Type: "asset", WireName: "mask", MaxBytes: 64},
		},
	}
}

func TestEncodeMultipartWritesDeclaredFieldsAndAssetParts(t *testing.T) {
	assets := &fixtureAssets{data: []byte("\x89PNG-fixture")}
	request := generation.StartRequest{
		Offering: multipartOffering(),
		Input:    json.RawMessage(`{"prompt":"edit","n":2,"quality":"xhigh","reference_assets":["a"],"mask":"b"}`),
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{ID: "a", MediaType: "image/png", OriginalFilename: "reference.png"}},
			"mask":             {{ID: "b", MediaType: "image/png"}},
		},
	}
	reader, contentType, err := imagewire.EncodeMultipart(context.Background(), request, assets)
	if err != nil {
		t.Fatal(err)
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		t.Fatalf("content type = %q (%v)", contentType, err)
	}
	form, err := multipart.NewReader(reader, parameters["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	for field, expected := range map[string]string{
		"model":   "configured-model",
		"prompt":  "edit",
		"n":       "2",
		"quality": "xhigh",
	} {
		if form.Value[field] == nil || form.Value[field][0] != expected {
			t.Errorf("field %s = %#v", field, form.Value[field])
		}
	}
	if len(form.File["image[]"]) != 1 || len(form.File["mask"]) != 1 {
		t.Fatalf("file parts = %#v", form.File)
	}
	reference := form.File["image[]"][0]
	if reference.Filename != "reference.png" ||
		reference.Header.Get("Content-Type") != "image/png" {
		t.Errorf("reference part = %#v", reference.Header)
	}
	if mask := form.File["mask"][0]; mask.Filename != "upload.png" {
		t.Errorf("mask filename = %q", mask.Filename)
	}
	if len(assets.urls) != 2 || assets.urls[0] != "a" || assets.urls[1] != "b" {
		t.Errorf("opened assets = %#v", assets.urls)
	}
}

func TestEncodeMultipartOmitsConditionalParameters(t *testing.T) {
	request := generation.StartRequest{
		Offering: multipartOffering(),
		Input:    json.RawMessage(`{"prompt":"edit","n":0,"quality":"xhigh"}`),
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{ID: "a", MediaType: "image/png"}},
		},
	}
	reader, contentType, err := imagewire.EncodeMultipart(context.Background(), request, &fixtureAssets{})
	if err != nil {
		t.Fatal(err)
	}
	_, parameters, _ := mime.ParseMediaType(contentType)
	form, err := multipart.NewReader(reader, parameters["boundary"]).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if form.Value["quality"] != nil {
		t.Errorf("omitted parameter leaked: %#v", form.Value)
	}
}

// partOrder lists the parts of an encoded multipart body in wire order as
// "name=value" (a file part as "name=<file>").
func partOrder(t *testing.T, reader io.Reader, contentType string) []string {
	t.Helper()
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	parts := multipart.NewReader(reader, parameters["boundary"])
	var order []string
	for {
		part, err := parts.NextPart()
		if err == io.EOF {
			return order
		}
		if err != nil {
			t.Fatal(err)
		}
		if part.FileName() != "" {
			order = append(order, part.FormName()+"=<file>")
			continue
		}
		value, _ := io.ReadAll(part)
		order = append(order, part.FormName()+"="+string(value))
	}
}

// A parameter whose wire name is a request_template field replaces the
// template value in place (#279): the Rodin Schema turns two display constants
// into parameters and the request must keep the field order of the recorded
// wire. The template value stays the fallback while the parameter is absent or
// omitted, and the other parameters still follow the template.
func TestEncodeMultipartParameterTakesItsTemplateFieldPlace(t *testing.T) {
	offering := func(changes ...func(*generation.Offering)) generation.Offering {
		protocol := generation.ImageProtocol{
			Endpoint:        "rodin",
			RequestEncoding: "multipart",
			RequestTemplate: json.RawMessage(`{"tier":"fixed","mesh_mode":"Raw","count":2000000,"flag":"template"}`),
		}
		encoded, _ := json.Marshal(protocol)
		result := generation.Offering{
			ID:              "configured/offering",
			ModelID:         "configured-model",
			ProviderOptions: encoded,
			Parameters: []generation.Parameter{
				{Name: "prompt", Type: "text"},
				{Name: "mesh_mode", Type: "select", Default: json.RawMessage(`"Raw"`)},
				{Name: "count", Type: "number", Default: json.RawMessage(`2000000`), OmitWhen: []generation.ParameterCondition{{
					Parameter: "mesh_mode", Operator: "equals", Value: json.RawMessage(`"Skip"`),
				}}},
				{Name: "seed", Type: "number"},
				{Name: "shape", Type: "select", WireName: "flag",
					WireValues: map[string]json.RawMessage{"round": json.RawMessage(`"mapped-round"`)}},
			},
		}
		for _, change := range changes {
			change(&result)
		}
		return result
	}
	encode := func(t *testing.T, offering generation.Offering, input string) []string {
		t.Helper()
		reader, contentType, err := imagewire.EncodeMultipart(context.Background(), generation.StartRequest{
			Offering: offering, Input: json.RawMessage(input),
		}, &fixtureAssets{})
		if err != nil {
			t.Fatal(err)
		}
		return partOrder(t, reader, contentType)
	}
	for name, test := range map[string]struct {
		input string
		want  []string
	}{
		"parameters replace the template values where the template had them": {
			`{"prompt":"p","mesh_mode":"Quad","count":10000,"seed":7}`,
			[]string{"count=10000", "flag=template", "mesh_mode=Quad", "tier=fixed", "prompt=p", "seed=7"},
		},
		"the display defaults keep the template's request unchanged": {
			`{"prompt":"p","mesh_mode":"Raw","count":2000000}`,
			[]string{"count=2000000", "flag=template", "mesh_mode=Raw", "tier=fixed", "prompt=p"},
		},
		"an absent parameter leaves the template value": {
			`{"prompt":"p"}`,
			[]string{"count=2000000", "flag=template", "mesh_mode=Raw", "tier=fixed", "prompt=p"},
		},
		"an omitted parameter leaves the template value": {
			`{"prompt":"p","mesh_mode":"Skip","count":10000}`,
			[]string{"count=2000000", "flag=template", "mesh_mode=Skip", "tier=fixed", "prompt=p"},
		},
		"the wire mapping of the parameter applies in place": {
			`{"prompt":"p","shape":"round"}`,
			[]string{"count=2000000", "flag=mapped-round", "mesh_mode=Raw", "tier=fixed", "prompt=p"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := encode(t, offering(), test.input); !equalStrings(got, test.want) {
				t.Fatalf("parts = %v, want %v", got, test.want)
			}
		})
	}
	t.Run("a template without a same-named parameter keeps the old order", func(t *testing.T) {
		plain := offering(func(o *generation.Offering) { o.Parameters = o.Parameters[:1] })
		want := []string{"count=2000000", "flag=template", "mesh_mode=Raw", "tier=fixed", "prompt=p"}
		if got := encode(t, plain, `{"prompt":"p"}`); !equalStrings(got, want) {
			t.Fatalf("parts = %v, want %v", got, want)
		}
	})
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestEncodeMultipartEnforcesAssetLimitsAndLocalFailures(t *testing.T) {
	offering := multipartOffering()
	offering.Parameters[len(offering.Parameters)-1].MaxBytes = 4
	request := generation.StartRequest{
		Offering: offering,
		Input:    json.RawMessage(`{"prompt":"edit","reference_assets":["a"],"mask":"b"}`),
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{ID: "a", MediaType: "image/png"}},
			"mask":             {{ID: "b", MediaType: "image/png"}},
		},
	}
	reader, _, err := imagewire.EncodeMultipart(context.Background(), request, &fixtureAssets{data: []byte("12345")})
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(reader)
	if readErr == nil || !strings.Contains(readErr.Error(), "超过大小限制") {
		t.Fatalf("limit error = %v", readErr)
	}

	failing := &fixtureAssets{err: io.ErrUnexpectedEOF}
	reader, _, err = imagewire.EncodeMultipart(context.Background(), request, failing)
	if err != nil {
		t.Fatal(err)
	}
	if _, readErr := io.ReadAll(reader); !bytes.Contains([]byte(readErr.Error()), []byte("unexpected EOF")) {
		t.Fatalf("asset failure = %v", readErr)
	}
}

// A part without a reliable extension is refused while the body is built,
// before any asset is opened or any byte can reach an upstream.
func TestEncodeMultipartRefusesUnnamedFormatsBeforeReading(t *testing.T) {
	offering := multipartOffering()
	offering.Parameters[3].Extensions = []string{"png", "jpg"}
	assets := &fixtureAssets{data: []byte("fixture")}
	_, _, err := imagewire.EncodeMultipart(context.Background(), generation.StartRequest{
		Offering: offering,
		Input:    json.RawMessage(`{"prompt":"edit","reference_assets":["a"]}`),
		InputAssets: map[string][]generation.InputAsset{
			"reference_assets": {{ID: "a", MediaType: "image/gif", OriginalFilename: "a.gif"}},
		},
	}, assets)
	if !errors.Is(err, uploadname.ErrNoReliableExtension) || !strings.Contains(err.Error(), "reference_assets") {
		t.Fatalf("refusal = %v", err)
	}
	if len(assets.urls) != 0 {
		t.Fatalf("assets opened before refusal: %#v", assets.urls)
	}
}

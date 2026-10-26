package imagewire_test

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativeConsentRemainsPrivateForJSONAndMultipart(t *testing.T) {
	o := generation.Offering{NativePricing: &schema.NativePricing{Version: 1, Unit: "provider.credit", UnitLabel: "Provider credits", BaseAmount: "0.5", Components: []schema.NativePriceComponent{{ID: "quality", Label: "Quality", Amount: "0.5"}}, ConsentParameter: "server_consent", MaxAuthorizedExtraAmount: "0.5"}, ProviderOptions: json.RawMessage(`{"request_template":{"model":"test"}}`), Parameters: []generation.Parameter{{Name: "prompt", Type: "text"}, {Name: "server_consent", Type: "boolean"}}}
	r := generation.StartRequest{Offering: o, Input: json.RawMessage(`{"prompt":"test","server_consent":true}`)}
	body, e := imagewire.Build(context.Background(), r, nil)
	if e != nil {
		t.Fatal(e)
	}
	if body["prompt"] != "test" || body["server_consent"] != nil {
		t.Fatalf("control leaked into JSON %v", body)
	}
	stream, ct, e := imagewire.EncodeMultipart(context.Background(), r, nil)
	if e != nil {
		t.Fatal(e)
	}
	boundary := strings.Split(ct, "boundary=")[1]
	form := multipart.NewReader(stream, boundary)
	seen := map[string]bool{}
	for {
		part, e := form.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		seen[part.FormName()] = true
		_, _ = io.Copy(io.Discard, part)
	}
	if !seen["prompt"] || seen["server_consent"] {
		t.Fatalf("control leaked into multipart %v", seen)
	}
}

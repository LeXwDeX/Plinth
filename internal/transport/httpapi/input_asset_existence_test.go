package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #118 (AUTH-09): another user's input asset must be indistinguishable from a
// missing one on every download route, byte for byte except the request ID.
func TestForeignInputAssetResponseMatchesMissingAsset(t *testing.T) {
	t.Parallel()
	_, handler, inputs := newHTTPHarnessWithRoles(t, nil)
	asset, err := inputs.UploadAsset(context.Background(), "another-owner", generation.InputAssetUpload{
		Body:             strings.NewReader("private"),
		OriginalFilename: "private.png",
		MediaType:        "image/png",
		AssetType:        "image",
		MaxBytes:         1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestID := regexp.MustCompile(`req_[0-9a-f]+`)
	fetch := func(path string) (int, string, http.Header) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		header := response.Header().Clone()
		header.Del("X-Request-Id")
		return response.Code, requestID.ReplaceAllString(response.Body.String(), "req_X"), header
	}
	for _, prefix := range []string{"/api/assets/", "/api/v1/assets/"} {
		for _, suffix := range []string{"", "?size=preview"} {
			foreignCode, foreignBody, foreignHeader := fetch(prefix + string(asset.ID) + suffix)
			missingCode, missingBody, missingHeader := fetch(prefix + "asset_00000000000000000000000000000000" + suffix)
			if foreignCode != http.StatusNotFound || foreignCode != missingCode {
				t.Fatalf("%s%s: foreign %d, missing %d", prefix, suffix, foreignCode, missingCode)
			}
			if foreignBody != missingBody {
				t.Fatalf("%s%s: bodies differ: %q vs %q", prefix, suffix, foreignBody, missingBody)
			}
			if len(foreignHeader) != len(missingHeader) {
				t.Fatalf("%s%s: headers differ: %v vs %v", prefix, suffix, foreignHeader, missingHeader)
			}
			for name, values := range missingHeader {
				if strings.Join(values, ",") != strings.Join(foreignHeader[name], ",") {
					t.Fatalf("%s%s: header %s differs: %v vs %v", prefix, suffix, name, foreignHeader[name], values)
				}
			}
		}
	}
}

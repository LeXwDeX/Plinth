package fakeupstream_test

import (
	"bytes"
	"compress/gzip"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

func weightRequest(t *testing.T, key string, headers ...string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://fake.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	for index := 0; index+1 < len(headers); index += 2 {
		request.Header.Set(headers[index], headers[index+1])
	}
	return request
}

// #629: every selector reaches the same weight, the first one present wins,
// and nothing is padded by default.
func TestPictureWeightSelectors(t *testing.T) {
	env := fakeupstream.NewEnv(fakeupstream.Config{})
	text, noise := fakeupstream.FillerText, fakeupstream.FillerNoise
	cases := []struct {
		name    string
		request *http.Request
		prompt  string
		want    fakeupstream.PictureWeight
	}{
		{"default", weightRequest(t, "test-key"), "a cube", fakeupstream.PictureWeight{}},
		{"header", weightRequest(t, "test-key", fakeupstream.PictureBytesHeader, "5000"), "", fakeupstream.PictureWeight{Bytes: 5000, Filler: text}},
		{"header noise", weightRequest(t, "test-key", fakeupstream.PictureBytesHeader, "5000", fakeupstream.PictureFillerHeader, "noise"), "", fakeupstream.PictureWeight{Bytes: 5000, Filler: noise}},
		{"key", weightRequest(t, "fake-response-bytes-6000"), "", fakeupstream.PictureWeight{Bytes: 6000, Filler: text}},
		{"key noise", weightRequest(t, "fake-response-bytes-6000-noise"), "", fakeupstream.PictureWeight{Bytes: 6000, Filler: noise}},
		{"marker", weightRequest(t, "test-key"), "a cube [[fake:picture-bytes:7000]] please", fakeupstream.PictureWeight{Bytes: 7000, Filler: text}},
		{"marker noise", weightRequest(t, "test-key"), "[[fake:picture-bytes:7000:noise]]", fakeupstream.PictureWeight{Bytes: 7000, Filler: noise}},
		{"header beats marker", weightRequest(t, "test-key", fakeupstream.PictureBytesHeader, "1"), "[[fake:picture-bytes:7000]]", fakeupstream.PictureWeight{Bytes: 1, Filler: text}},
		{"capped", weightRequest(t, "test-key"), "[[fake:picture-bytes:999999999999]]", fakeupstream.PictureWeight{Bytes: fakeupstream.MaxPictureBytes, Filler: text}},
		{"malformed marker", weightRequest(t, "test-key"), "[[fake:picture-bytes:heavy]]", fakeupstream.PictureWeight{}},
		{"unclosed marker", weightRequest(t, "test-key"), "[[fake:picture-bytes:7000", fakeupstream.PictureWeight{}},
	}
	for _, c := range cases {
		if got := env.PictureWeightOf(c.request, c.prompt, 64, 64); got != c.want {
			t.Errorf("%s: weight = %+v, want %+v", c.name, got, c.want)
		}
	}

	// The stack-wide weight reaches pictures of at least min_pixels only, and a
	// request selector still wins over it.
	if err := env.SetPictureWeight(fakeupstream.PictureWeight{Bytes: 9000, MinPixels: 4096, Filler: noise}); err != nil {
		t.Fatal(err)
	}
	if got := env.PictureWeightOf(weightRequest(t, "test-key"), "", 64, 64); got != (fakeupstream.PictureWeight{Bytes: 9000, Filler: noise}) {
		t.Errorf("stack weight at min_pixels = %+v", got)
	}
	if got := env.PictureWeightOf(weightRequest(t, "test-key"), "", 63, 64); got != (fakeupstream.PictureWeight{}) {
		t.Errorf("stack weight below min_pixels = %+v, want none", got)
	}
	if got := env.PictureWeightOf(weightRequest(t, "test-key"), "[[fake:picture-bytes:10]]", 64, 64); got.Bytes != 10 {
		t.Errorf("marker under a stack weight = %+v, want the marker's", got)
	}
	for _, bad := range []fakeupstream.PictureWeight{{Bytes: -1}, {Bytes: fakeupstream.MaxPictureBytes + 1}, {MinPixels: -1}, {Filler: "zeros"}} {
		if err := env.SetPictureWeight(bad); err == nil {
			t.Errorf("stack weight %+v was accepted", bad)
		}
	}
}

func gzipSize(t *testing.T, data []byte) int {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Len()
}

// #629: a weighted picture keeps its pixels, size and format; the text filler
// compresses to almost nothing and the noise filler does not compress, and
// the same weight answers the same shared bytes again.
func TestWeightedPictureKeepsThePictureAndChoosesItsFiller(t *testing.T) {
	const weight = 3 << 20
	for _, format := range []string{"png", "jpeg"} {
		spec := fakeupstream.PictureSpec{Format: format, Width: 512, Height: 768}
		plain, err := fakeupstream.Picture(spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, filler := range []fakeupstream.Filler{fakeupstream.FillerText, fakeupstream.FillerNoise} {
			file, err := fakeupstream.WeightedPicture(spec, fakeupstream.PictureWeight{Bytes: weight, Filler: filler})
			if err != nil {
				t.Fatal(err)
			}
			if len(file.Data) < weight || len(file.Data) > weight+16 || file.MediaType != plain.MediaType {
				t.Fatalf("%s %s: %d bytes %s, want about %d bytes %s", format, filler, len(file.Data), file.MediaType, weight, plain.MediaType)
			}
			decoded, kind, err := image.Decode(bytes.NewReader(file.Data))
			if err != nil || kind != format || decoded.Bounds().Dx() != 512 || decoded.Bounds().Dy() != 768 {
				t.Fatalf("%s %s: decoded %s %v err=%v", format, filler, kind, decoded.Bounds(), err)
			}
			compressed := gzipSize(t, file.Data)
			switch filler {
			case fakeupstream.FillerText:
				if compressed > weight/20 {
					t.Fatalf("%s text filler gzips to %d bytes, want far below %d", format, compressed, weight)
				}
			case fakeupstream.FillerNoise:
				if compressed < weight*95/100 {
					t.Fatalf("%s noise filler gzips to %d bytes, want about %d", format, compressed, weight)
				}
			}
			again, _ := fakeupstream.WeightedPicture(spec, fakeupstream.PictureWeight{Bytes: weight, Filler: filler})
			if &again.Data[0] != &file.Data[0] {
				t.Fatalf("%s %s: the padded picture was padded again instead of reused", format, filler)
			}
		}
		// No weight, or one at or below the drawn bytes, keeps the picture as drawn.
		for _, light := range []int{0, len(plain.Data)} {
			if file, _ := fakeupstream.WeightedPicture(spec, fakeupstream.PictureWeight{Bytes: light}); !bytes.Equal(file.Data, plain.Data) {
				t.Fatalf("%s weight %d changed the picture", format, light)
			}
		}
	}
}

// #629: the stack-wide weight is set, read and reset through the control route.
func TestPictureWeightControlRoute(t *testing.T) {
	fake, err := fakeupstream.New(fakeupstream.Config{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fake)
	defer server.Close()
	do := func(method, body string) (int, string) {
		request, err := http.NewRequest(method, server.URL+fakeupstream.PictureWeightPath, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, strings.TrimSpace(string(raw))
	}
	if status, body := do(http.MethodPut, `{"bytes":20000000,"min_pixels":16000000,"filler":"noise"}`); status != http.StatusOK ||
		body != `{"bytes":20000000,"min_pixels":16000000,"filler":"noise"}` {
		t.Fatalf("PUT = %d %s", status, body)
	}
	if got := fake.Env().StackPictureWeight(); got.Bytes != 20000000 || got.MinPixels != 16000000 || got.Filler != fakeupstream.FillerNoise {
		t.Fatalf("stack weight = %+v", got)
	}
	for _, bad := range []string{`{"bytes":-1}`, `{"bytes":1,"filler":"zeros"}`, `{"weight":1}`, `not json`} {
		if status, _ := do(http.MethodPut, bad); status != http.StatusBadRequest {
			t.Fatalf("PUT %s = %d, want 400", bad, status)
		}
	}
	if status, _ := do(http.MethodDelete, ""); status != http.StatusNoContent {
		t.Fatalf("DELETE = %d", status)
	}
	if status, body := do(http.MethodGet, ""); status != http.StatusOK || body != `{"bytes":0}` {
		t.Fatalf("GET after reset = %d %s", status, body)
	}
}

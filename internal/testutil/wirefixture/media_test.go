package wirefixture_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

const mediaCanary = "PRIVATE-CANARY-4f4f-9a6e"

func mediaMetadata() wirefixture.Metadata {
	meta := testMetadata()
	meta.Provenance = wirefixture.ProvenanceCaptured
	return meta
}

func derivedMetadata() wirefixture.Metadata {
	meta := testMetadata()
	meta.Provenance = wirefixture.ProvenanceDerived
	return meta
}

func privateStore(t *testing.T) (*wirefixture.PrivateStore, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wirefixture-evidence-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := wirefixture.NewPrivateStore(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dir
}

func TestPrivateSinkZeroValueAbort(t *testing.T) {
	var sink wirefixture.PrivateSink
	sink.Abort()
	sink.Abort()
}

func TestPrivateStoreCloseRejectsNewEvidence(t *testing.T) {
	store, dir := privateStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewSink("image/png"); !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("closed store accepted new evidence: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("closed store wrote evidence: %d entries, %v", len(entries), err)
	}
}

type multipartField struct {
	name  string
	value string
}

type multipartFile struct {
	name      string
	filename  string
	mediaType string
	data      []byte
}

func multipartBody(t *testing.T, boundary string, fields []multipartField, files []multipartFile) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		part, err := writer.CreateFormField(field.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(field.value)); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name": file.name, "filename": file.filename,
		}))
		header.Set("Content-Type", file.mediaType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func multipartRequest(t *testing.T, body []byte, boundary string) *http.Request {
	t.Helper()
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/inputs", string(body))
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	return request
}

func syntheticPNG(t *testing.T) []byte {
	t.Helper()
	_, data, err := wirefixture.SyntheticMedia("png-1x1")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func privateFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func TestBinaryResponseCaptureAndPrivateReplay(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	store, dir := privateStore(t)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
	// Synthetic transport bytes with a canary appended. Capture trusts the
	// transport Content-Type and never decodes the image.
	transportBytes := append(syntheticPNG(t), []byte(mediaCanary)...)
	response, body := capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK,
				Header:        http.Header{"Content-Type": {"image/png"}},
				Body:          io.NopCloser(bytes.NewReader(transportBytes)),
				ContentLength: int64(len(transportBytes))}, nil
		}))
	if !bytes.Equal(body, transportBytes) {
		t.Fatal("business response changed")
	}
	exchange := recorder.Exchanges()[0]
	if !exchange.Complete || exchange.Invalid != "" {
		t.Fatalf("capture invalid: %+v", exchange)
	}
	if exchange.Response.ContentType != "image/png" || exchange.Metadata.Protocol != "media" {
		t.Fatalf("media metadata: %+v", exchange)
	}
	if exchange.Response.Body.Encoding != wirefixture.PayloadMedia || exchange.Response.Body.Media == nil {
		t.Fatalf("media payload: %+v", exchange.Response.Body)
	}
	media := exchange.Response.Body.Media
	if media.MediaType != "image/png" || media.Length != int64(len(transportBytes)) || media.Recipe != "" || media.Substituted {
		t.Fatalf("media descriptor: %+v", media)
	}
	if exchange.Response.Status != http.StatusOK || response.StatusCode != http.StatusOK {
		t.Fatal("response status changed")
	}
	data, err := json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(mediaCanary)) || bytes.Contains(data, []byte(dir)) ||
		bytes.Contains(data, []byte("iVBOR")) || bytes.Contains(data, []byte("media-")) {
		t.Fatalf("public exchange leaked private evidence: %s", data)
	}
	if err := wirefixture.Validate(recorder.Exchanges(), policy); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AssertComplete(); err != nil {
		t.Fatal(err)
	}
	if err := wirefixture.WriteCandidate(t.TempDir(), filepath.Join(t.TempDir(), "candidate.json"), recorder.Exchanges(), policy); !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("private media export error = %v", err)
	}

	resolver, err := wirefixture.NewPrivateMediaResolver(recorder.MediaEvidence())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := wirefixture.NewReplayWithMedia(recorder.Exchanges(), policy, resolver)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/result", ""))
	if err != nil {
		t.Fatal(err)
	}
	replayedBody, err := io.ReadAll(replayed.Body)
	_ = replayed.Body.Close()
	if err != nil || !bytes.Equal(replayedBody, transportBytes) {
		t.Fatalf("replayed media = %q err=%v", replayedBody, err)
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}

	paths := privateFiles(t, dir)
	if len(paths) != 1 {
		t.Fatalf("private files = %d", len(paths))
	}
	info, err := os.Lstat(paths[0])
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("private file mode: %v %v", info, err)
	}
	altered := append([]byte(nil), transportBytes...)
	altered[0] ^= 1
	if err := os.WriteFile(paths[0], altered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths[0], 0o600); err != nil {
		t.Fatal(err)
	}
	tampered, err := wirefixture.NewReplayWithMedia(recorder.Exchanges(), policy, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tampered.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/result", "")); err == nil {
		t.Fatal("tampered same-size evidence replayed")
	}
	if err := tampered.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("tampered replay assertion = %v", err)
	}
}

func TestMultipartCaptureAndStrictReplay(t *testing.T) {
	policy := testPolicy()
	policy.Keys["prompt"] = true
	policy.Keys["file"] = true
	policy.Allowed["synthetic prompt"] = true
	policy.Allowed["inputs"] = true
	store, dir := privateStore(t)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
	// Synthetic transport bytes with a canary, not a decoded image.
	png := append(syntheticPNG(t), []byte(mediaCanary)...)
	files := []multipartFile{
		{name: "file", filename: "original-canary.png", mediaType: "image/png", data: png},
		{name: "file", filename: "second-canary.png", mediaType: "image/png", data: append(append([]byte(nil), png...), '\n')},
	}
	fields := []multipartField{{name: "prompt", value: "synthetic prompt"}}
	body := multipartBody(t, "boundary-one", fields, files)
	request := multipartRequest(t, body, "boundary-one")
	_, responseBody := capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
	if string(responseBody) != "{}" {
		t.Fatalf("business response changed: %q", responseBody)
	}
	exchange := recorder.Exchanges()[0]
	if !exchange.Complete || exchange.Request.ContentType != "multipart/form-data" ||
		exchange.Request.Body.Encoding != wirefixture.PayloadMultipart || exchange.Request.Body.Multipart == nil {
		t.Fatalf("multipart capture: %+v", exchange)
	}
	parts := exchange.Request.Body.Multipart.Parts
	if len(parts) != 3 || parts[0].Name != "prompt" || parts[0].Value != "synthetic prompt" ||
		parts[1].Name != "file" || parts[2].Name != "file" {
		t.Fatalf("part order = %+v", parts)
	}
	for i, want := range files {
		part := parts[i+1]
		if part.Media == nil || part.Media.MediaType != want.mediaType || part.Media.Length != int64(len(want.data)) ||
			part.Media.Recipe != "" || part.Media.Substituted {
			t.Fatalf("part %d media = %+v", i, part.Media)
		}
	}
	data, err := json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{mediaCanary, "original-canary.png", "second-canary.png", "boundary-one", string(png[:8]), dir} {
		if bytes.Contains(data, []byte(leaked)) {
			t.Fatalf("public multipart leaked %q: %s", leaked, data)
		}
	}
	if err := wirefixture.Validate(recorder.Exchanges(), policy); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AssertComplete(); err != nil {
		t.Fatal(err)
	}
	if err := wirefixture.WriteCandidate(t.TempDir(), filepath.Join(t.TempDir(), "candidate.json"), recorder.Exchanges(), policy); !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("private multipart export error = %v", err)
	}
	if len(privateFiles(t, dir)) != 2 {
		t.Fatalf("private files = %d", len(privateFiles(t, dir)))
	}

	resolver, err := wirefixture.NewPrivateMediaResolver(recorder.MediaEvidence())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := wirefixture.NewReplayWithMedia(recorder.Exchanges(), policy, resolver)
	if err != nil {
		t.Fatal(err)
	}
	changedBoundary := multipartRequest(t, multipartBody(t, "boundary-two", fields, files), "boundary-two")
	response, err := replay.RoundTrip(changedBoundary)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}

	failCases := []struct {
		name     string
		body     []byte
		boundary string
	}{
		{
			name: "reordered",
			body: multipartBody(t, "boundary-r", []multipartField{{name: "prompt", value: "synthetic prompt"}},
				[]multipartFile{files[1], files[0]}),
			boundary: "boundary-r",
		},
		{
			name:     "extra",
			body:     multipartBody(t, "boundary-e", append(fields, multipartField{name: "prompt", value: "synthetic prompt"}), files),
			boundary: "boundary-e",
		},
		{
			name:     "absent",
			body:     multipartBody(t, "boundary-a", fields, files[:1]),
			boundary: "boundary-a",
		},
		{
			name: "same-size-tamper",
			body: multipartBody(t, "boundary-t", fields, []multipartFile{files[0],
				{name: "file", filename: "second-canary.png", mediaType: "image/png", data: append([]byte(nil), files[1].data...)}}),
			boundary: "boundary-t",
		},
	}
	failCases[3].body = func() []byte {
		tampered := append([]byte(nil), files[1].data...)
		tampered[0] ^= 1
		return multipartBody(t, "boundary-t", fields, []multipartFile{files[0],
			{name: "file", filename: "second-canary.png", mediaType: "image/png", data: tampered}})
	}()
	for _, tc := range failCases {
		t.Run(tc.name, func(t *testing.T) {
			fresh, err := wirefixture.NewReplayWithMedia(recorder.Exchanges(), policy, resolver)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fresh.RoundTrip(multipartRequest(t, tc.body, tc.boundary)); err == nil {
				t.Fatal("mismatched multipart replayed")
			}
			if err := fresh.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
				t.Fatalf("sticky failure = %v", err)
			}
		})
	}

	noResolver, err := wirefixture.NewReplay(recorder.Exchanges(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noResolver.RoundTrip(multipartRequest(t, multipartBody(t, "boundary-n", fields, files), "boundary-n")); !errors.Is(err, wirefixture.ErrMediaResolver) {
		t.Fatalf("missing resolver error = %v", err)
	}
	if err := noResolver.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("missing resolver assertion = %v", err)
	}
}

func TestSyntheticMediaReplayAndExport(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	media, want, err := wirefixture.SyntheticMedia("png-1x1")
	if err != nil {
		t.Fatal(err)
	}
	exchange := safetyExchange()
	exchange.Metadata.Provenance = wirefixture.ProvenanceDerived
	exchange.Metadata.Protocol = "media"
	exchange.Metadata.Endpoint = "GET /v1/result"
	exchange.Request.Path = "/v1/result"
	exchange.Response.ContentType = "image/png"
	exchange.Response.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMedia, Media: &media}

	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); err != nil {
		t.Fatal(err)
	}
	replay, err := wirefixture.NewReplayWithMedia([]wirefixture.Exchange{exchange}, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/result", ""))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || !bytes.Equal(body, want) {
		t.Fatalf("synthetic replay = %d bytes err=%v", len(body), err)
	}
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}

	privateResolver := &wirefixture.PrivateMediaResolver{}
	if _, err := privateResolver.Resolve(wirefixture.MediaLocation{Sequence: 1, Part: -1}, media); !errors.Is(err, wirefixture.ErrMediaResolver) {
		t.Fatalf("private resolver accepted recipe media: %v", err)
	}
	privateMedia := wirefixture.Media{MediaType: "image/png", Length: int64(len(want))}
	if _, err := (wirefixture.SyntheticMediaResolver{}).Resolve(wirefixture.MediaLocation{Sequence: 1, Part: -1}, privateMedia); !errors.Is(err, wirefixture.ErrMediaResolver) {
		t.Fatalf("synthetic resolver accepted private media: %v", err)
	}
	captured := exchange
	captured.Metadata.Provenance = wirefixture.ProvenanceCaptured
	if err := wirefixture.Validate([]wirefixture.Exchange{captured}, policy); err == nil {
		t.Fatal("captured provenance accepted derived media")
	}
	unknown := media
	unknown.Recipe = "unknown-recipe"
	unknown.Substituted = true
	poisoned := exchange
	poisoned.Response.Body.Media = &unknown
	if err := wirefixture.Validate([]wirefixture.Exchange{poisoned}, policy); err == nil {
		t.Fatal("unknown recipe validated")
	}

	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "derived.json")
	if err := wirefixture.WriteCandidate(root, path, []wirefixture.Exchange{exchange}, policy); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"recipe":"png-1x1"`)) || !bytes.Contains(data, []byte(`"substituted":true`)) {
		t.Fatalf("derived descriptor lost provenance: %s", data)
	}
	if bytes.Contains(data, []byte("iVBOR")) {
		t.Fatalf("derived candidate contains binary encoding: %s", data)
	}
}

func TestSyntheticMediaContentTypeMustMatch(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	media, _, err := wirefixture.SyntheticMedia("png-1x1")
	if err != nil {
		t.Fatal(err)
	}
	exchange := safetyExchange()
	exchange.Metadata.Provenance = wirefixture.ProvenanceDerived
	exchange.Metadata.Protocol = "media"
	exchange.Metadata.Endpoint = "GET /v1/result"
	exchange.Request.Path = "/v1/result"
	exchange.Response.ContentType = "model/gltf-binary"
	exchange.Response.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMedia, Media: &media}

	if err := wirefixture.Validate([]wirefixture.Exchange{exchange}, policy); !errors.Is(err, wirefixture.ErrUnsafePublicValue) {
		t.Fatalf("mismatched response media type error = %v", err)
	}
	if _, err := wirefixture.NewReplayWithMedia([]wirefixture.Exchange{exchange}, policy, wirefixture.SyntheticMediaResolver{}); err == nil {
		t.Fatal("mismatched response media type was replayed")
	}
	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "mismatch.json")
	if err := wirefixture.WriteCandidate(root, path, []wirefixture.Exchange{exchange}, policy); err == nil {
		t.Fatal("mismatched response media type was exported")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched candidate exists: %v", err)
	}
}

func TestReplayConnectionIdentity(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	request := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", "")
	_, _ = capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
	exchanges := recorder.Exchanges()
	if exchanges[0].Metadata.Connection != "hyper3d" {
		t.Fatalf("connection = %q", exchanges[0].Metadata.Connection)
	}

	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.WithConnection("agent").RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", "")); !errors.Is(err, wirefixture.ErrConnectionMismatch) {
		t.Fatalf("connection mismatch = %v", err)
	}
	if err := replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("connection failure assertion = %v", err)
	}

	replay, err = wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	response, err := replay.WithConnection("hyper3d").RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/tasks", ""))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}

	recorder = &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	_, _ = capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/tasks", ""),
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			header := http.Header{"Content-Type": {"application/json"}}
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{}`)), ContentLength: 2}, nil
		}))
	if recorder.Exchanges()[0].Metadata.Connection != "hyper3d" {
		t.Fatalf("default connection lost: %+v", recorder.Exchanges()[0].Metadata)
	}
}

func TestReplayFailureIsSticky(t *testing.T) {
	policy := testPolicy()
	recorder := &wirefixture.Recorder{Policy: policy, Default: testMetadata()}
	first := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/first", "")
	_, _ = capture(t, recorder, first, jsonResponse(http.StatusOK, "application/json", `{}`))
	second := mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/second", "")
	_, _ = capture(t, recorder, second, jsonResponse(http.StatusOK, "application/json", `{}`))
	replay, err := wirefixture.NewReplay(recorder.Exchanges(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/second", "")); !errors.Is(err, wirefixture.ErrReorderedRequest) {
		t.Fatalf("reordered error = %v", err)
	}
	for _, path := range []string{"/v1/first", "/v1/second"} {
		if _, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test"+path, "")); !errors.Is(err, wirefixture.ErrReplayFailed) {
			t.Fatalf("sticky replay error = %v", err)
		}
	}
	if err := replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("sticky assertion = %v", err)
	}
}

func TestEvidenceStorageFailureDoesNotChangeBusiness(t *testing.T) {
	policy := testPolicy()
	policy.Keys["file"] = true
	policy.Allowed["result"] = true
	policy.Allowed["inputs"] = true
	t.Run("binary response", func(t *testing.T) {
		store, dir := privateStore(t)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
		payload := syntheticPNG(t)
		_, body := capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""),
			roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}},
					Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload))}, nil
			}))
		if !bytes.Equal(body, payload) {
			t.Fatal("storage failure changed business response")
		}
		exchange := recorder.Exchanges()[0]
		if exchange.Complete || exchange.Invalid == "" {
			t.Fatalf("storage failure accepted: %+v", exchange)
		}
		if len(recorder.Failures()) != 0 {
			t.Fatalf("storage failure became transport failure: %v", recorder.Failures())
		}
		if err := recorder.AssertComplete(); err == nil {
			t.Fatal("incomplete evidence capture asserted complete")
		}
	})

	t.Run("multipart request", func(t *testing.T) {
		store, dir := privateStore(t)
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
		request := multipartRequest(t, multipartBody(t, "boundary-f", nil,
			[]multipartFile{{name: "file", filename: "canary.png", mediaType: "image/png", data: syntheticPNG(t)}}), "boundary-f")
		_, body := capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
		if string(body) != "{}" {
			t.Fatal("storage failure changed business response")
		}
		exchange := recorder.Exchanges()[0]
		if exchange.Complete || exchange.Invalid == "" {
			t.Fatalf("multipart storage failure accepted: %+v", exchange)
		}
		if len(recorder.Failures()) != 0 {
			t.Fatalf("multipart storage failure became transport failure: %v", recorder.Failures())
		}
	})
}

func TestMultipartRejectsUnsupportedShapes(t *testing.T) {
	policy := testPolicy()
	policy.Keys["prompt"] = true
	policy.Keys["file"] = true
	policy.Allowed["synthetic prompt"] = true
	policy.Allowed["inputs"] = true
	png := syntheticPNG(t)
	valid := multipartBody(t, "boundary-u", []multipartField{{name: "prompt", value: "synthetic prompt"}},
		[]multipartFile{{name: "file", filename: "canary.png", mediaType: "image/png", data: png}})
	cases := map[string]struct {
		body        []byte
		contentType string
	}{
		"nested": {
			body: multipartBody(t, "boundary-n", nil, []multipartFile{
				{name: "file", filename: "canary.png", mediaType: "multipart/mixed", data: valid},
			}),
			contentType: "multipart/form-data; boundary=boundary-n",
		},
		"unknown header": {
			body:        withPartHeader(t, "boundary-h", "X-Unknown", "value", png),
			contentType: "multipart/form-data; boundary=boundary-h",
		},
		"base64 transfer": {
			body:        withTransferEncoding(t, "boundary-b", "base64", png),
			contentType: "multipart/form-data; boundary=boundary-b",
		},
		"truncated": {
			body:        valid[:len(valid)-8],
			contentType: "multipart/form-data; boundary=boundary-u",
		},
		"trailing junk": {
			body:        append(append([]byte(nil), valid...), []byte("junk")...),
			contentType: "multipart/form-data; boundary=boundary-u",
		},
		"unknown part name": {
			body:        multipartBody(t, "boundary-x", []multipartField{{name: "unknown", value: "value"}}, nil),
			contentType: "multipart/form-data; boundary=boundary-x",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata()}
			request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/inputs", string(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			_, body := capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
			if string(body) != "{}" {
				t.Fatal("business response changed")
			}
			exchange := recorder.Exchanges()[0]
			if exchange.Complete || exchange.Invalid == "" {
				t.Fatalf("unsupported multipart accepted: %+v", exchange)
			}
			if err := wirefixture.Validate(recorder.Exchanges(), policy); err == nil {
				t.Fatal("unsupported multipart validated")
			}
		})
	}
}

func withPartHeader(t *testing.T, boundary, name, value string, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="canary.png"`)
	header.Set("Content-Type", "image/png")
	header.Set(name, value)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func withTransferEncoding(t *testing.T, boundary, encoding string, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="canary.png"`)
	header.Set("Content-Type", "image/png")
	header.Set("Content-Transfer-Encoding", encoding)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type countingBody struct {
	data   []byte
	reads  int
	closed bool
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.reads++
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *countingBody) Close() error {
	b.closed = true
	return nil
}

func TestBinaryResponseCloseDoesNotDrain(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata()}
	body := &countingBody{data: []byte("0123456789")}
	response, err := recorder.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}},
			Body: body, ContentLength: 10}, nil
	})).RoundTrip(mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""))
	if err != nil {
		t.Fatal(err)
	}
	read := make([]byte, 2)
	if n, err := response.Body.Read(read); err != nil || n != 2 || string(read) != "01" {
		t.Fatalf("business read = %d %q %v", n, read, err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if body.reads != 1 || !body.closed {
		t.Fatalf("binary close drained: reads=%d closed=%v", body.reads, body.closed)
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("partial binary capture accepted: %+v", exchange)
	}
	if len(recorder.Failures()) != 0 {
		t.Fatalf("partial binary capture failure: %v", recorder.Failures())
	}
}

func TestUnsupportedInlineBinaryRequest(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["inputs"] = true
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/inputs", string(syntheticPNG(t)))
	request.Header.Set("Content-Type", "image/png")
	_, body := capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
	if string(body) != "{}" {
		t.Fatal("unsupported inline binary changed business response")
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" || !strings.Contains(exchange.Invalid, "unsupported") {
		t.Fatalf("inline binary request accepted: %+v", exchange)
	}
}

func TestMultipartRejectsPreamblePrefixTrick(t *testing.T) {
	policy := testPolicy()
	policy.Keys["prompt"] = true
	policy.Allowed["synthetic prompt"] = true
	policy.Allowed["inputs"] = true
	normal := multipartBody(t, "probe-boundary", []multipartField{{name: "prompt", value: "synthetic prompt"}}, nil)
	canary := "PRIVATE-PREAMBLE-CANARY-7b7b"
	crafted := append([]byte("--probe-boundaryX\r\n"+canary+"\r\n"), normal...)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/inputs", string(crafted))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=probe-boundary")
	_, body := capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
	if string(body) != "{}" {
		t.Fatal("preamble trick changed business response")
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("preamble prefix trick accepted: %+v", exchange)
	}
	data, err := json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(canary)) || strings.Contains(exchange.Invalid, canary) {
		t.Fatalf("preamble canary leaked: %s", exchange.Invalid)
	}
}

func TestMultipartRejectsEpilogue(t *testing.T) {
	policy := testPolicy()
	policy.Keys["prompt"] = true
	policy.Allowed["synthetic prompt"] = true
	policy.Allowed["inputs"] = true
	normal := multipartBody(t, "probe-boundary", []multipartField{{name: "prompt", value: "synthetic prompt"}}, nil)
	canary := "PRIVATE-EPILOGUE-CANARY-9d9d"
	crafted := append(append([]byte(nil), normal...), []byte("--probe-boundary--\r\n"+canary+"\r\n--probe-boundary--\r\n")...)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata()}
	request := mustRequest(t, http.MethodPost, "https://api.meshy.ai/v1/inputs", string(crafted))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=probe-boundary")
	_, body := capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
	if string(body) != "{}" {
		t.Fatal("epilogue trick changed business response")
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("multipart epilogue accepted: %+v", exchange)
	}
	data, err := json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(canary)) || strings.Contains(exchange.Invalid, canary) {
		t.Fatalf("epilogue canary leaked: %s", exchange.Invalid)
	}
}

func TestEmptyMultipartPayloadRejected(t *testing.T) {
	policy := testPolicy()
	policy.Keys["prompt"] = true
	policy.Allowed["inputs"] = true
	metadata := testMetadata()
	metadata.Endpoint = "POST /v1/inputs"
	metadata.Protocol = "json"
	exchange := wirefixture.Exchange{
		Sequence: 1,
		Metadata: metadata,
		Request: wirefixture.Request{
			Method: http.MethodPost, Host: "example.test", Path: "/v1/inputs",
			ContentType: "multipart/form-data", Body: wirefixture.Payload{Encoding: wirefixture.PayloadNone}, Complete: true,
		},
		Response: wirefixture.Response{
			Status: http.StatusOK, ContentType: "application/json",
			Body: wirefixture.Payload{Encoding: wirefixture.PayloadJSON, JSON: []byte(`{}`)}, Complete: true,
		},
		Complete: true,
	}
	exchanges := []wirefixture.Exchange{exchange}
	if err := wirefixture.Validate(exchanges, policy); err == nil {
		t.Fatal("no-byte multipart payload validated")
	}
	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "empty-multipart.json")
	if err := wirefixture.WriteCandidate(root, path, exchanges, policy); err == nil {
		t.Fatal("no-byte multipart payload exported")
	} else if strings.Contains(err.Error(), dir) {
		t.Fatalf("export error leaked private path: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected candidate exists: %v", err)
	}
	if _, err := wirefixture.NewReplay(exchanges, policy); err == nil {
		t.Fatal("no-byte multipart payload replayed")
	}

	emptyParts := exchange
	emptyParts.Request.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMultipart, Multipart: &wirefixture.Multipart{}}
	if err := wirefixture.Validate([]wirefixture.Exchange{emptyParts}, policy); err == nil {
		t.Fatal("explicit empty multipart validated")
	}

	explicitEmptyField := exchange
	explicitEmptyField.Request.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMultipart, Multipart: &wirefixture.Multipart{
		Parts: []wirefixture.MultipartPart{{Name: "prompt"}},
	}}
	if err := wirefixture.Validate([]wirefixture.Exchange{explicitEmptyField}, policy); err != nil {
		t.Fatalf("explicit empty field rejected: %v", err)
	}
}

func TestWriteCandidatePrivateMediaErrorDoesNotLeakPath(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	store, evidenceDir := privateStore(t)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
	payload := append(syntheticPNG(t), []byte(mediaCanary)...)
	_, _ = capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}},
				Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload))}, nil
		}))
	root, dir := safetyCandidateDir(t)
	path := filepath.Join(dir, "private.json")
	err := wirefixture.WriteCandidate(root, path, recorder.Exchanges(), policy)
	if !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("private media export error = %v", err)
	}
	if strings.Contains(err.Error(), evidenceDir) || strings.Contains(err.Error(), mediaCanary) {
		t.Fatalf("export error leaked private evidence: %v", err)
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("private candidate exists: %v", statErr)
	}
}

func TestSyntheticMediaRecipes(t *testing.T) {
	cases := []struct {
		recipe    string
		mediaType string
		length    int64
		digest    string
	}{
		{
			recipe: "png-1x1", mediaType: "image/png", length: 75,
			digest: "2e39f08959218012438d1b58a05b73fe326f02823732d5201cb330225869bfe0",
		},
		{
			recipe: "glb-triangle", mediaType: "model/gltf-binary", length: 400,
			digest: "85770b49ba58fa2cdef58019c09943871cbb884dd21cd4b93ea21b89d577426c",
		},
	}
	for _, tc := range cases {
		t.Run(tc.recipe, func(t *testing.T) {
			media, data, err := wirefixture.SyntheticMedia(tc.recipe)
			if err != nil {
				t.Fatal(err)
			}
			if media.MediaType != tc.mediaType || media.Length != tc.length ||
				media.Recipe != tc.recipe || !media.Substituted {
				t.Fatalf("synthetic descriptor = %+v", media)
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != tc.digest {
				t.Fatalf("synthetic recipe digest changed: %x", sum)
			}
			if tc.recipe == "glb-triangle" {
				if len(data) < 12 || string(data[:4]) != "glTF" || binary.LittleEndian.Uint32(data[4:8]) != 2 ||
					binary.LittleEndian.Uint32(data[8:12]) != uint32(len(data)) {
					t.Fatalf("GLB header is invalid: %x", data[:min(len(data), 12)])
				}
			}
		})
	}
	_, first, err := wirefixture.SyntheticMedia("png-1x1")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := wirefixture.SyntheticMedia("png-1x1")
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("synthetic PNG is not stable: %v", err)
	}
}

func TestPrivateStoreRejectsCaseAlias(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "private")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(root, "case-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "CASE-PROBE")); err != nil {
		t.Skip("case-sensitive filesystem")
	}
	if store, err := wirefixture.NewPrivateStore(root, strings.ToUpper(inner)); !errors.Is(err, wirefixture.ErrInsideWorktree) {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("case-variant evidence directory error = %v", err)
	}
	if store, err := wirefixture.NewPrivateStore(strings.ToUpper(root), inner); !errors.Is(err, wirefixture.ErrInsideWorktree) {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("case-variant worktree error = %v", err)
	}
}

func TestWriteCandidateFilesystemErrorsDoNotLeakPaths(t *testing.T) {
	policy := testPolicy()
	exchanges := []wirefixture.Exchange{safetyExchange()}
	root := filepath.Join(t.TempDir(), "PRIVATE-ROOT-CANARY")
	dir, err := os.MkdirTemp("", "wirefixture-export-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "candidate.json")
	err = wirefixture.WriteCandidate(root, path, exchanges, policy)
	if err == nil || strings.Contains(err.Error(), "PRIVATE-ROOT-CANARY") {
		t.Fatalf("missing root error = %v", err)
	}

	missingDir := filepath.Join(t.TempDir(), "PRIVATE-DIR-CANARY")
	root = t.TempDir()
	err = wirefixture.WriteCandidate(root, filepath.Join(missingDir, "candidate.json"), exchanges, policy)
	if err == nil || strings.Contains(err.Error(), "PRIVATE-DIR-CANARY") {
		t.Fatalf("missing directory error = %v", err)
	}

	dir, err = os.MkdirTemp("", "wirefixture-export-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	err = wirefixture.WriteCandidate(root, dir, exchanges, policy)
	if err == nil || strings.Contains(err.Error(), dir) {
		t.Fatalf("existing directory error = %v", err)
	}
}

func TestMediaEvidenceFormattingIsRedacted(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	store, dir := privateStore(t)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
	payload := syntheticPNG(t)
	_, _ = capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}},
				Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload))}, nil
		}))
	evidence := recorder.MediaEvidence()
	if len(evidence) != 1 {
		t.Fatalf("evidence = %d", len(evidence))
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		text := fmt.Sprintf(format, evidence)
		if strings.Contains(text, dir) || strings.Contains(text, "media-") || strings.Contains(text, mediaCanary) {
			t.Fatalf("evidence format %s leaked private data: %s", format, text)
		}
		sourceText := fmt.Sprintf(format, evidence[0].Source)
		if strings.Contains(sourceText, dir) || strings.Contains(sourceText, "media-") {
			t.Fatalf("source format %s leaked private data: %s", format, sourceText)
		}
	}
	storeText := fmt.Sprintf("%+v", store)
	if strings.Contains(storeText, dir) || strings.Contains(storeText, "media-") {
		t.Fatalf("store format leaked private data: %s", storeText)
	}
}

func TestPrivateStoreRejectsUnsafeLayout(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "private")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := wirefixture.NewPrivateStore(root, inner); !errors.Is(err, wirefixture.ErrInsideWorktree) {
		t.Fatalf("inside worktree error = %v", err)
	}

	loose, err := os.MkdirTemp("", "wirefixture-loose-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(loose) })
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := wirefixture.NewPrivateStore(root, loose); !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("loose directory error = %v", err)
	}

	store, dir := privateStore(t)
	if _, err := store.Spool("image/png", []byte(mediaCanary)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Spool("image/png", []byte(mediaCanary)); err != nil {
		t.Fatal(err)
	}
	if len(privateFiles(t, dir)) != 2 {
		t.Fatalf("evidence files were overwritten: %v", privateFiles(t, dir))
	}

	alias, err := os.MkdirTemp("", "wirefixture-alias-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(alias) })
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alias, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewSink("image/png"); !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("symlink evidence directory error = %v", err)
	}

	store, dir = privateStore(t)
	source, err := store.Spool("image/png", []byte(mediaCanary))
	if err != nil {
		t.Fatal(err)
	}
	paths := privateFiles(t, dir)
	if len(paths) != 1 {
		t.Fatalf("source files = %v", paths)
	}
	if err := os.Chmod(paths[0], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Bytes(); !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("loose evidence file error = %v", err)
	}
}

func TestPrivateResolverRejectsUnknownOrMislabeledEvidence(t *testing.T) {
	store, _ := privateStore(t)
	source, err := store.Spool("image/png", []byte(mediaCanary))
	if err != nil {
		t.Fatal(err)
	}
	location := wirefixture.MediaLocation{Sequence: 1, Part: 0}
	resolver, err := wirefixture.NewPrivateMediaResolver([]wirefixture.MediaEvidence{{Location: location, Source: source}})
	if err != nil {
		t.Fatal(err)
	}
	good := wirefixture.Media{MediaType: "image/png", Length: int64(len(mediaCanary))}
	if data, err := resolver.Resolve(location, good); err != nil || string(data) != mediaCanary {
		t.Fatalf("private resolve = %q %v", data, err)
	}
	if _, err := resolver.Resolve(wirefixture.MediaLocation{Sequence: 2, Part: 0}, good); !errors.Is(err, wirefixture.ErrMediaEvidence) {
		t.Fatalf("unknown location error = %v", err)
	}
	mislabeled := good
	mislabeled.MediaType = "model/gltf-binary"
	if _, err := resolver.Resolve(location, mislabeled); !errors.Is(err, wirefixture.ErrMediaMismatch) {
		t.Fatalf("mislabeled media error = %v", err)
	}
	derived := good
	derived.Recipe = "png-1x1"
	derived.Substituted = true
	if _, err := resolver.Resolve(location, derived); !errors.Is(err, wirefixture.ErrMediaResolver) {
		t.Fatalf("recipe media accepted by private resolver: %v", err)
	}
	if _, err := wirefixture.NewPrivateMediaResolver([]wirefixture.MediaEvidence{
		{Location: location, Source: source}, {Location: location, Source: source},
	}); err == nil {
		t.Fatal("duplicate evidence location accepted")
	}
	if _, err := wirefixture.NewPrivateMediaResolver([]wirefixture.MediaEvidence{{Location: location}}); err == nil {
		t.Fatal("nil evidence source accepted")
	}
}

type failingReadBody struct {
	data []byte
	err  error
}

func (b *failingReadBody) Read(p []byte) (int, error) {
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	return 0, b.err
}

func (b *failingReadBody) Close() error { return nil }

func TestBinaryReadErrorDoesNotChangeBusiness(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata()}
	readErr := errors.New("synthetic transport read failure")
	response, err := recorder.Wrap("hyper3d", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}},
			Body: &failingReadBody{data: []byte("abc"), err: readErr}, ContentLength: 10}, nil
	})).RoundTrip(mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if !errors.Is(err, readErr) || string(body) != "abc" {
		t.Fatalf("business read = %q %v", body, err)
	}
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	exchange := recorder.Exchanges()[0]
	if exchange.Complete || exchange.Invalid == "" {
		t.Fatalf("read failure accepted: %+v", exchange)
	}
	if len(recorder.Failures()) != 0 {
		t.Fatalf("read failure became transport failure: %v", recorder.Failures())
	}
}

func TestMultipartWithoutFilesNeedsNoResolver(t *testing.T) {
	policy := testPolicy()
	policy.Keys["prompt"] = true
	policy.Allowed["synthetic prompt"] = true
	policy.Allowed["inputs"] = true
	exchange := safetyExchange()
	exchange.Metadata.Endpoint = "POST /v1/inputs"
	exchange.Request.Method = http.MethodPost
	exchange.Request.Path = "/v1/inputs"
	exchange.Request.ContentType = "multipart/form-data"
	exchange.Request.Headers = map[string]string{"accept": "application/json", "x-api-version": "v1"}
	exchange.Request.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMultipart, Multipart: &wirefixture.Multipart{
		Parts: []wirefixture.MultipartPart{{Name: "prompt", Value: "synthetic prompt"}},
	}}
	exchanges := []wirefixture.Exchange{exchange}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	body := multipartBody(t, "boundary-field", []multipartField{{name: "prompt", value: "synthetic prompt"}}, nil)
	response, err := replay.RoundTrip(multipartRequest(t, body, "boundary-field"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestBinaryReplayRequiresResolver(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	media := wirefixture.Media{MediaType: "image/png", Length: int64(len(mediaCanary))}
	exchange := safetyExchange()
	exchange.Metadata.Provenance = wirefixture.ProvenanceCaptured
	exchange.Metadata.Protocol = "media"
	exchange.Metadata.Endpoint = "GET /v1/result"
	exchange.Request.Path = "/v1/result"
	exchange.Response.ContentType = "image/png"
	exchange.Response.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMedia, Media: &media}
	exchanges := []wirefixture.Exchange{exchange}
	if err := wirefixture.Validate(exchanges, policy); err != nil {
		t.Fatal(err)
	}
	replay, err := wirefixture.NewReplay(exchanges, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/result", "")); !errors.Is(err, wirefixture.ErrMediaResolver) {
		t.Fatalf("missing binary resolver = %v", err)
	}
	if err := replay.AssertConsumed(); !errors.Is(err, wirefixture.ErrReplayFailed) {
		t.Fatalf("missing binary resolver assertion = %v", err)
	}
}

func TestAssertCompleteRequiresReadableEvidence(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	store, dir := privateStore(t)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
	payload := syntheticPNG(t)
	_, _ = capture(t, recorder, mustRequest(t, http.MethodGet, "https://api.meshy.ai/v1/result", ""),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}},
				Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload))}, nil
		}))
	if err := recorder.AssertComplete(); err != nil {
		t.Fatal(err)
	}
	paths := privateFiles(t, dir)
	if len(paths) != 1 {
		t.Fatalf("evidence files = %v", paths)
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AssertComplete(); !errors.Is(err, wirefixture.ErrIncompleteCapture) {
		t.Fatalf("missing evidence assertion = %v", err)
	}
}

func TestMediaSnapshotsAreDeep(t *testing.T) {
	policy := testPolicy()
	policy.Allowed["result"] = true
	media, _, err := wirefixture.SyntheticMedia("png-1x1")
	if err != nil {
		t.Fatal(err)
	}
	exchange := safetyExchange()
	exchange.Metadata.Provenance = wirefixture.ProvenanceDerived
	exchange.Metadata.Protocol = "media"
	exchange.Metadata.Endpoint = "GET /v1/result"
	exchange.Request.Path = "/v1/result"
	exchange.Response.ContentType = "image/png"
	exchange.Response.Body = wirefixture.Payload{Encoding: wirefixture.PayloadMedia, Media: &media}
	replay, err := wirefixture.NewReplayWithMedia([]wirefixture.Exchange{exchange}, policy, wirefixture.SyntheticMediaResolver{})
	if err != nil {
		t.Fatal(err)
	}
	exchange.Response.Body.Media.Recipe = "unknown-recipe"
	exchange.Response.Body.Media.Length = 1
	response, err := replay.RoundTrip(mustRequest(t, http.MethodGet, "https://example.test/v1/result", ""))
	if err != nil {
		t.Fatalf("media snapshot was not copied: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if err := replay.AssertConsumed(); err != nil {
		t.Fatal(err)
	}

	policy.Keys["file"] = true
	policy.Allowed["inputs"] = true
	store, _ := privateStore(t)
	recorder := &wirefixture.Recorder{Policy: policy, Default: mediaMetadata(), Evidence: store}
	png := syntheticPNG(t)
	request := multipartRequest(t, multipartBody(t, "boundary-d", nil,
		[]multipartFile{{name: "file", filename: "canary.png", mediaType: "image/png", data: png}}), "boundary-d")
	_, _ = capture(t, recorder, request, jsonResponse(http.StatusOK, "application/json", `{}`))
	first := recorder.Exchanges()
	if len(first) != 1 || first[0].Request.Body.Multipart == nil {
		t.Fatalf("capture = %+v", first)
	}
	first[0].Request.Body.Multipart.Parts[0].Name = "changed"
	first[0].Request.Body.Multipart.Parts[0].Media.Length = 999
	first[0].Request.Body.Multipart.Parts[0].Media.Recipe = "png-1x1"
	first[0].Request.Body.Multipart.Parts[0].Media.Substituted = true
	second := recorder.Exchanges()
	if second[0].Request.Body.Multipart.Parts[0].Name != "file" ||
		second[0].Request.Body.Multipart.Parts[0].Media.Length != int64(len(png)) ||
		second[0].Request.Body.Multipart.Parts[0].Media.Recipe != "" ||
		second[0].Request.Body.Multipart.Parts[0].Media.Substituted {
		t.Fatalf("recorder snapshot mutated: %+v", second[0].Request.Body.Multipart)
	}
}

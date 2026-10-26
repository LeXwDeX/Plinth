package modelsource_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
	"github.com/LeXwDeX/Plinth/internal/assets/modelsource"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type previewNative struct {
	asset            generation.InputAsset
	body             io.ReadCloser
	openErr          error
	openCalls        int
	resolveCalls     int
	ordinaryOpenCall int
}

func (n *previewNative) Resolve(context.Context, generation.OwnerID, generation.Offering, json.RawMessage) (map[string][]generation.InputAsset, error) {
	n.resolveCalls++
	return nil, nil
}

func (n *previewNative) Open(context.Context, generation.InputAsset) (io.ReadCloser, error) {
	n.ordinaryOpenCall++
	return nil, errors.New("unexpected compatible open")
}

func (n *previewNative) OpenOwnedUpload(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	n.openCalls++
	return n.asset, n.body, n.openErr
}

type observedReadCloser struct {
	reader    io.Reader
	readBytes int
	closeErr  error
	closed    int
}

func (r *observedReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.readBytes += n
	return n, err
}
func (r *observedReadCloser) Close() error { r.closed++; return r.closeErr }

func previewAsset(raw []byte) generation.InputAsset {
	digest := sha256.Sum256(raw)
	return generation.InputAsset{ID: "upload-a", OwnerID: "owner-a", Size: int64(len(raw)), SHA256: hex.EncodeToString(digest[:])}
}

func previewService(t *testing.T, native *previewNative, limits modelinput.Policy) *modelsource.Service {
	t.Helper()
	service, err := modelsource.New(native, limits)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestOpenPreviewReturnsValidatedPrivateCopyFromSingleNativeOpen(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	body := &observedReadCloser{reader: bytes.NewReader(raw)}
	native := &previewNative{asset: previewAsset(raw), body: body}
	service := previewService(t, native, policy())

	asset, preview, err := service.OpenPreview(context.Background(), "owner-a", "upload-a")
	if err != nil {
		t.Fatal(err)
	}
	defer preview.Close()
	got, err := io.ReadAll(preview)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) || asset.ID != "upload-a" || asset.SHA256 != previewAsset(raw).SHA256 {
		t.Fatal("preview did not preserve the validated source bytes and identity")
	}
	if native.openCalls != 1 || native.resolveCalls != 0 || native.ordinaryOpenCall != 0 || body.closed != 1 {
		t.Fatalf("unexpected access path: native=%+v closed=%d", native, body.closed)
	}
	// The returned reader owns its independent byte buffer; changing the source
	// reader after OpenPreview cannot change bytes already returned to the caller.
	if !bytes.Equal(got, raw) {
		t.Fatal("returned preview bytes changed")
	}
}

func TestOpenPreviewRejectsOwnerIdentityAndMetadataMismatch(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	cases := []struct {
		name   string
		mutate func(*generation.InputAsset)
		owner  generation.OwnerID
		id     generation.AssetID
		code   generation.ErrorCode
	}{
		{name: "owner mismatch", mutate: func(a *generation.InputAsset) { a.OwnerID = "owner-b" }, owner: "owner-a", id: "upload-a", code: generation.ErrNotFound},
		{name: "id mismatch", mutate: func(a *generation.InputAsset) { a.ID = "upload-b" }, owner: "owner-a", id: "upload-a", code: generation.ErrNotFound},
		{name: "missing owner", mutate: func(*generation.InputAsset) {}, owner: "", id: "upload-a", code: generation.ErrNotFound},
		{name: "missing hash", mutate: func(a *generation.InputAsset) { a.SHA256 = "" }, owner: "owner-a", id: "upload-a", code: generation.ErrConflict},
		{name: "malformed hash", mutate: func(a *generation.InputAsset) { a.SHA256 = strings.Repeat("z", 64) }, owner: "owner-a", id: "upload-a", code: generation.ErrConflict},
		{name: "wrong hash", mutate: func(a *generation.InputAsset) { a.SHA256 = strings.Repeat("0", 64) }, owner: "owner-a", id: "upload-a", code: generation.ErrConflict},
		{name: "wrong size", mutate: func(a *generation.InputAsset) { a.Size++ }, owner: "owner-a", id: "upload-a", code: generation.ErrConflict},
		{name: "empty size", mutate: func(a *generation.InputAsset) { a.Size = 0 }, owner: "owner-a", id: "upload-a", code: generation.ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asset := previewAsset(raw)
			tc.mutate(&asset)
			body := &observedReadCloser{reader: bytes.NewReader(raw)}
			native := &previewNative{asset: asset, body: body}
			service := previewService(t, native, policy())
			_, preview, err := service.OpenPreview(context.Background(), tc.owner, tc.id)
			if preview != nil {
				preview.Close()
			}
			if !generation.IsCode(err, tc.code) {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
			if native.openCalls != 1 || body.closed != 1 {
				t.Fatalf("expected one open and close, got opens=%d closes=%d", native.openCalls, body.closed)
			}
			if tc.name == "owner mismatch" || tc.name == "id mismatch" || tc.name == "missing owner" || tc.name == "missing hash" || tc.name == "malformed hash" || tc.name == "empty size" {
				if body.readBytes != 0 {
					t.Fatalf("read bytes before trusted metadata checks: %d", body.readBytes)
				}
			}
		})
	}
}

func TestOpenPreviewRejectsInvalidAndExternalGLB(t *testing.T) {
	valid := modelinputtest.Triangle(modelinputtest.Options{})
	externalDoc, bin := modelinputtest.Document(modelinputtest.Options{})
	buffer := externalDoc["buffers"].([]any)[0].(map[string]any)
	buffer["uri"] = "https://example.invalid/model.bin"
	external := modelinputtest.GLB(externalDoc, bin)
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{name: "corrupt", raw: []byte("not a GLB")},
		{name: "external URI", raw: external},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &observedReadCloser{reader: bytes.NewReader(tc.raw)}
			native := &previewNative{asset: previewAsset(tc.raw), body: body}
			service := previewService(t, native, policy())
			_, preview, err := service.OpenPreview(context.Background(), "owner-a", "upload-a")
			if preview != nil {
				preview.Close()
			}
			want := generation.ErrInvalidModelInput
			if tc.name == "external URI" {
				want = generation.ErrUnsupportedModelInput
			}
			if !generation.IsCode(err, want) {
				t.Fatalf("expected %s, got %v", want, err)
			}
		})
	}
	if len(valid) == 0 {
		t.Fatal("invalid test fixture")
	}
}

func TestOpenPreviewBoundsReadsAndFailsClosedOnReadLifecycleErrors(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	tooLargePolicy := policy()
	tooLargePolicy.Limits.MaxBytes = int64(len(raw)) - 1
	tooLargePolicy.Limits.MaxJSONBytes = tooLargePolicy.Limits.MaxBytes - 16
	// Keep the test policy structurally valid while setting a small byte ceiling.
	tooLargePolicy.Limits.MaxJSONBytes = tooLargePolicy.Limits.MaxBytes
	cases := []struct {
		name     string
		ctx      func() (context.Context, func())
		reader   func(*testing.T) io.Reader
		closeErr error
		code     generation.ErrorCode
		maxRead  int
	}{
		{name: "oversize actual bytes", ctx: func() (context.Context, func()) { return context.Background(), func() {} }, reader: func(*testing.T) io.Reader { return bytes.NewReader(raw) }, code: generation.ErrModelInputLimit, maxRead: int(tooLargePolicy.Limits.MaxBytes + 1)},
		{
			name: "cancel during read",
			ctx: func() (context.Context, func()) {
				ctx, cancel := context.WithCancel(context.Background())
				return ctx, cancel
			},
			reader:  func(*testing.T) io.Reader { return bytes.NewReader(raw) },
			code:    generation.ErrUnavailable,
			maxRead: len(raw),
		},
		{name: "read error", ctx: func() (context.Context, func()) { return context.Background(), func() {} }, reader: func(*testing.T) io.Reader {
			return readerFunc(func([]byte) (int, error) { return 0, errors.New("read failed") })
		}, code: generation.ErrUnavailable, maxRead: 0},
		{name: "close error", ctx: func() (context.Context, func()) { return context.Background(), func() {} }, reader: func(*testing.T) io.Reader { return bytes.NewReader(raw) }, closeErr: errors.New("close failed"), code: generation.ErrUnavailable, maxRead: len(raw)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.ctx()
			defer cancel()
			reader := tc.reader(t)
			if tc.name == "cancel during read" {
				ctx, cancel = context.WithCancel(context.Background())
				reader = readerFunc(func(p []byte) (int, error) { n := copy(p, raw); cancel(); return n, nil })
			}
			body := &observedReadCloser{reader: reader, closeErr: tc.closeErr}
			limits := policy()
			if tc.name == "oversize actual bytes" {
				limits = tooLargePolicy
			}
			asset := previewAsset(raw)
			if tc.name == "oversize actual bytes" {
				asset.Size = limits.Limits.MaxBytes
			}
			native := &previewNative{asset: asset, body: body}
			service := previewService(t, native, limits)
			_, preview, err := service.OpenPreview(ctx, "owner-a", "upload-a")
			if preview != nil {
				preview.Close()
			}
			if !generation.IsCode(err, tc.code) {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
			if body.readBytes > tc.maxRead || body.closed != 1 || native.openCalls != 1 {
				t.Fatalf("read/close/open bounds violated: read=%d max=%d close=%d open=%d", body.readBytes, tc.maxRead, body.closed, native.openCalls)
			}
			if tc.name == "oversize actual bytes" && body.readBytes != tc.maxRead {
				t.Fatalf("expected bounded read to stop at max+1 (%d), read %d", tc.maxRead, body.readBytes)
			}
		})
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

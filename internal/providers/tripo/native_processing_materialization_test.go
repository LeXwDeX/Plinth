package tripo

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func nativeBoundGLB(label string) []byte {
	document := []byte(`{"asset":{"version":"2.0"},"extras":{"label":"` + label + `"}}`)
	for len(document)%4 != 0 {
		document = append(document, ' ')
	}
	result := make([]byte, 20+len(document))
	copy(result, "glTF")
	binary.LittleEndian.PutUint32(result[4:], 2)
	binary.LittleEndian.PutUint32(result[8:], uint32(len(result)))
	binary.LittleEndian.PutUint32(result[12:], uint32(len(document)))
	copy(result[16:], "JSON")
	copy(result[20:], document)
	return result
}
func nativeBoundMetadata(body []byte) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"native_conversion_source": map[string]any{"version": 1, "sha256": uploadedDigest(body), "size": len(body)}})
	return raw
}
func nativeBoundClaim() generation.ArtifactClaim {
	return generation.ArtifactClaim{ID: "source", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", Locator: "https://assets.example/original.glb"}
}
func nativeBoundStore(t *testing.T, body io.Reader) (generation.Asset, error, string) {
	t.Helper()
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	asset, err := store.Put(context.Background(), generation.AssetWrite{AssetID: "asset", OwnerID: "owner", TaskID: "task", AttemptID: "attempt", Role: "model", Variant: "glb", MediaType: "model/gltf-binary", MaxBytes: 1 << 20, Body: body})
	return asset, err, root
}
func assertNativeBoundIntegrity(t *testing.T, err error) {
	t.Helper()
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil || provider.Failure.Class != generation.FailureIntegrity {
		t.Fatalf("want integrity failure, got %v", err)
	}
}
func assertNativeBoundNoFiles(t *testing.T, root string) {
	t.Helper()
	for _, dir := range []string{"tmp", "blobs"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(_ string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() {
				t.Error("rejected source left a temporary or CAS file")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeBoundSourceStoreRejectsSameURLChangedValidGLB(t *testing.T) {
	original, changed := nativeBoundGLB("A"), nativeBoundGLB("B")
	// Establish that B is independently valid; only its frozen identity differs.
	if _, err, _ := nativeBoundStore(t, bytes.NewReader(changed)); err != nil {
		t.Fatalf("alternate GLB invalid: %v", err)
	}
	for _, kind := range []string{"exact", "changed", "extra-byte", "short"} {
		t.Run(kind, func(t *testing.T) {
			current := original
			sourceReads, legacyReads := 0, 0
			a := &Adapter{nativeSourceClient: nativeDeliveryTestSourceClient(func() []byte { sourceReads++; return current }, nil)}
			// The old OpenAsset path would accept the changed bytes and must not be used.
			dummy := httptest.NewServer(http.NotFoundHandler())
			defer dummy.Close()
			a.client = rigAdapter(t, dummy).client
			a.client.HTTPClient = nativeDeliveryTestSourceClient(func() []byte { legacyReads++; return current }, nil)
			r := nativeDeliveryFixture(generation.Rigging)
			_, options, err := nativeProcessingDeclaration(r.Offering)
			if err != nil {
				t.Fatal(err)
			}
			claim := nativeBoundClaim()
			frozen, err := a.bindNativeConversionSource(context.Background(), r.Offering, claim, options.NativeConversion)
			if err != nil {
				t.Fatal(err)
			}
			if frozen.SHA256 != uploadedDigest(original) {
				t.Fatal("wrong source binding")
			}
			claim.Metadata = nativeBoundMetadata(original)
			if kind == "changed" {
				current = changed
			}
			if kind == "extra-byte" {
				current = append(bytes.Clone(original), 'X')
			}
			if kind == "short" {
				current = original[:len(original)-1]
			}
			reader, _, err := a.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: r.Offering, Claim: claim})
			if err != nil {
				if kind == "exact" {
					t.Fatal(err)
				}
				assertNativeBoundIntegrity(t, err)
				return
			}
			asset, putErr, root := nativeBoundStore(t, reader)
			// The copy must reject before Close; Close cannot prevent Store's CAS commit.
			if kind == "exact" {
				if putErr != nil || asset.SHA256 != frozen.SHA256 || asset.Size != frozen.Size {
					t.Fatalf("exact source rejected %+v %v", asset, putErr)
				}
			} else {
				if putErr == nil || asset.ID != "" {
					t.Fatalf("different bytes became a stored asset sha=%s", asset.SHA256)
				}
				assertNativeBoundIntegrity(t, putErr)
				assertNativeBoundNoFiles(t, root)
			}
			reader.Close()
			if legacyReads != 0 || sourceReads < 2 {
				t.Fatal("materialization bypassed anonymous pinned source client")
			}
		})
	}
}

func TestNativeBoundSourceRequiresStrictMetadataBeforeGET(t *testing.T) {
	original := nativeBoundGLB("A")
	hash := uploadedDigest(original)
	size := len(original)
	for _, raw := range []string{"", `null`, `{}`, `{"native_conversion_source":null}`, fmt.Sprintf(`{"native_conversion_source":{"version":2,"sha256":%q,"size":%d}}`, hash, size), fmt.Sprintf(`{"native_conversion_source":{"version":1,"sha256":"invalid","size":%d}}`, size), fmt.Sprintf(`{"native_conversion_source":{"version":1,"sha256":%q,"size":0}}`, hash), fmt.Sprintf(`{"native_conversion_source":{"version":1,"sha256":%q,"size":150000001}}`, hash), fmt.Sprintf(`{"native_conversion_source":{"version":1,"sha256":%q,"size":%d,"endpoint":"https://other.example"}}`, hash, size), fmt.Sprintf(`{"native_conversion_source":{"version":1,"sha256":%q,"size":%d},"configuration":{}}`, hash, size), fmt.Sprintf(`{"native_conversion_source":{"version":1,"sha256":%q,"sha256":%q,"size":%d}}`, hash, hash, size)} {
		t.Run(fmt.Sprint(len(raw))+raw[:min(len(raw), 10)], func(t *testing.T) {
			calls := 0
			a := &Adapter{nativeSourceClient: nativeDeliveryTestSourceClient(func() []byte { calls++; return original }, nil)}
			dummy := httptest.NewServer(http.NotFoundHandler())
			defer dummy.Close()
			a.client = rigAdapter(t, dummy).client
			a.client.HTTPClient = a.nativeSourceClient
			r := nativeDeliveryFixture(generation.Rigging)
			claim := nativeBoundClaim()
			claim.Metadata = json.RawMessage(raw)
			reader, _, err := a.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: r.Offering, Claim: claim})
			if reader != nil {
				reader.Close()
			}
			if err == nil {
				t.Fatal("unbound native source opened")
			}
			assertNativeBoundIntegrity(t, err)
			if calls != 0 {
				t.Fatal("invalid metadata reached network")
			}
		})
	}
}

func TestNativeBoundSourceRefreshPreservesFrozenIdentity(t *testing.T) {
	current := nativeBoundGLB("A")
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			id := "task_processed"
			if strings.HasSuffix(r.URL.Path, "models/convert") {
				id = "child_fbx"
			}
			fmt.Fprintf(w, `{"code":0,"data":{"task_id":%q}}`, id)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v3/tasks/")
		locator := "https://assets.example/original.glb"
		if id == "child_fbx" {
			locator = "https://assets.example/result.fbx"
		}
		fmt.Fprintf(w, `{"code":0,"data":{"task_id":%q,"status":"success","output":{"model_url":%q}}}`, id, locator)
	}))
	defer server.Close()
	a := rigAdapter(t, server)
	a.nativeSourceClient = nativeDeliveryTestSourceClient(func() []byte { return current }, nil)
	r := nativeDeliveryFixture(generation.Rigging)
	ctx := generation.WithDispatchCheckpointer(context.Background(), &nativeDeliveryPermit{})
	accepted, err := a.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	q := rigObserve(r, accepted)
	var completed generation.Observation
	for range 3 {
		completed, err = a.Observe(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		q.Opaque = completed.Opaque
	}
	if completed.Phase != generation.UpstreamCompleted || !bytes.Equal(completed.Artifacts[0].Metadata, nativeBoundMetadata(current)) {
		t.Fatalf("completed claim missing frozen byte marker %s", completed.Artifacts[0].Metadata)
	}
	refreshed, err := a.Observe(context.Background(), q)
	if err != nil || !bytes.Equal(refreshed.Artifacts[0].Metadata, completed.Artifacts[0].Metadata) {
		t.Fatal("refresh changed/missed original binding")
	}
	current = nativeBoundGLB("B")
	if _, err := a.Observe(context.Background(), q); err == nil || posts != 2 {
		t.Fatal("refresh re-froze changed source or repeated paid submission")
	}
}

type nativeFinalEOFBody struct {
	*bytes.Reader
	read, closes int
}

func (b *nativeFinalEOFBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	if b.Len() == 0 {
		err = io.EOF
	}
	return n, err
}
func (b *nativeFinalEOFBody) Close() error { b.closes++; return nil }

func TestNativeBoundSourceEOFWithBytesUnknownLengthAndImmediateBound(t *testing.T) {
	original := nativeBoundGLB("A")
	for _, kind := range []string{"exact", "changed", "long", "short"} {
		t.Run(kind, func(t *testing.T) {
			current := original
			if kind == "changed" {
				current = nativeBoundGLB("B")
			}
			if kind == "long" {
				current = append(bytes.Clone(original), make([]byte, 4096)...)
			}
			if kind == "short" {
				current = current[:len(current)-1]
			}
			underlying := &nativeFinalEOFBody{Reader: bytes.NewReader(current)}
			a := &Adapter{nativeSourceClient: &http.Client{Transport: nativeRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, ContentLength: -1, Body: underlying}, nil
			})}}
			r := nativeDeliveryFixture(generation.Rigging)
			claim := nativeBoundClaim()
			claim.Metadata = nativeBoundMetadata(original)
			reader, _, err := a.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: r.Offering, Claim: claim})
			if err != nil {
				t.Fatal(err)
			}
			asset, err, root := nativeBoundStore(t, reader)
			if kind == "exact" {
				if err != nil || asset.SHA256 != uploadedDigest(original) {
					t.Fatalf("EOF with final bytes rejected %v", err)
				}
			} else {
				if err == nil || asset.ID != "" {
					t.Fatal("bad unknown-length stream committed")
				}
				assertNativeBoundIntegrity(t, err)
				assertNativeBoundNoFiles(t, root)
			}
			if underlying.closes != 0 {
				t.Fatal("verification relied on Close")
			}
			if underlying.read > len(original)+1 {
				t.Fatal("source consumed beyond one excess byte")
			}
			_, again := reader.Read(make([]byte, 1))
			if kind == "exact" {
				if again != io.EOF {
					t.Fatal("verified EOF not stable")
				}
			} else {
				assertNativeBoundIntegrity(t, again)
			}
			reader.Close()
			if underlying.closes != 1 {
				t.Fatal("source body not closed")
			}
		})
	}
}

func TestNativeBoundSourceHistoricalAndOtherSlotsRetainOpener(t *testing.T) {
	for _, kind := range []string{"historical-native", "other-provider", "native-child"} {
		t.Run(kind, func(t *testing.T) {
			r := nativeDeliveryFixture(generation.Rigging)
			claim := nativeBoundClaim()
			if kind == "historical-native" {
				var options map[string]any
				json.Unmarshal(r.Offering.ProviderOptions, &options)
				delete(options, "native_conversion")
				delete(options, "convert_endpoint")
				r.Offering.ProviderOptions, _ = json.Marshal(options)
			}
			if kind == "other-provider" {
				r.Offering.Capability = generation.TextTo3D
			}
			if kind == "native-child" {
				claim.Variant = "fbx"
				claim.MediaType = "model/fbx"
			}
			dummy := httptest.NewServer(http.NotFoundHandler())
			defer dummy.Close()
			a := rigAdapter(t, dummy)
			calls := 0
			a.client.HTTPClient = nativeDeliveryTestSourceClient(func() []byte { calls++; return nativeBoundGLB("A") }, nil)
			a.nativeSourceClient = &http.Client{Transport: nativeRoundTrip(func(*http.Request) (*http.Response, error) {
				t.Fatal("historical/other claim used bound source opener")
				return nil, errors.New("unexpected")
			})}
			reader, _, err := a.OpenAsset(context.Background(), generation.OpenAssetRequest{Offering: r.Offering, Claim: claim})
			if err != nil {
				t.Fatal(err)
			}
			reader.Close()
			if calls != 1 {
				t.Fatal("legacy opener changed")
			}
		})
	}
}

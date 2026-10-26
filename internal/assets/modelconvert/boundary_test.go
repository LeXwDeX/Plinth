package modelconvert

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

func TestReverseURIPositionsPreserveRemainingJSONAndPayload(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{})
	raw := modelinputtest.GLB(d, b)
	j, _ := parts(raw)
	uri := fmt.Sprintf(`"uri":%q`, bufferPrefix+base64.StdEncoding.EncodeToString(b))
	for name, buffer := range map[string]string{"first": "{ " + uri + `, "byteLength":36, "name":"buffer" }`, "middle": `{ "byteLength":36, ` + uri + `, "name":"buffer" }`, "last": `{ "byteLength":36, "name":"buffer", ` + uri + " }"} {
		t.Run(name, func(t *testing.T) {
			gltf := bytes.Replace(j, []byte(`{"byteLength":36}`), []byte(buffer), 1)
			out, err := GLTFToGLB(context.Background(), gltf, testPolicy(), testLimits())
			if err != nil {
				t.Fatal(err)
			}
			gotJ, gotB := parts(out)
			got := document(t, gotJ)
			want := document(t, gltf)
			delete(want["buffers"].([]any)[0].(map[string]any), "uri")
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(mustJSON(t, got), wantJSON) {
				t.Fatal("member removal changed sibling fields")
			}
			if !bytes.Equal(logical(t, gotJ, gotB), b) {
				t.Fatal("payload changed")
			}
		})
	}
}
func mustJSON(t *testing.T, d map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLargeChunkedRoundTripAndLogicalPadding(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{Indexed: true, Textured: true})
	b = append(b, bytes.Repeat([]byte{0x5a}, 100003)...)
	d["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(b)
	raw := modelinputtest.GLB(d, b)
	gltf, err := GLBToGLTF(context.Background(), raw, testPolicy(), testLimits())
	if err != nil {
		t.Fatal(err)
	}
	back, err := GLTFToGLB(context.Background(), gltf, testPolicy(), testLimits())
	if err != nil {
		t.Fatal(err)
	}
	j, bb := parts(back)
	if !bytes.Equal(logical(t, j, bb), b) {
		t.Fatal("chunked encoder/decoder changed logical bytes")
	}
}

func TestDamagedGLBAndJSONRejectedWithoutPanicOrMutation(t *testing.T) {
	good := modelinputtest.Triangle(modelinputtest.Options{})
	j, b := parts(good)
	altered := func(at int, value uint32) []byte {
		raw := bytes.Clone(good)
		binary.LittleEndian.PutUint32(raw[at:], value)
		return raw
	}
	cases := map[string][]byte{"wrong-total": altered(8, 0), "oversized-json-chunk": altered(12, ^uint32(0)), "nonaligned-json": altered(12, 5), "wrong-bin-size": altered(20+len(good[20:20+int(binary.LittleEndian.Uint32(good[12:]))]), 4), "trailing-json": modelinputtest.Frame(append(bytes.Clone(j), []byte(` {}`)...), b), "duplicate-json": modelinputtest.Frame(append([]byte(`{"scene":0,`), j[1:]...), b)}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) { assertFailure(t, GLBToGLTF, raw, testPolicy(), testLimits(), modelinput.Invalid) })
	}
	for name, raw := range map[string][]byte{"empty": nil, "primitive": []byte(`42`), "array": []byte(`[]`), "trailing": append(selfContained(t, good), 'x'), "bad-string": []byte(`{"buffers":[{"byteLength":36,"uri":"\q"}]}`), "invalid-utf8": append([]byte{'"', 0xff}, '"')} {
		t.Run(name, func(t *testing.T) { assertFailure(t, GLTFToGLB, raw, testPolicy(), testLimits(), modelinput.Invalid) })
	}
}

func TestPolicyParsingAndSupportedStructureBounds(t *testing.T) {
	d, b := modelinputtest.Document(modelinputtest.Options{})
	d["extras"] = map[string]any{"nested": map[string]any{"value": 1}}
	raw := modelinputtest.GLB(d, b)
	gltf := selfContained(t, raw)
	for name, adjust := range map[string]func(*modelinput.Policy){"depth": func(p *modelinput.Policy) { p.Limits.MaxDepth = 2 }, "elements": func(p *modelinput.Policy) { p.Limits.MaxElements = 5 }, "json": func(p *modelinput.Policy) { p.Limits.MaxJSONBytes = 32 }, "bytes": func(p *modelinput.Policy) { p.Limits.MaxBytes = 100; p.Limits.MaxJSONBytes = 100 }} {
		t.Run(name, func(t *testing.T) {
			p := testPolicy()
			adjust(&p)
			assertFailure(t, GLBToGLTF, raw, p, testLimits(), modelinput.Limit)
			assertFailure(t, GLTFToGLB, gltf, p, testLimits(), modelinput.Limit)
		})
	}
	for name, modify := range map[string]func(map[string]any){"morph": func(d map[string]any) {
		d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["targets"] = []any{}
	}, "unknown-structure": func(d map[string]any) { d["unrecognized"] = true }, "image-data-uri": func(d map[string]any) {
		d["images"] = []any{map[string]any{"uri": "data:image/png;base64,AAAA", "mimeType": "image/png"}}
	}, "external-buffer": func(d map[string]any) { d["buffers"].([]any)[0].(map[string]any)["uri"] = "model.bin" }} {
		t.Run(name, func(t *testing.T) {
			d, b := modelinputtest.Document(modelinputtest.Options{})
			modify(d)
			assertFailure(t, GLBToGLTF, modelinputtest.GLB(d, b), testPolicy(), testLimits(), modelinput.Unsupported)
			if name != "external-buffer" {
				assertFailure(t, GLTFToGLB, selfContained(t, modelinputtest.GLB(d, b)), testPolicy(), testLimits(), modelinput.Unsupported)
			}
		})
	}
	p := testPolicy()
	p.Revision = "unknown"
	assertFailure(t, GLBToGLTF, raw, p, testLimits(), modelinput.InvalidPolicy)
	assertFailure(t, GLTFToGLB, gltf, p, testLimits(), modelinput.InvalidPolicy)
}

// This context cancels after real parser/encoder/validator checkpoints, making
// in-flight cancellation deterministic instead of relying on scheduler timing.
type checkpointContext struct{ calls, cancelAt int }

func (c *checkpointContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *checkpointContext) Done() <-chan struct{}       { return nil }
func (c *checkpointContext) Value(any) any               { return nil }
func (c *checkpointContext) Err() error {
	c.calls++
	if c.cancelAt > 0 && c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func TestCancellationAfterWorkStarted(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Textured: true, Interleaved: true})
	gltf := selfContained(t, raw)
	for name, fn := range map[string]func(context.Context, []byte, modelinput.Policy, Limits) ([]byte, error){"forward": GLBToGLTF, "reverse": GLTFToGLB} {
		t.Run(name, func(t *testing.T) {
			input := raw
			if name == "reverse" {
				input = gltf
			}
			baseline := &checkpointContext{}
			if _, err := fn(baseline, input, testPolicy(), testLimits()); err != nil {
				t.Fatal(err)
			}
			for _, at := range []int{2, baseline.calls / 2, baseline.calls - 1} {
				ctx := &checkpointContext{cancelAt: at}
				out, err := fn(ctx, input, testPolicy(), testLimits())
				var failure *modelinput.Error
				if out != nil || !errors.As(err, &failure) || failure.Kind != modelinput.Canceled {
					t.Fatalf("checkpoint %d/%d got %d %v", at, baseline.calls, len(out), err)
				}
			}
		})
	}
}

func TestContainerURIOverheadDoesNotConsumeModelElementAllowance(t *testing.T) {
	raw := modelinputtest.Triangle(modelinputtest.Options{})
	j, _ := parts(raw)
	var count func(any) int64
	count = func(v any) int64 {
		n := int64(1)
		switch v := v.(type) {
		case map[string]any:
			for _, child := range v {
				n += 1 + count(child)
			}
		case []any:
			for _, child := range v {
				n += count(child)
			}
		}
		return n
	}
	p := testPolicy()
	p.Limits.MaxElements = count(document(t, j))
	if _, err := modelinput.ValidateGLBBytes(context.Background(), raw, p); err != nil {
		t.Fatalf("fixture must use exact legal model element boundary: %v", err)
	}
	gltf, err := GLBToGLTF(context.Background(), raw, p, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = GLTFToGLB(context.Background(), gltf, p, testLimits()); err != nil {
		t.Fatalf("container-only URI must not make a legal model fail round trip: %v", err)
	}
}

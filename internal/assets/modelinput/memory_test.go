package modelinput_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

func TestMemoryValidationMatchesOwnedFileContract(t *testing.T) {
	cases := []struct {
		name   string
		raw    []byte
		policy modelinput.Policy
	}{
		{"plain", modelinputtest.Triangle(modelinputtest.Options{}), policy()},
		{"textured_interleaved", modelinputtest.Triangle(modelinputtest.Options{Indexed: true, Interleaved: true, MultiplePrimitives: true, Textured: true}), policy()},
		{"empty", nil, policy()},
		{"short_header", []byte("glTF"), policy()},
	}
	for _, name := range []string{"external_metadata", "bad_index", "external_buffer", "duplicate_json", "work_limit", "byte_limit"} {
		doc, bin := modelinputtest.Document(modelinputtest.Options{Indexed: true})
		p := policy()
		switch name {
		case "external_metadata":
			doc["asset"].(map[string]any)["extras"] = map[string]any{"source": "https://invalid.example/private"}
		case "bad_index":
			bin[36], bin[37] = 99, 0
		case "external_buffer":
			doc["buffers"].([]any)[0].(map[string]any)["uri"] = "../escape.bin"
		case "work_limit":
			p.Limits.MaxWork = 1
		case "byte_limit":
			p.Limits.MaxBytes = 128
			p.Limits.MaxJSONBytes = 128
		}
		raw := modelinputtest.GLB(doc, bin)
		if name == "duplicate_json" {
			jsonLength := int(raw[12]) | int(raw[13])<<8 | int(raw[14])<<16 | int(raw[15])<<24
			j := bytes.Replace(raw[20:20+jsonLength], []byte(`"version":"2.0"`), []byte(`"version":"2.0","version":"2.0"`), 1)
			raw = modelinputtest.Frame(j, bin)
		}
		cases = append(cases, struct {
			name   string
			raw    []byte
			policy modelinput.Policy
		}{name, raw, p})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := bytes.Clone(tc.raw)
			shouldPass := tc.name == "plain" || tc.name == "textured_interleaved"
			want, wantErr := validate(t, tc.raw, tc.policy)
			got, gotErr := modelinput.ValidateGLBBytes(context.Background(), tc.raw, tc.policy)
			if (gotErr == nil) != shouldPass {
				t.Fatalf("want accepted=%v; got %v", shouldPass, gotErr)
			}
			if (wantErr == nil) != (gotErr == nil) || want != got {
				t.Fatalf("file=(%+v,%v), bytes=(%+v,%v)", want, wantErr, got, gotErr)
			}
			if wantErr != nil {
				var we, ge *modelinput.Error
				if !errors.As(wantErr, &we) || !errors.As(gotErr, &ge) || we.Kind != ge.Kind || we.Reason != ge.Reason {
					t.Fatalf("different rejection: file=%v bytes=%v", wantErr, gotErr)
				}
			}
			if !bytes.Equal(before, tc.raw) {
				t.Fatal("input bytes changed")
			}
		})
	}
}

func TestMemoryValidationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := modelinput.ValidateGLBBytes(ctx, modelinputtest.Triangle(modelinputtest.Options{}), policy())
	var e *modelinput.Error
	if !errors.As(err, &e) || e.Kind != modelinput.Canceled {
		t.Fatalf("want canceled: %v", err)
	}
}

func TestMemoryValidationPreservesArchiveDetection(t *testing.T) {
	doc, bin := modelinputtest.Document(modelinputtest.Options{})
	bin = append(bin, []byte{'P', 'K', 5, 6}...)
	bin = append(bin, make([]byte, 24)...)
	doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = len(bin)
	raw := modelinputtest.GLB(doc, bin)
	file, err := os.CreateTemp(t.TempDir(), "polyglot-*.glb")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.Write(raw); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if err = local.ValidateDeliveredFile(file, "model", "glb", "model/gltf-binary"); err == nil {
		t.Fatal("delivery must reject an archive-like GLB")
	}
	if _, err = modelinput.ValidateGLBBytes(context.Background(), raw, policy()); err == nil {
		t.Fatal("byte validation bypassed archive detection")
	}
	if _, err = validate(t, raw, policy()); err == nil {
		t.Fatal("file validation bypassed archive detection")
	}
}

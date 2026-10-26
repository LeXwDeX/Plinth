package local_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
)

func validatedTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		result[path] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestValidatedBlobRejectsBeforeCAS(t *testing.T) {
	for _, mode := range []string{"reject", "rewrite", "replace", "cancel", "nil_body", "nil_callback", "empty", "oversize", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			store, err := local.New(root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			before := validatedTree(t, root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var body io.Reader = bytes.NewReader([]byte("ORIGINAL"))
			limit := int64(100)
			callback := func(f *os.File) error {
				info, err := f.Stat()
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0600 {
					t.Errorf("staging mode=%v", info.Mode())
				}
				if _, err := f.Write([]byte("x")); err == nil {
					t.Error("validator received writable file")
				}
				switch mode {
				case "reject":
					return &modelinput.Error{Kind: modelinput.Unsupported, Reason: "uri"}
				case "rewrite":
					return os.WriteFile(f.Name(), []byte("MUTATION"), 0600)
				case "replace":
					if err := os.Remove(f.Name()); err != nil {
						return err
					}
					return os.WriteFile(f.Name(), []byte("ORIGINAL"), 0600)
				case "cancel":
					cancel()
				}
				return nil
			}
			switch mode {
			case "nil_body":
				body = nil
			case "nil_callback":
				callback = nil
			case "empty":
				body = bytes.NewReader(nil)
			case "oversize":
				limit = 2
			case "overflow":
				limit = math.MaxInt64
			}
			_, err = store.PutValidatedBlob(ctx, body, "application/octet-stream", limit, callback)
			if err == nil {
				t.Fatal("invalid validated upload accepted")
			}
			if mode == "reject" {
				var domain *modelinput.Error
				if !errors.As(err, &domain) || domain.Kind != modelinput.Unsupported {
					t.Fatalf("typed error lost: %v", err)
				}
			}
			if !reflect.DeepEqual(before, validatedTree(t, root)) {
				t.Fatal("rejected validation changed CAS or left staging bytes")
			}
		})
	}
}
func TestValidatedBlobCommitsExactValidatedBytes(t *testing.T) {
	root := t.TempDir()
	store, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw := []byte("exact immutable bytes")
	calls := 0
	blob, err := store.PutValidatedBlob(context.Background(), bytes.NewReader(raw), "application/octet-stream", 100, func(f *os.File) error {
		calls++
		got, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, raw) {
			t.Fatal("validator saw other bytes")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || blob.Size != int64(len(raw)) {
		t.Fatalf("calls=%d blob=%+v", calls, blob)
	}
	f, err := store.OpenBlob(context.Background(), blob.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if !bytes.Equal(got, raw) {
		t.Fatal("committed bytes differ")
	}
}

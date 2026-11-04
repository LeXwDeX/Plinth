package localparts

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func legacyCombinedDigest(files []outputFile, bodies [][]byte) string {
	hasher := sha256.New()
	var length [8]byte
	for index, file := range files {
		values := []string{file.role, file.variant, file.mediaType, strconv.Itoa(file.ordinal), digest(bodies[index])}
		for _, value := range values {
			binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
			hasher.Write(length[:])
			hasher.Write([]byte(value))
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func TestCombinedDigestMatchesOriginalAlgorithm(t *testing.T) {
	bodies := [][]byte{[]byte("first-part-body"), []byte(`{"version":1,"parts":[]}`)}
	files := []outputFile{
		newOutputFile("model", "glb", modelMIME, 1, bodies[0]),
		newOutputFile(manifestRole, manifestVariant, manifestMIME, 1, bodies[1]),
	}
	digests := newCombinedDigest()
	for _, file := range files {
		digests.add(file)
	}
	if got, want := digests.sum(), legacyCombinedDigest(files, bodies); got != want {
		t.Fatalf("combined digest = %s, want %s", got, want)
	}
}

func TestOutputFileRetainsMetadataOnly(t *testing.T) {
	typ := reflect.TypeOf(outputFile{})
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.Name == "body" {
			t.Fatal("outputFile still retains the full output body")
		}
		if field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Uint8 {
			t.Fatalf("outputFile field %q retains raw bytes", field.Name)
		}
	}
}

func TestVerdictKeyBindsAssetIdentity(t *testing.T) {
	offering := generation.Offering{ID: "local/model-parts/split", Revision: "1", ProviderOptions: json.RawMessage(`{}`)}
	input := json.RawMessage(`{"source_asset":"asset"}`)
	base := generation.InputAsset{
		ID: "asset", OwnerID: "owner", Role: "model", Variant: "glb",
		TaskID: "task", SessionID: "session", SHA256: strings.Repeat("a", 64),
	}
	key := verdictKey(offering, input, []generation.InputAsset{base})
	same := base
	if got := verdictKey(offering, input, []generation.InputAsset{same}); got != key {
		t.Fatalf("identical asset identity produced a different key: %s != %s", got, key)
	}
	changes := map[string]func(*generation.InputAsset){
		"asset ID": func(asset *generation.InputAsset) { asset.ID = "other-asset" },
		"owner":    func(asset *generation.InputAsset) { asset.OwnerID = "other-owner" },
		"role":     func(asset *generation.InputAsset) { asset.Role = "thumbnail" },
		"variant":  func(asset *generation.InputAsset) { asset.Variant = "png" },
		"task":     func(asset *generation.InputAsset) { asset.TaskID = "other-task" },
		"session":  func(asset *generation.InputAsset) { asset.SessionID = "other-session" },
	}
	for name, change := range changes {
		changed := base
		change(&changed)
		if got := verdictKey(offering, input, []generation.InputAsset{changed}); got == key {
			t.Fatalf("changing %s did not change the verdict key", name)
		}
	}
}

func TestSplitDedupeKeyMatchesStoredBytes(t *testing.T) {
	raw := twoCubesAndFragment(t)
	offering := loadTestOffering(t, "local/model-parts/split")
	source := sourceAsset(t, "dedupe-source", "source-task", raw)
	inputs := newMemoryInputs(map[generation.AssetID][]byte{source.ID: raw})
	store := newMemoryStore()
	adapter := newTestAdapter(t, inputs, store)
	input := splitInputJSON(t, string(source.ID), "primitive", 0.002, 32)
	observation, err := adapter.Start(context.Background(), generation.StartRequest{
		SessionID: testSession, OwnerID: testOwner, TaskID: "split-task", AttemptID: "attempt-1",
		Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{"source_asset": {source}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	hasher := sha256.New()
	var length [8]byte
	for index, claim := range observation.Artifacts {
		body := store.bodies[claim.StoredAsset.ID]
		values := []string{claim.Role, claim.Variant, claim.MediaType, strconv.Itoa(index + 1), digest(body)}
		if claim.Role == manifestRole {
			values[3] = "1"
		}
		for _, value := range values {
			binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
			hasher.Write(length[:])
			hasher.Write([]byte(value))
		}
	}
	want := hex.EncodeToString(hasher.Sum(nil))
	if observation.DedupeKey != want {
		t.Fatalf("dedupe key = %s, want %s", observation.DedupeKey, want)
	}
}

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// #665 wave 4: the fingerprint keeps its value for every Skill the packaged
// fingerprints were made from, and a field added to SkillSchema later changes
// it only when the field is set.
func TestRetiredSkillFingerprintIgnoresNewZeroFields665(t *testing.T) {
	skill := SkillSchema{ID: "skill", Name: "Name", Category: "c", ThumbnailPath: "/t.png", RawImageURL: "/t.png",
		Prompt: "p", Tags: []string{"x"}}
	encoded, _ := json.Marshal(skill)
	digest := sha256.Sum256(encoded)
	if got, err := RetiredSkillFingerprint(skill); err != nil || got != hex.EncodeToString(digest[:]) {
		t.Fatalf("fingerprint = %s, %v; want the digest of the complete encoding", got, err)
	}
	frozen := encoded
	withZero := append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"new_field":"","new_flag":false}`)...)
	withValue := append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"new_field":"set"}`)...)
	base, _ := skillFingerprint(frozen, encoded)
	if got, _ := skillFingerprint(frozen, withZero); got != base {
		t.Fatal("a new field at its zero value changed the fingerprint")
	}
	if got, _ := skillFingerprint(frozen, withValue); got == base {
		t.Fatal("a new field with a value kept the packaged fingerprint")
	}
}

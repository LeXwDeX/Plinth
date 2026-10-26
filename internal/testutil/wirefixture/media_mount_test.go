package wirefixture

import (
	"errors"
	"testing"
)

func TestRejectBindMountOverlap(t *testing.T) {
	records, err := parseMountInfo([]byte(
		"36 35 98:0 /worktree/private /tmp/evidence rw,noatime master:1 - ext4 /dev/sda1 rw\n" +
			"37 35 98:0 / /tmp/independent rw - ext4 /dev/sda2 rw\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectBindMountOverlap(records, "/worktree", "/tmp/evidence"); !errors.Is(err, ErrInsideWorktree) {
		t.Fatalf("bind mount overlap error = %v", err)
	}
	if err := rejectBindMountOverlap(records, "/worktree", "/tmp/independent"); err != nil {
		t.Fatalf("independent mount rejected: %v", err)
	}
	if _, err := parseMountInfo([]byte("not mountinfo\n")); err == nil {
		t.Fatal("malformed mountinfo accepted")
	}
}

func TestUnescapeMountPath(t *testing.T) {
	value, err := unescapeMountPath(`/tmp/private\040name`)
	if err != nil || value != "/tmp/private name" {
		t.Fatalf("mount escape = %q %v", value, err)
	}
	if _, err := unescapeMountPath(`/tmp/private\001name`); err == nil {
		t.Fatal("unknown mount escape accepted")
	}
}

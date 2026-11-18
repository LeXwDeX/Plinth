package app

import (
	"context"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assetlist"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestAssetListSubmissionGateIsNotInstalledWithoutExecution(t *testing.T) {
	t.Parallel()
	service := &assetlist.Service{}
	if gate := newAssetListSubmissionGate(nil, nil); gate != nil {
		t.Fatal("gate installed without an asset list section")
	}
	if gate := newAssetListSubmissionGate(service, &catalog.AssetListSchema{}); gate != nil {
		t.Fatal("gate installed without an execution section")
	}
}

func TestAssetListSubmissionGateAllowsWithoutAnItem(t *testing.T) {
	t.Parallel()
	schema := &catalog.AssetListSchema{
		Execution: &catalog.AssetListExecutionSchema{Enabled: true},
	}
	gate := newAssetListSubmissionGate(&assetlist.Service{}, schema)
	if gate == nil {
		t.Fatal("gate not installed for a declared execution section")
	}
	// The zero service has no run store, so the session is ordinary; an
	// unassembled gate must keep the tool path permissive.
	if message := gate(context.Background(), chat.ToolCommand{
		OwnerID: "owner", SessionID: "ses_plain",
	}); message != "" {
		t.Fatalf("ordinary session refused with %q", message)
	}
}

func TestAssetListSubmissionMessageUsesSchemaCopy(t *testing.T) {
	t.Parallel()
	messages := catalog.AssetListMessagesSchema{
		PausePresence: "presence-copy",
		PauseUser:     "user-copy",
		PauseStepCap:  "step-cap-copy",
		PauseFailure:  "failure-copy",
		Stopped:       "stopped-copy",
	}
	for reason, want := range map[string]string{
		assetlist.PausePresence: "presence-copy",
		assetlist.PauseUser:     "user-copy",
		assetlist.PauseStepCap:  "step-cap-copy",
		assetlist.PauseFailure:  "failure-copy",
		assetlist.RunStopped:    "stopped-copy",
		"completed":             "stopped-copy",
	} {
		if got := assetListSubmissionMessage(messages, reason); got != want {
			t.Fatalf("reason %q = %q; want %q", reason, got, want)
		}
	}
	// A historical Catalog with empty copy still refuses with a non-empty
	// fallback; an empty answer would read as allowed.
	for _, reason := range []string{
		assetlist.PausePresence, assetlist.PauseUser, assetlist.PauseStepCap,
		assetlist.PauseFailure, assetlist.RunStopped,
	} {
		if got := assetListSubmissionMessage(catalog.AssetListMessagesSchema{}, reason); strings.TrimSpace(got) == "" {
			t.Fatalf("reason %q has an empty fallback", reason)
		}
	}
}

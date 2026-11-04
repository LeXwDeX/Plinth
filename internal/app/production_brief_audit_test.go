package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

type briefStoreFake struct {
	session chat.Session
	found   bool
	readErr error
	setErr  error
	writes  int
}

func (store *briefStoreFake) Session(context.Context, string, string) (chat.Session, bool, error) {
	return store.session, store.found, store.readErr
}

func (store *briefStoreFake) SetProductionBrief(
	_ context.Context,
	_ string,
	_ string,
	raw map[string]json.RawMessage,
) (chat.ProductionBrief, error) {
	if store.setErr != nil {
		return nil, store.setErr
	}
	store.writes++
	store.session.Brief = chat.ProductionBrief(raw)
	return store.session.Brief, nil
}

type failingAudits struct{ events int }

func (audits *failingAudits) RecordAudit(context.Context, administration.AuditEvent) error {
	audits.events++
	return errors.New("audit store unavailable")
}

// #103: the Agent's update_production_brief tool writes the same product audit
// as the HTTP PUT: action and resource type from the Schema, the session as the
// resource, and only the changed field names in the detail.
func TestAgentProductionBriefWriteIsAuditedByFieldNames(t *testing.T) {
	store := &briefStoreFake{
		session: chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "A"},
		found:   true,
	}
	audits := &recordedAudits{}
	raw := map[string]json.RawMessage{
		"purpose":   json.RawMessage(`"game_character"`),
		"needs_rig": json.RawMessage(`true`),
	}
	for range 2 {
		if err := updateProductionBriefAudited(
			context.Background(), store, audits, nil,
			"set_production_brief", "session", "owner-a", "session-a", raw,
		); err != nil {
			t.Fatal(err)
		}
	}
	if store.writes != 2 {
		t.Fatalf("writes = %d, want both replacements", store.writes)
	}
	if len(audits.events) != 1 {
		t.Fatalf("audit events = %+v, want one change", audits.events)
	}
	event := audits.events[0]
	fields, _ := event.Detail["fields"].([]string)
	if event.Action != "set_production_brief" || event.ResourceType != "session" ||
		event.ResourceID != "session-a" || event.ActorID != "owner-a" || event.ActorName != "A" ||
		len(fields) != 2 || fields[0] != "needs_rig" || fields[1] != "purpose" {
		t.Fatalf("audit event = %+v", event)
	}
}

// An audit failure must not fail a brief that was already stored; it is logged
// as a warning instead.
func TestAgentProductionBriefAuditFailureDoesNotFailTheWrite(t *testing.T) {
	store := &briefStoreFake{
		session: chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "A"},
		found:   true,
	}
	audits := &failingAudits{}
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	err := updateProductionBriefAudited(
		context.Background(), store, audits, logger,
		"set_production_brief", "session", "owner-a", "session-a",
		map[string]json.RawMessage{"purpose": json.RawMessage(`"render"`)},
	)
	if err != nil {
		t.Fatalf("an audit failure failed the stored brief: %v", err)
	}
	if store.writes != 1 || audits.events != 1 {
		t.Fatalf("writes=%d audit attempts=%d", store.writes, audits.events)
	}
	if !strings.Contains(log.String(), "写入产品审计事件失败") {
		t.Fatalf("audit failure was not logged: %s", log.String())
	}
}

func TestAgentProductionBriefWriteRefusesAMissingOrForeignSession(t *testing.T) {
	store := &briefStoreFake{found: false}
	audits := &recordedAudits{}
	err := updateProductionBriefAudited(
		context.Background(), store, audits, nil,
		"set_production_brief", "session", "owner-a", "session-a",
		map[string]json.RawMessage{"purpose": json.RawMessage(`"render"`)},
	)
	if !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("missing session = %v", err)
	}
	if store.writes != 0 || len(audits.events) != 0 {
		t.Fatalf("a missing session wrote %d and audited %+v", store.writes, audits.events)
	}
}

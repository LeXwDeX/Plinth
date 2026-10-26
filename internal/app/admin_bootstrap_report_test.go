package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/authentication"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type recordedAudits struct{ events []administration.AuditEvent }

func (r *recordedAudits) RecordAudit(_ context.Context, event administration.AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

// #319: the startup administrator bootstrap is visible in the service log and,
// when it changed something, in the administration audit trail. Neither
// carries the password.
func TestAdminBootstrapIsLoggedAndAudited(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	api := product.Application().Authentication.API
	for _, testCase := range []struct {
		outcome authentication.BootstrapAdminResult
		action  string
		audit   string
	}{
		{authentication.BootstrapAdminResult{Action: authentication.BootstrapAdminCreated, UserID: "user_1"}, "created", api.BootstrapAdminCreatedAuditAction},
		{authentication.BootstrapAdminResult{Action: authentication.BootstrapAdminPasswordReset, UserID: "user_1", SessionsRevoked: true}, "password_reset", api.BootstrapAdminPasswordResetAuditAction},
		{authentication.BootstrapAdminResult{Action: authentication.BootstrapAdminUnchanged, UserID: "user_1"}, "unchanged", ""},
	} {
		var buffer bytes.Buffer
		audits := &recordedAudits{}
		reportAdminBootstrap(context.Background(), slog.New(slog.NewJSONHandler(&buffer, nil)), audits, api, "admin", testCase.outcome)
		var line map[string]any
		if err := json.Unmarshal(buffer.Bytes(), &line); err != nil {
			t.Fatalf("%s: log = %q", testCase.action, buffer.String())
		}
		if line["level"] != "INFO" || line["action"] != testCase.action || line["user_id"] != "user_1" || line["username"] != "admin" ||
			line["sessions_revoked"] != testCase.outcome.SessionsRevoked {
			t.Fatalf("%s: log = %v", testCase.action, line)
		}
		if testCase.audit == "" {
			if len(audits.events) != 0 {
				t.Fatalf("unchanged bootstrap audited: %v", audits.events)
			}
			continue
		}
		if len(audits.events) != 1 || audits.events[0].Action != testCase.audit || audits.events[0].ResourceID != "user_1" ||
			audits.events[0].ResourceType != api.AuditResourceType || audits.events[0].ActorID != "user_1" {
			t.Fatalf("%s: audit = %#v", testCase.action, audits.events)
		}
	}
	if !strings.HasPrefix(api.BootstrapAdminCreatedAuditAction, "bootstrap_admin_") || !strings.HasPrefix(api.BootstrapAdminPasswordResetAuditAction, "bootstrap_admin_") {
		t.Fatalf("Schema audit actions = %q %q", api.BootstrapAdminCreatedAuditAction, api.BootstrapAdminPasswordResetAuditAction)
	}
	// Without ADMIN_PASSWORD nothing ran, so nothing is reported.
	var buffer bytes.Buffer
	audits := &recordedAudits{}
	reportAdminBootstrap(context.Background(), slog.New(slog.NewJSONHandler(&buffer, nil)), audits, api, "admin", authentication.BootstrapAdminResult{})
	if buffer.Len() != 0 || len(audits.events) != 0 {
		t.Fatalf("skipped bootstrap reported: %q %v", buffer.String(), audits.events)
	}
}

package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
)

// stepCapFixture seeds one session whose cap mode already used its only step,
// so the next counted 3D grant is denied at the cap with the newest turn bound.
func stepCapFixture(t *testing.T, owner, session, turn string) (*stepFixture, executiongrant.Grant) {
	t.Helper()
	f := newStepFixture(t)
	f.setSettings(t, owner, capSetting(1), capSetting(1))
	f.task(t, "used-"+session, owner, session, "text-to-3d", "SUCCEEDED", "", "COMPLETED")
	grant := f.grant(t, owner, session, "text-to-3d", turn)
	return f, grant
}

func TestStepCapReachedReadsTheNewestTurnsDenial(t *testing.T) {
	ctx := context.Background()

	t.Run("no turn or grant", func(t *testing.T) {
		f := newStepFixture(t)
		f.session(t, "owner-a", "session-a")
		reached, err := f.r.StepCapReached(ctx, "owner-a", "session-a")
		if err != nil || reached {
			t.Fatalf("StepCapReached = %v, %v; want false", reached, err)
		}
		if reached, err := f.r.StepCapReached(ctx, "owner-a", "missing-session"); err != nil || reached {
			t.Fatalf("unknown session = %v, %v; want false", reached, err)
		}
	})

	t.Run("denied at the cap", func(t *testing.T) {
		f, grant := stepCapFixture(t, "owner-a", "session-a", "turn-a")
		f.mustRefuse(t, grant, approvalpolicy.StepEntryList)
		reached, err := f.r.StepCapReached(ctx, "owner-a", "session-a")
		if err != nil || !reached {
			t.Fatalf("StepCapReached = %v, %v; want true", reached, err)
		}
		if reached, err := f.r.StepCapReached(ctx, "owner-b", "session-a"); err != nil || reached {
			t.Fatalf("foreign owner = %v, %v; want false", reached, err)
		}
	})

	t.Run("a persons denial is not a cap refusal", func(t *testing.T) {
		f, grant := stepCapFixture(t, "owner-a", "session-a", "turn-a")
		if _, err := f.r.DecideExecutionGrant(ctx, grant.OwnerID, grant.RequestID,
			"person-a", executiongrant.Deny); err != nil {
			t.Fatal(err)
		}
		reached, err := f.r.StepCapReached(ctx, "owner-a", "session-a")
		if err != nil || reached {
			t.Fatalf("StepCapReached = %v, %v; want false", reached, err)
		}
	})

	t.Run("a newer turn drops the old denial", func(t *testing.T) {
		f, grant := stepCapFixture(t, "owner-a", "session-a", "turn-a")
		f.mustRefuse(t, grant, approvalpolicy.StepEntryList)

		later := formatTime(time.Now().UTC().Add(time.Hour))
		if _, err := f.r.db.Exec(`INSERT INTO go_chat_messages
 (id,session_id,role,content,image_urls,tool_calls,created_at)
 VALUES ('turn-later','session-a','assistant','','[]','[]',?)`, later); err != nil {
			t.Fatal(err)
		}
		if _, err := f.r.db.Exec(`INSERT INTO go_chat_turns
 (assistant_message_id,session_id,owner_id,options_json,state,created_at,updated_at)
 VALUES ('turn-later','session-a','owner-a','{}','RUNNING',?,?)`, later, later); err != nil {
			t.Fatal(err)
		}

		reached, err := f.r.StepCapReached(ctx, "owner-a", "session-a")
		if err != nil || reached {
			t.Fatalf("StepCapReached = %v, %v; want the old denial left out", reached, err)
		}
	})
}

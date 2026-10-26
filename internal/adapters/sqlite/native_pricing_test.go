package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativeSessionAndUsageTotalsPersistentUnits(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "s", "owner", chat.Session{})
	insert := func(id string, q *schema.NativeQuote, credits int) {
		t.Helper()
		o := schema.Offering{ID: "o", EstimatedCredits: credits}
		var quote any
		if q != nil {
			o.NativePricing = &schema.NativePricing{Version: 1, Unit: q.Unit, UnitLabel: q.UnitLabel, BaseAmount: q.TotalAmount}
			b, _ := json.Marshal(q)
			quote = string(b)
		}
		b, _ := json.Marshal(o)
		_, e := r.db.Exec(`INSERT INTO generation_tasks(id,owner_id,capability,offering_json,input_json,request_hash,state,version,created_at,updated_at,session_id,native_quote_json,native_quote_required)VALUES(?,'owner','text-to-image',?,'{}',?,'SUCCEEDED',1,'2026-10-16T00:00:00Z','2026-10-16T00:00:00Z','s',?,?)`, id, string(b), id, quote, q != nil)
		if e != nil {
			t.Fatal(e)
		}
	}
	q := &schema.NativeQuote{Version: 1, Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", BaseAmount: "0.5", ExtraAmount: "0", TotalAmount: "0.5"}
	insert("n1", q, 0)
	insert("n2", q, 0)
	insert("l", nil, 7)
	other := *q
	other.Unit = "other.credit"
	other.UnitLabel = "Other credits"
	insert("n3", &other, 0)
	session, _, e := r.Session(ctx, "owner", "s")
	if e != nil || session.EstimatedCredits != 7 || len(session.PriceTotals) != 3 || session.PriceTotals[0].Amount != "1" || session.PriceTotals[1].Unit != "legacy.credits" || session.PriceTotals[2].Amount != "0.5" {
		t.Fatalf("session totals %+v %v", session, e)
	}
	service, e := administration.NewUsageService(administration.UsageServiceConfig{Repository: r, DefaultDays: 7, SucceededStates: []string{"SUCCEEDED"}, FailedStates: []string{"FAILED"}, DatePattern: `^\d{4}-\d{2}-\d{2}$`, DateLayout: "2006-01-02", Timezone: "UTC", Now: func() time.Time { return time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC) }})
	if e != nil {
		t.Fatal(e)
	}
	summaries, e := service.Summarize(ctx, administration.UsageRequest{UserID: "owner"})
	if e != nil || len(summaries) != 1 || len(summaries[0].PriceTotals) != 3 || summaries[0].PriceTotals[0].Amount != "1" || summaries[0].TotalTasks != 4 {
		t.Fatalf("usage %+v %v", summaries, e)
	}
	records, _, e := r.QueryAdministrationGenerations(ctx, administration.GenerationQuery{UserID: "owner", Page: 1, PageSize: 10})
	if e != nil || len(records) != 4 {
		t.Fatalf("records %d %v", len(records), e)
	}
	for _, v := range records {
		if v.TaskID != "l" && (v.NativeQuote == nil || v.NativeQuote.TotalAmount != "0.5") {
			t.Fatal("lost record quote")
		}
	}
	if _, e = r.db.Exec(`UPDATE generation_tasks SET native_quote_json=NULL WHERE id='n1'`); e == nil {
		t.Fatal("persisted native quote mutated")
	}
	if _, e = r.db.Exec(`DROP TRIGGER generation_task_native_quote_immutable`); e != nil {
		t.Fatal(e)
	} // corruption fixture only
	_, e = r.db.Exec(`UPDATE generation_tasks SET native_quote_json=NULL WHERE id='n1'`)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = r.Session(ctx, "owner", "s"); e == nil {
		t.Fatal("missing native quote became free session")
	}
	if _, e = r.GetTaskSummary(ctx, generation.OwnTasks("owner"), "n1"); e == nil {
		t.Fatal("summary accepted missing quote")
	}
	if _, e = r.QueryAdministrationUsage(ctx, administration.UsageQuery{Since: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}); e == nil {
		t.Fatal("usage accepted missing quote")
	}
}

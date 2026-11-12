package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

type memorySelfCheckStore struct {
	mu      sync.Mutex
	records map[string]chat.SelfCheckRecord
	// conflicts makes the next SaveSelfCheck lose one compare-and-swap race.
	conflicts int
}

func newMemorySelfCheckStore() *memorySelfCheckStore {
	return &memorySelfCheckStore{records: map[string]chat.SelfCheckRecord{}}
}

func selfCheckKey(owner, session, task string) string {
	return owner + "\x00" + session + "\x00" + task
}

func (store *memorySelfCheckStore) ListSelfChecks(_ context.Context, owner, session string) ([]chat.SelfCheckRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var records []chat.SelfCheckRecord
	for key, record := range store.records {
		if strings.HasPrefix(key, owner+"\x00"+session+"\x00") {
			records = append(records, record)
		}
	}
	return records, nil
}

func (store *memorySelfCheckStore) SelfCheck(_ context.Context, owner, session, task string) (chat.SelfCheckRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, found := store.records[selfCheckKey(owner, session, task)]
	return record, found, nil
}

func (store *memorySelfCheckStore) SaveSelfCheck(_ context.Context, owner, session string,
	record chat.SelfCheckRecord, expected int64) (chat.SelfCheckRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := selfCheckKey(owner, session, record.TaskID)
	stored, found := store.records[key]
	if store.conflicts > 0 {
		store.conflicts--
		return chat.SelfCheckRecord{}, true, nil
	}
	if expected <= 0 {
		if found {
			return chat.SelfCheckRecord{}, true, nil
		}
		record.Revision = 1
		store.records[key] = record
		return record, false, nil
	}
	if !found || stored.Revision != expected {
		return chat.SelfCheckRecord{}, true, nil
	}
	record.Revision = expected + 1
	store.records[key] = record
	return record, false, nil
}

func testSelfCheckSchema(t *testing.T) catalog.ChatSelfCheckSchema {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product.Application().Chat.SelfCheck
}

func TestValidateSelfCheckConclusionAppliesTheSchemaRules(t *testing.T) {
	schema := testSelfCheckSchema(t)
	passing := chat.SelfCheckRecord{Checks: []chat.SelfCheckCheck{
		{Kind: chat.SelfCheckKindInspect, OK: true},
		{Kind: chat.SelfCheckKindReference, OK: true},
	}}
	cases := []struct {
		name       string
		record     chat.SelfCheckRecord
		conclusion chat.SelfCheckConclusion
		kind       string
		field      string
	}{
		{name: "unknown status", conclusion: chat.SelfCheckConclusion{Status: "done", Summary: "x"}, kind: "unknown_status"},
		{name: "unchecked is not writable", conclusion: chat.SelfCheckConclusion{Status: "unchecked", Summary: "x"}, kind: "not_writable"},
		{name: "passed needs evidence", conclusion: chat.SelfCheckConclusion{Status: "passed", Summary: "x"}, kind: "needs_check"},
		{name: "passed forbids remaining", record: passing,
			conclusion: chat.SelfCheckConclusion{Status: "passed", Summary: "x", Remaining: []string{"crack"}}, kind: "remaining_forbidden"},
		{name: "issues needs remaining", record: passing,
			conclusion: chat.SelfCheckConclusion{Status: "has_issues", Summary: "x"}, kind: "remaining_required"},
		{name: "summary too long", record: passing,
			conclusion: chat.SelfCheckConclusion{Status: "passed", Summary: strings.Repeat("设", schema.Conclusion.MaxSummaryCharacters+1)},
			kind:       "too_long", field: "summary"},
		{name: "item too long", record: passing,
			conclusion: chat.SelfCheckConclusion{Status: "has_issues", Summary: "x", Remaining: []string{strings.Repeat("设", schema.Conclusion.MaxItemCharacters+1)}},
			kind:       "too_long", field: "remaining[0]"},
		{name: "too many items", record: passing,
			conclusion: chat.SelfCheckConclusion{Status: "has_issues", Summary: "x",
				Remaining: make([]string, schema.Conclusion.MaxItems+1)},
			kind: "too_long", field: "remaining"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			failure := chat.ValidateSelfCheckConclusion(schema, tt.record, tt.conclusion)
			if failure == nil || failure.Kind != tt.kind || failure.Field != tt.field {
				t.Fatalf("failure = %+v, want kind %q field %q", failure, tt.kind, tt.field)
			}
		})
	}
	if failure := chat.ValidateSelfCheckConclusion(schema, passing,
		chat.SelfCheckConclusion{Status: "passed", Summary: "fits", Findings: []string{"ok"}, Fixes: []string{"none"}}); failure != nil {
		t.Fatalf("a valid passed conclusion was refused: %+v", failure)
	}
	if failure := chat.ValidateSelfCheckConclusion(schema, passing,
		chat.SelfCheckConclusion{Status: "has_issues", Summary: "crack", Remaining: []string{"crack"}}); failure != nil {
		t.Fatalf("a valid issues conclusion was refused: %+v", failure)
	}
}

func TestSelfCheckCountsDeriveRoundsRendersAndState(t *testing.T) {
	schema := testSelfCheckSchema(t)
	record := chat.SelfCheckRecord{Checks: []chat.SelfCheckCheck{
		{Kind: chat.SelfCheckKindRender, OK: true},
		{Kind: chat.SelfCheckKindInspect, OK: true},
		{Kind: chat.SelfCheckKindRender, OK: true},
		{Kind: chat.SelfCheckKindCompare, OK: true},
		{Kind: chat.SelfCheckKindInspect, OK: false},
		{Kind: chat.SelfCheckKindInspect, OK: true},
		{Kind: chat.SelfCheckKindReference, OK: true},
	}}
	counters := chat.SelfCheckCounts(schema, record)
	if counters.RoundsUsed != 3 || counters.RendersInRound != 1 {
		t.Fatalf("counters = %+v", counters)
	}
	if counters.StatusID != "unchecked" || counters.StatusRole != chat.SelfCheckRoleUnchecked {
		t.Fatalf("state without a conclusion = %+v", counters)
	}
	// A failed inspect still opened its round; renders before the last inspect
	// belong to earlier rounds.
	if counters.RoundsLimit != schema.Limits.MaxRoundsPerResult || counters.CorrectionsLimit != schema.Limits.MaxCorrectionsPerResult {
		t.Fatalf("limits = %+v", counters)
	}
	record.Conclusion = &chat.SelfCheckConclusion{Status: "passed", Summary: "ok"}
	if got := chat.SelfCheckCounts(schema, record); got.StatusID != "passed" || got.StatusRole != chat.SelfCheckRolePassed {
		t.Fatalf("passed state = %+v", got)
	}
	record.Conclusion.Status = "has_issues"
	if got := chat.SelfCheckCounts(schema, record); got.StatusRole != chat.SelfCheckRoleIssues {
		t.Fatalf("issues state = %+v", got)
	}
}

func TestSelfCheckServiceAppendsEvidenceAndInvalidatesTheConclusion(t *testing.T) {
	schema := testSelfCheckSchema(t)
	store := newMemorySelfCheckStore()
	service := chat.NewSelfCheckService(store, schema)
	ctx := context.Background()
	first, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "asset-a", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
	})
	if err != nil || first.Revision != 1 || len(first.Checks) != 1 || first.Checks[0].Round != 1 {
		t.Fatalf("first append = %+v err=%v", first, err)
	}
	// A passed conclusion needs a render or reference check; add one first.
	withRender, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "asset-a", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindReference, Tool: "compare_model_reference", Views: []string{"front"}, OK: true,
	})
	if err != nil || withRender.Conclusion != nil || withRender.Checks[1].Round != 1 {
		t.Fatalf("reference append = %+v err=%v", withRender, err)
	}
	conclusion, err := service.RecordConclusion(ctx, "owner-a", "session-a", "task-a", "asset-a",
		chat.SelfCheckConclusion{Status: "passed", Summary: "looks right"})
	if err != nil || conclusion.Conclusion == nil || conclusion.Conclusion.Status != "passed" {
		t.Fatalf("conclusion = %+v err=%v", conclusion, err)
	}
	// New evidence invalidates the written conclusion, so the state returns to
	// unchecked until the Agent records again.
	cleared, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "asset-a", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindRender, Tool: "render_model_views", Views: []string{"back"}, OK: true,
	})
	if err != nil || cleared.Conclusion != nil {
		t.Fatalf("new evidence must clear the conclusion: %+v err=%v", cleared, err)
	}
	passed, err := service.RecordConclusion(ctx, "owner-a", "session-a", "task-a", "asset-a",
		chat.SelfCheckConclusion{Status: "passed", Summary: "checked"})
	if err != nil || passed.Conclusion == nil || passed.Conclusion.Status != "passed" {
		t.Fatalf("passed conclusion = %+v err=%v", passed, err)
	}
	if passed.Revision != 5 {
		t.Fatalf("revision = %d, want 5", passed.Revision)
	}
	counters := service.Counts(passed)
	if counters.StatusRole != chat.SelfCheckRolePassed || counters.RoundsUsed != 1 || counters.RendersInRound != 2 {
		t.Fatalf("counters = %+v", counters)
	}
}

func TestSelfCheckServiceRetriesALostCompareAndSwapOnce(t *testing.T) {
	schema := testSelfCheckSchema(t)
	store := newMemorySelfCheckStore()
	service := chat.NewSelfCheckService(store, schema)
	ctx := context.Background()
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	store.conflicts = 1
	record, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindRender, Tool: "render_model_views", OK: true,
	})
	if err != nil || len(record.Checks) != 2 {
		t.Fatalf("retried append = %+v err=%v", record, err)
	}
	store.conflicts = 2
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindRender, Tool: "render_model_views", OK: true,
	}); err == nil {
		t.Fatal("two lost races must be reported")
	}
}

func TestSelfCheckServiceIsOwnerAndSessionScoped(t *testing.T) {
	schema := testSelfCheckSchema(t)
	store := newMemorySelfCheckStore()
	service := chat.NewSelfCheckService(store, schema)
	ctx := context.Background()
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := service.Get(ctx, "owner-b", "session-a", "task-a"); err != nil || found {
		t.Fatalf("foreign owner read = found %v err %v", found, err)
	}
	if _, found, err := service.Get(ctx, "owner-a", "session-b", "task-a"); err != nil || found {
		t.Fatalf("foreign session read = found %v err %v", found, err)
	}
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "", "", chat.SelfCheckCheck{}); err == nil {
		t.Fatal("a record needs a Task")
	}
}

func TestSelfCheckServiceAttachesHandlesWithoutInvalidatingTheConclusion(t *testing.T) {
	schema := testSelfCheckSchema(t)
	store := newMemorySelfCheckStore()
	service := chat.NewSelfCheckService(store, schema)
	ctx := context.Background()
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindRender, Tool: "render_model_views", Views: []string{"front", "back"}, OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordConclusion(ctx, "owner-a", "session-a", "task-a", "",
		chat.SelfCheckConclusion{Status: "passed", Summary: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := service.AttachCheckHandles(ctx, "owner-a", "session-a", "task-a", "render_model_views", []string{"@img1", "@img2"}); err != nil {
		t.Fatal(err)
	}
	record, found, err := service.Get(ctx, "owner-a", "session-a", "task-a")
	if err != nil || !found {
		t.Fatalf("record = %+v err=%v", record, err)
	}
	if got := record.Checks[1].Handles; len(got) != 2 || got[0] != "@img1" || got[1] != "@img2" {
		t.Fatalf("handles = %v", got)
	}
	if record.Conclusion == nil || record.Conclusion.Status != "passed" {
		t.Fatalf("naming pictures must not clear the conclusion: %+v", record.Conclusion)
	}
	// A record without that tool is left untouched, not reported as an error.
	if err := service.AttachCheckHandles(ctx, "owner-a", "session-a", "task-a", "compare_models", []string{"@img3"}); err != nil {
		t.Fatal(err)
	}
}

func TestSelfCheckServiceUnavailableStore(t *testing.T) {
	service := chat.NewSelfCheckService(nil, catalog.ChatSelfCheckSchema{})
	if _, err := service.List(context.Background(), "owner-a", "session-a"); !errors.Is(err, chat.ErrSelfCheckUnavailable) {
		t.Fatalf("unavailable list err = %v", err)
	}
}

// TestSelfCheckCorrectionCountsAKeyedPaidStepOnce is the #246 regression: the
// same paid submission can reach the count from a restarted turn, a bound-task
// observation or a retried approval, but corrections_used advances once per
// key and the conclusion is invalidated only by the first real correction.
func TestSelfCheckCorrectionCountsAKeyedPaidStepOnce(t *testing.T) {
	schema := testSelfCheckSchema(t)
	store := newMemorySelfCheckStore()
	service := chat.NewSelfCheckService(store, schema)
	ctx := context.Background()
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindInspect, Tool: "inspect_model", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AppendCheck(ctx, "owner-a", "session-a", "task-a", "", chat.SelfCheckCheck{
		Kind: chat.SelfCheckKindRender, Tool: "render_model_views", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordConclusion(ctx, "owner-a", "session-a", "task-a", "",
		chat.SelfCheckConclusion{Status: "passed", Summary: "ok"}); err != nil {
		t.Fatal(err)
	}
	first, found, err := service.IncrementCorrectionsOnce(ctx, "owner-a", "session-a", "task-a", "operation:op-1")
	if err != nil || !found || first.CorrectionsUsed != 1 || first.Conclusion != nil {
		t.Fatalf("first correction = %+v found=%v err=%v", first, found, err)
	}
	replayed, found, err := service.IncrementCorrectionsOnce(ctx, "owner-a", "session-a", "task-a", "operation:op-1")
	if err != nil || !found || replayed.CorrectionsUsed != 1 || replayed.Revision != first.Revision {
		t.Fatalf("replayed correction = %+v found=%v err=%v", replayed, found, err)
	}
	second, found, err := service.IncrementCorrectionsOnce(ctx, "owner-a", "session-a", "task-a", "operation:op-2")
	if err != nil || !found || second.CorrectionsUsed != 2 || len(second.CorrectionKeys) != 2 {
		t.Fatalf("second correction = %+v found=%v err=%v", second, found, err)
	}
	// The keys survive the stored wire document; a foreign session cannot
	// count the result at all.
	stored, found, err := store.SelfCheck(ctx, "owner-a", "session-a", "task-a")
	if err != nil || !found || len(stored.CorrectionKeys) != 2 {
		t.Fatalf("stored record = %+v found=%v err=%v", stored, found, err)
	}
	if _, found, err := service.IncrementCorrectionsOnce(ctx, "owner-b", "session-a", "task-a", "operation:op-3"); err != nil || found {
		t.Fatalf("foreign correction found=%v err=%v", found, err)
	}
}

// TestSelfCheckPassRequiresACleanPurposeCheckOrAWaiver is #281: "passed" is
// refused while the latest successful inspection still has failed purpose
// checks, unless the conclusion records why the person accepted them; the
// uncheckable status needs a refused latest inspection; the saved conclusion
// names the purpose it was judged against.
func TestSelfCheckPassRequiresACleanPurposeCheckOrAWaiver(t *testing.T) {
	schema := testSelfCheckSchema(t)
	if !schema.RequiredChecks.UsageClean {
		t.Fatal("default catalog must require a clean purpose check for a pass")
	}
	failing := chat.SelfCheckRecord{Checks: []chat.SelfCheckCheck{
		{Kind: chat.SelfCheckKindInspect, OK: true, UsageFailed: []string{"triangles", "uv"},
			Purpose: "render", PurposeLabel: "展示/渲染", PurposeSource: "default_profile"},
		{Kind: chat.SelfCheckKindRender, OK: true},
	}}
	failure := chat.ValidateSelfCheckConclusion(schema, failing, chat.SelfCheckConclusion{Status: "passed", Summary: "x"})
	if failure == nil || failure.Kind != "usage_failed" || failure.Field != "triangles, uv" {
		t.Fatalf("failed purpose checks: %+v", failure)
	}
	if failure := chat.ValidateSelfCheckConclusion(schema, failing,
		chat.SelfCheckConclusion{Status: "passed", Summary: "x", Waiver: "用户接受面数略超"}); failure != nil {
		t.Fatalf("a waived pass was refused: %+v", failure)
	}
	if failure := chat.ValidateSelfCheckConclusion(schema, failing,
		chat.SelfCheckConclusion{Status: "has_issues", Summary: "x", Remaining: []string{"面数超预算"}}); failure != nil {
		t.Fatalf("issues with failed checks was refused: %+v", failure)
	}
	// A later clean inspection replaces the failed one.
	fixed := failing
	fixed.Checks = append(append([]chat.SelfCheckCheck(nil), failing.Checks...),
		chat.SelfCheckCheck{Kind: chat.SelfCheckKindInspect, OK: true})
	if failure := chat.ValidateSelfCheckConclusion(schema, fixed, chat.SelfCheckConclusion{Status: "passed", Summary: "x"}); failure != nil {
		t.Fatalf("a clean re-inspection was refused: %+v", failure)
	}
	if failure := chat.ValidateSelfCheckConclusion(schema, failing,
		chat.SelfCheckConclusion{Status: "passed", Summary: "x", Waiver: strings.Repeat("设", schema.Conclusion.MaxItemCharacters+1)}); failure == nil ||
		failure.Kind != "too_long" || failure.Field != "waiver" {
		t.Fatalf("over-long waiver: %+v", failure)
	}

	uncheckable := ""
	for _, status := range schema.Statuses {
		if status.Role == chat.SelfCheckRoleUncheckable {
			uncheckable = status.ID
		}
	}
	if uncheckable == "" {
		t.Fatal("default catalog must declare an uncheckable status")
	}
	if failure := chat.ValidateSelfCheckConclusion(schema, fixed,
		chat.SelfCheckConclusion{Status: uncheckable, Summary: "x"}); failure == nil || failure.Kind != "uncheckable_unproven" {
		t.Fatalf("uncheckable after a readable inspection: %+v", failure)
	}
	refused := chat.SelfCheckRecord{Checks: []chat.SelfCheckCheck{
		{Kind: chat.SelfCheckKindInspect, OK: false, Error: "too large"},
	}}
	if failure := chat.ValidateSelfCheckConclusion(schema, refused,
		chat.SelfCheckConclusion{Status: uncheckable, Summary: "超出检查上限"}); failure != nil {
		t.Fatalf("uncheckable after a refused inspection was refused: %+v", failure)
	}
	if failure := chat.ValidateSelfCheckConclusion(schema, refused, chat.SelfCheckConclusion{Status: "passed", Summary: "x"}); failure == nil ||
		failure.Kind != "needs_check" {
		t.Fatalf("a refused inspection must never pass: %+v", failure)
	}

	store := newMemorySelfCheckStore()
	service := chat.NewSelfCheckService(store, schema)
	ctx := context.Background()
	for _, check := range failing.Checks {
		if _, err := service.AppendCheck(ctx, "o", "s", "t", "a", check); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := service.RecordConclusion(ctx, "o", "s", "t", "a",
		chat.SelfCheckConclusion{Status: "passed", Summary: "ok", Waiver: " 用户接受 ", Purpose: "forged"})
	if err != nil || saved.Conclusion == nil {
		t.Fatalf("waived pass = %+v err=%v", saved, err)
	}
	if got := saved.Conclusion; got.Waiver != "用户接受" || got.Purpose != "render" ||
		got.PurposeLabel != "展示/渲染" || got.PurposeSource != "default_profile" {
		t.Fatalf("saved conclusion = %+v", got)
	}
}

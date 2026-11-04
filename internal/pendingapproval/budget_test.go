package pendingapproval_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/budget"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/pendingapproval"
)

// productionPlan stores one pending plan the Agent's proposal tool would have
// opened, with the message and call identity the card is anchored to.
func (w *world) productionPlan(t *testing.T, key string, limit int, steps ...budget.PlanStep) budget.ProductionBudget {
	t.Helper()
	w.ensureSession()
	production, err := w.grants.CreateProductionPlan(context.Background(), "owner-a", "session-a",
		"plan-"+key, "turn-"+key, "call-"+key, budget.Plan{Title: "骑士", Steps: steps}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return production
}

// The pending plan is a card of its own: it carries the approved shape, the
// server's quote and the plan limit, and it survives beside step approvals.
func TestPendingListCarriesTheProductionPlanCard(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	plan := w.productionPlan(t, "one", 40,
		budget.PlanStep{Capability: "image-to-3d", Purpose: "生成模型", EstimatedCredits: 25},
		budget.PlanStep{Capability: "retexture", Purpose: "修贴图", EstimatedCredits: 5})

	items, err := w.service.List(context.Background(), "owner-a", "session-a")
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %+v err=%v", items, err)
	}
	item := items[0]
	if item.Kind != pendingapproval.ItemKindPlan || item.RequestID != plan.ID ||
		item.Status != pendingapproval.StatusPending || item.Tool != "propose_production_plan" ||
		item.EstimatedCredits != 30 || item.BudgetCredits != 40 ||
		item.MessageID != "turn-one" || len(item.Inputs) != 0 {
		t.Fatalf("plan card = %+v", item)
	}
	if item.Plan == nil || item.Plan.Title != "骑士" || len(item.Plan.Steps) != 2 ||
		item.Plan.Steps[0].Capability != "image-to-3d" || item.Plan.Steps[0].EstimatedCredits != 25 {
		t.Fatalf("plan card data = %+v", item.Plan)
	}
	if got := item.ExpiresAt.Sub(item.CreatedAt); got != time.Hour {
		t.Fatalf("plan card expiry window = %v", got)
	}

	// A pending plan never reserved or spent anything.
	summary, found, err := w.grants.BudgetSummary(context.Background(), "owner-a", "session-a")
	if err != nil || found {
		t.Fatalf("pending plan summary = %+v found=%v err=%v", summary, found, err)
	}
}

// Approval activates the plan, posts its one decision message and removes the
// card; denial activates nothing. A second decision is refused.
func TestDecidingAProductionPlanCard(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
		plan := w.productionPlan(t, "approve", 40,
			budget.PlanStep{Capability: "image-to-3d", Purpose: "生成模型", EstimatedCredits: 25})

		outcome, err := w.service.DecideBudget(context.Background(), w.operator, "session-a", plan.ID, executiongrant.Approve)
		if err != nil || outcome.BudgetID != plan.ID || outcome.State != budget.StateActive ||
			outcome.MessageID != pendingapproval.BudgetMessageID(plan.ID) {
			t.Fatalf("approve = %+v err=%v", outcome, err)
		}
		message, ok := w.message(outcome.MessageID)
		if !ok || !strings.Contains(message.Content, "骑士") || !strings.Contains(message.Content, "40") {
			t.Fatalf("approval message = %+v ok=%v", message, ok)
		}
		stored, err := w.repo.GetProductionBudget(context.Background(), "owner-a", plan.ID)
		if err != nil || stored.State != budget.StateActive || stored.DecidedBy != "owner-a" {
			t.Fatalf("stored plan = %+v err=%v", stored, err)
		}
		items, err := w.service.List(context.Background(), "owner-a", "session-a")
		if err != nil || len(items) != 0 {
			t.Fatalf("plan still listed = %+v err=%v", items, err)
		}
		summary, found, err := w.grants.BudgetSummary(context.Background(), "owner-a", "session-a")
		if err != nil || !found || summary.State != budget.StateActive || summary.RemainingCredits != 40 {
			t.Fatalf("active summary = %+v found=%v err=%v", summary, found, err)
		}
		if _, err := w.service.DecideBudget(context.Background(), w.operator, "session-a", plan.ID, executiongrant.Approve); !errors.Is(err, executiongrant.ErrBudgetState) {
			t.Fatalf("second decision = %v", err)
		}
	})

	t.Run("deny", func(t *testing.T) {
		w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
		plan := w.productionPlan(t, "deny", 40,
			budget.PlanStep{Capability: "image-to-3d", Purpose: "生成模型", EstimatedCredits: 25})
		outcome, err := w.service.DecideBudget(context.Background(), w.operator, "session-a", plan.ID, executiongrant.Deny)
		if err != nil || outcome.State != budget.StateDenied || outcome.MessageID != pendingapproval.BudgetMessageID(plan.ID) {
			t.Fatalf("deny = %+v err=%v", outcome, err)
		}
		stored, err := w.repo.GetProductionBudget(context.Background(), "owner-a", plan.ID)
		if err != nil || stored.State != budget.StateDenied {
			t.Fatalf("denied plan = %+v err=%v", stored, err)
		}
		if summary, found, err := w.grants.BudgetSummary(context.Background(), "owner-a", "session-a"); err != nil || found {
			t.Fatalf("denied plan became current: %+v found=%v err=%v", summary, found, err)
		}
	})
}

// An expired plan cannot be approved, leaves the list, and the sweep posts its
// one expiry notice instead of a decision.
func TestProductionPlanCardExpires(t *testing.T) {
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	plan := w.productionPlan(t, "expire", 40,
		budget.PlanStep{Capability: "image-to-3d", Purpose: "生成模型", EstimatedCredits: 25})
	w.grants.Clock = func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }

	if _, err := w.service.DecideBudget(context.Background(), w.operator, "session-a", plan.ID, executiongrant.Approve); !errors.Is(err, executiongrant.ErrExpired) {
		t.Fatalf("expired approval = %v", err)
	}
	if items, err := w.service.List(context.Background(), "owner-a", "session-a"); err != nil || len(items) != 0 {
		t.Fatalf("expired plan listed = %+v err=%v", items, err)
	}
	settled, err := w.service.Sweep(context.Background())
	if err != nil || settled != 1 {
		t.Fatalf("sweep settled=%d err=%v", settled, err)
	}
	stored, err := w.repo.GetProductionBudget(context.Background(), "owner-a", plan.ID)
	if err != nil || stored.State != budget.StateDenied {
		t.Fatalf("expired plan = %+v err=%v", stored, err)
	}
	notice, ok := w.message(pendingapproval.BudgetMessageID(plan.ID) + ":expired")
	if !ok || !strings.Contains(notice.Content, "骑士") || !strings.Contains(notice.Content, "超时") {
		t.Fatalf("expiry notice = %+v ok=%v", notice, ok)
	}
	// A repeated sweep neither expires the plan again nor posts another notice.
	if settled, err := w.service.Sweep(context.Background()); err != nil || settled != 0 {
		t.Fatalf("second sweep settled=%d err=%v", settled, err)
	}
	posted := 0
	for _, message := range w.allMessages(t) {
		if message.ID == pendingapproval.BudgetMessageID(plan.ID)+":expired" {
			posted++
		}
	}
	if posted != 1 {
		t.Fatalf("expiry notices = %d", posted)
	}
}

// Approving a newer plan closes the session's former plan; the session's
// current budget is always the newest approved one.
func TestANewerPlanClosesTheFormerOne(t *testing.T) {
	ctx := context.Background()
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	former := w.productionPlan(t, "former", 20,
		budget.PlanStep{Capability: "image-to-3d", Purpose: "生成模型", EstimatedCredits: 5})
	if _, err := w.service.DecideBudget(ctx, w.operator, "session-a", former.ID, executiongrant.Approve); err != nil {
		t.Fatal(err)
	}
	newer := w.productionPlan(t, "newer", 50,
		budget.PlanStep{Capability: "image-to-3d", Purpose: "重新生成", EstimatedCredits: 15})
	if summary, found, err := w.grants.BudgetSummary(ctx, "owner-a", "session-a"); err != nil || !found || summary.ID != former.ID {
		t.Fatalf("former plan before the decision = %+v found=%v err=%v", summary, found, err)
	}
	outcome, err := w.service.DecideBudget(ctx, w.operator, "session-a", newer.ID, executiongrant.Approve)
	if err != nil || outcome.State != budget.StateActive {
		t.Fatalf("newer decision = %+v err=%v", outcome, err)
	}
	closed, err := w.repo.GetProductionBudget(ctx, "owner-a", former.ID)
	if err != nil || closed.State != budget.StateClosed {
		t.Fatalf("former plan = %+v err=%v", closed, err)
	}
	summary, found, err := w.grants.BudgetSummary(ctx, "owner-a", "session-a")
	if err != nil || !found || summary.ID != newer.ID || summary.LimitCredits != 50 || summary.RemainingCredits != 50 {
		t.Fatalf("current plan = %+v found=%v err=%v", summary, found, err)
	}
}

// A plan decision belongs to the owner's own session and to a real browser
// human, like a step decision.
func TestProductionPlanDecisionIsScopedToTheOwnersSession(t *testing.T) {
	ctx := context.Background()
	w := openWorld(t, filepath.Join(t.TempDir(), "a.db"), time.Hour)
	plan := w.productionPlan(t, "scope", 40,
		budget.PlanStep{Capability: "image-to-3d", Purpose: "生成模型", EstimatedCredits: 5})

	foreignOwner := w.operator
	foreignOwner.OwnerID, foreignOwner.ActorID = "owner-b", "owner-b"
	if _, err := w.service.DecideBudget(ctx, foreignOwner, "session-a", plan.ID, executiongrant.Approve); !errors.Is(err, pendingapproval.ErrSessionNotFound) {
		t.Fatalf("foreign owner decision = %v", err)
	}
	otherSession := w.operator
	otherSession.OwnerID, otherSession.ActorID = "owner-a", "owner-a"
	now := time.Now().UTC()
	if _, err := w.repo.CreateSession(ctx, chat.Session{ID: "session-b", OwnerID: "owner-a", OwnerName: "Owner",
		Title: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.service.DecideBudget(ctx, otherSession, "session-b", plan.ID, executiongrant.Approve); !errors.Is(err, executiongrant.ErrConflict) {
		t.Fatalf("foreign session decision = %v", err)
	}
	agent := w.operator
	agent.Source = executiongrant.AgentPending
	if _, err := w.service.DecideBudget(ctx, agent, "session-a", plan.ID, executiongrant.Approve); !errors.Is(err, executiongrant.ErrInvalid) {
		t.Fatalf("agent decision = %v", err)
	}
	if stored, err := w.repo.GetProductionBudget(ctx, "owner-a", plan.ID); err != nil || stored.State != budget.StatePending {
		t.Fatalf("refused decisions changed the plan: %+v err=%v", stored, err)
	}
}

// allMessages returns the session's messages in order.
func (w *world) allMessages(t *testing.T) []chat.Message {
	t.Helper()
	messages, err := w.chat.ListMessages(context.Background(), "owner-a", "session-a")
	if err != nil {
		t.Fatal(err)
	}
	return messages
}

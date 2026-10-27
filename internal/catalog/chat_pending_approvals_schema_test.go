package catalog_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestPendingApprovalDeclarationsAreValidated(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.ApplicationSchema)
	}{
		{"expiry must be positive", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.TTLSeconds = 0 }},
		{"list limit must be positive", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.MaximumListed = 0 }},
		{"sweep interval must be positive", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.MonitorIntervalMilliseconds = 0 }},
		{"sweep batch must be positive", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.MonitorBatchSize = 0 }},
		{"denied text is required", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.Messages.Denied = " " }},
		{"expired text is required", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.Messages.Expired = "" }},
		{"cancelled text is required", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.Messages.Cancelled = " " }},
		{"extra-credit denial text is required", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.Messages.DeniedExtraCredits = "" }},
		{"extra-credit denial text names the extra credits", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.Messages.DeniedExtraCredits = "denied" }},
		{"extra items separator is required", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.Messages.ExtraItemsSeparator = "" }},
		{"duplicate text is required", func(a *catalog.ApplicationSchema) { a.Chat.Agent.ConsentDuplicateContent = "" }},
		{"started text names the model", func(a *catalog.ApplicationSchema) { a.Chat.PendingApprovals.Messages.Started = "started" }},
		// #329 E: the finished result that also says which pictures were used.
		{"result-with-note text names the note", func(a *catalog.ApplicationSchema) {
			a.Chat.PendingApprovals.Messages.SucceededWithNote = "{model} done"
		}},
		{"multi-picture capacity note names the count", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.Tools {
				if a.Chat.Tools[index].Name == "create_model_3d_from_image" {
					a.Chat.Tools[index].ReferenceCapacityNote.Multiple = "{model} takes several pictures"
				}
			}
		}},
		{"optional no-prompt text cannot be whitespace", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.Tools {
				if a.Chat.Tools[index].Name == "create_model_3d_from_image" {
					a.Chat.Tools[index].ReferenceCapacityNote.NoPrompt = " "
				}
			}
		}},
		{"capacity note needs a tool that takes pictures", func(a *catalog.ApplicationSchema) {
			for index := range a.Chat.Tools {
				if a.Chat.Tools[index].Name == "create_motion_clip" {
					a.Chat.Tools[index].ReferenceCapacityNote = catalog.ChatToolReferenceCapacityNote{Single: "one", Multiple: "{count}"}
				}
			}
		}},
		{"route needs the session parameter", func(a *catalog.ApplicationSchema) {
			a.Runtime.HTTP.Routes.ChatSessionPendingApprovals = "/api/v1/chat/pending-approvals"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			tt.mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("ValidateApplicationSchema accepted an invalid declaration")
			}
		})
	}
}

func TestDefaultPendingApprovalsExpireAfterADay(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Application().Chat.PendingApprovals.TTLSeconds; got != 24*3600 {
		t.Fatalf("pending approval ttl = %d seconds, want one day", got)
	}
}

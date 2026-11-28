package agenttools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #719: a three-view step on an Offering that does not bill in credits (GPT
// Image, Gemini, Jimeng) tells neither the result card nor the Agent a credit
// number; a credits-metered one (Meshy Nano Banana) keeps both.
func TestThreeViewStatesCreditsOnlyForCreditsMeteredOfferings(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	messages := product.Application().Chat.ThreeView.Messages
	for _, tc := range []struct {
		name    string
		metered bool
	}{{name: "metered", metered: true}, {name: "unmetered", metered: false}} {
		t.Run(tc.name, func(t *testing.T) {
			task := generation.Task{ID: "task-a", Input: json.RawMessage(`{}`),
				Offering: generation.Offering{EstimatedCredits: 6, CreditsMetered: tc.metered}}
			wantCredits := 0
			if tc.metered {
				wantCredits = 6
			}
			if got := legacyThreeViewCredits(task); got != wantCredits {
				t.Fatalf("observation credits = %d; want %d", got, wantCredits)
			}
			for _, continuing := range []bool{false, true} {
				receipt := threeViewReceipt(messages, task, continuing, "run-a", "正面", "已自动批准")
				if receipt == "" || !strings.Contains(receipt, "run-a") || !strings.Contains(receipt, "task-a") {
					t.Fatalf("receipt = %q", receipt)
				}
				if strings.Contains(receipt, "{") {
					t.Fatalf("receipt keeps a placeholder: %q", receipt)
				}
				if states := strings.Contains(receipt, "6 积分"); states != tc.metered {
					t.Fatalf("continuing=%v receipt states credits=%v; want %v: %q", continuing, states, tc.metered, receipt)
				}
				if !tc.metered && strings.Contains(receipt, "估算") {
					t.Fatalf("unmetered receipt still quotes an estimate: %q", receipt)
				}
			}
		})
	}
}

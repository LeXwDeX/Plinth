package generationtool

import (
	"encoding/json"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"testing"
)

func TestSessionAdjustmentReasonOnlyFollowsItsExactValue(t *testing.T) {
	e := &Executor{offeringParameterReasons: func(string, string, string) map[string]generation.ParameterReason {
		return map[string]generation.ParameterReason{"quality": {Value: json.RawMessage(`"low"`), Reason: "draft"}}
	}}
	for _, tc := range []struct{ input, want string }{{`{"quality":"low"}`, "draft"}, {`{"quality":"max"}`, ""}} {
		got, err := e.parameterReasons(chat.ToolCommand{OwnerID: "owner", SessionID: "session"}, generation.Offering{ID: "offering"}, json.RawMessage(tc.input))
		if err != nil || got["quality"] != tc.want {
			t.Fatalf("%s %+v %v", tc.input, got, err)
		}
	}
}

func TestAdjustmentReasonDistinguishesPendingApprovalCards(t *testing.T) {
	p := generation.PreparedInput{OfferingID: "o", NormalizedInput: json.RawMessage(`{"quality":"low"}`)}
	old := ApprovalRequestKey("generate", p)
	p.ParameterAdjustments = []generation.ParameterAdjustment{{Name: "quality", Value: json.RawMessage(`"low"`), Reason: "draft"}}
	explained := ApprovalRequestKey("generate", p)
	p.ParameterAdjustments[0].Reason = "changed intent"
	changed := ApprovalRequestKey("generate", p)
	if old == explained || explained == changed {
		t.Fatal("pending card reused different explanation")
	}
	p.ParameterAdjustments = nil
	if ApprovalRequestKey("generate", p) != old {
		t.Fatal("empty adjustments changed historical duplicate identity")
	}
}

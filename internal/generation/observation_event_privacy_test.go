package generation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObservationEventPersistsOnlyProgressSummary(t *testing.T) {
	data := observationEventData(Observation{
		Phase: UpstreamRunning, Progress: 52,
		Job:       &UpstreamJob{ProviderID: "tripo", RemoteID: "private-job-id"},
		Opaque:    json.RawMessage(`{"remote_id":"private-job-id","url":"https://provider.example/private"}`),
		Artifacts: []ArtifactClaim{{ID: "private-job-id", Locator: "https://provider.example/private"}},
		DedupeKey: "private-job-id",
		Dispatch:  &DispatchReceipt{AckToken: "private-ack"},
	})
	if string(data) != `{"phase":"RUNNING","progress":52}` {
		t.Fatalf("event data = %s", data)
	}
	for _, forbidden := range []string{"private-job-id", "provider.example", "private-ack", "opaque", "artifacts", "job"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("event persisted private field %q: %s", forbidden, data)
		}
	}
}

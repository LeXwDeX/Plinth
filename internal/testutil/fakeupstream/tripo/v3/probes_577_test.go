package tripov3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// The free negative probes of the alignment script (#577) run against the fake as
// the official run will send them (the upload step first, for its file_token), and
// the fake answers each with the status, code and message the probe table states.
// None of them is a paid step, every one is opt-in, and none creates a task.
func TestFreeNegativeProbesGetTheAnswersTheTableStates(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{AccountConcurrencyLimit: -1})
	profile := Profile()
	var steps []align.Step
	for _, step := range profile.Steps {
		if step.Name == "upload" {
			steps = append(steps, step)
		}
	}
	probes := Probes()
	for _, probe := range probes {
		steps = append(steps, probe.Step)
		if probe.Step.Paid || !probe.Step.OptIn {
			t.Errorf("%s: a free probe is opt-in and not paid (Paid %v, OptIn %v)", probe.Step.Name, probe.Step.Paid, probe.Step.OptIn)
		}
		if !slices.ContainsFunc(profile.Steps, func(step align.Step) bool { return step.Name == probe.Step.Name }) {
			t.Errorf("%s is not in the alignment script", probe.Step.Name)
		}
	}
	target := align.Target{Name: "fake", BaseURL: f.base(), Key: key, AuthHeader: "Authorization", AuthPrefix: "Bearer ",
		Client: http.DefaultClient, Download: http.DefaultClient, NoPollDelay: true}
	bundle, err := align.Run(context.Background(), "tripo", "v3", target, steps, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.PaidRequests != 0 {
		t.Fatalf("the probes sent %d paid requests", bundle.PaidRequests)
	}
	observed := map[string]align.Obs{}
	for _, obs := range bundle.Obs {
		observed[obs.Step] = obs
	}
	for _, probe := range probes {
		obs, ran := observed[probe.Step.Name]
		if !ran || obs.Skipped != "" {
			t.Errorf("%s did not run: %+v", probe.Step.Name, obs)
			continue
		}
		if obs.Status != probe.WantStatus {
			t.Errorf("%s: HTTP %d, the table says %d", probe.Step.Name, obs.Status, probe.WantStatus)
		}
		var envelope map[string]any
		decoder := json.NewDecoder(bytes.NewReader(obs.Body))
		decoder.UseNumber()
		if err := decoder.Decode(&envelope); err != nil {
			t.Errorf("%s: the answer is not JSON: %v", probe.Step.Name, err)
			continue
		}
		if fmt.Sprint(envelope["code"]) == "0" {
			t.Errorf("%s was accepted: %v", probe.Step.Name, envelope)
		}
		if probe.WantCode != 0 && fmt.Sprint(envelope["code"]) != fmt.Sprint(probe.WantCode) {
			t.Errorf("%s: code %v, the table says %d", probe.Step.Name, envelope["code"], probe.WantCode)
		}
		if probe.WantMessage != "" && envelope["message"] != probe.WantMessage {
			t.Errorf("%s: message %q, the table says %q", probe.Step.Name, envelope["message"], probe.WantMessage)
		}
	}
	// Nothing the probes sent made a task.
	for _, obs := range bundle.Obs {
		if obs.Step == "upload" {
			continue
		}
		var envelope map[string]any
		_ = json.Unmarshal(obs.Body, &envelope)
		if id, _ := fakeupstream.Dig(envelope, "data", "task_id"); id != nil {
			t.Errorf("%s created task %v", obs.Step, id)
		}
	}
}

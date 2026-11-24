package tripov3

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// The free negative probes of #577 ran against the official API on 2026-11-25: the
// upload and eleven probes, twelve requests, no credit spent, no task made, every
// probe refused. evidence-577-negative-probes holds the upload and the nine JSON
// probes (the recorder keeps no empty or text/plain file part, so the two upload
// refusals are in the Probe table alone). It is evidence, not a template.
//
//go:embed testdata/evidence-577-negative-probes.candidate.json
var probesEvidenceFS embed.FS

const probesEvidenceFile = "testdata/evidence-577-negative-probes.candidate.json"

// The probe table states the real answers: every JSON probe of the official run is
// in the table with the request it sent, and the table's status, code, message,
// suggestion and request_id are what the official API answered.
func TestTheProbeTableHoldsTheRealAnswers(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(probesEvidenceFS, probesEvidenceFile)
	if err != nil {
		t.Fatal(err)
	}
	probes := Probes()
	matched := map[string]bool{}
	for _, exchange := range samples.Exchanges {
		if exchange.Request.Path == "/v3/files" {
			continue
		}
		var sent map[string]any
		if len(exchange.Request.Body.JSON) > 0 {
			if err := json.Unmarshal(exchange.Request.Body.JSON, &sent); err != nil {
				t.Fatal(err)
			}
		}
		index := slices.IndexFunc(probes, func(probe Probe) bool {
			return exchange.Request.Method == probe.Step.Method && exchange.Request.Path == "/v3"+probe.Step.Path &&
				sameProbeBody(probe.Step.Body, sent)
		})
		if index < 0 {
			t.Errorf("no probe sends %s %s %v", exchange.Request.Method, exchange.Request.Path, sent)
			continue
		}
		probe := probes[index]
		matched[probe.Step.Name] = true
		real := mustBody(t, exchange)
		if exchange.Response.Status != probe.WantStatus {
			t.Errorf("%s: the official API answered %d, the table says %d", probe.Step.Name, exchange.Response.Status, probe.WantStatus)
		}
		for name, want := range map[string]any{
			"code": probe.WantCode, "status": "error", "message": probe.WantMessage, "suggestion": probe.WantSuggestion,
		} {
			if fmt.Sprint(real[name]) != fmt.Sprint(want) {
				t.Errorf("%s: the official %s is %v, the table says %v", probe.Step.Name, name, real[name], want)
			}
		}
		if _, has := real["request_id"]; has != probe.WantRequestID {
			t.Errorf("%s: the official answer has a request_id %v, the table says %v", probe.Step.Name, has, probe.WantRequestID)
		}
	}
	for _, probe := range probes {
		if probe.Step.Multipart == nil && !matched[probe.Step.Name] {
			t.Errorf("%s has no official answer in the evidence", probe.Step.Name)
		}
	}
}

// sameProbeBody compares a probe's body with the body the official run sent. The
// sample's ids are placeholders ("id-N"), so a placeholder matches any string the
// probe sends there (its {file_token}, the zero task id).
func sameProbeBody(probe any, sent map[string]any) bool {
	if probe == nil {
		return len(sent) == 0
	}
	raw, err := json.Marshal(probe)
	if err != nil {
		return false
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil || len(want) != len(sent) {
		return false
	}
	for name, value := range sent {
		if text, ok := value.(string); ok && strings.HasPrefix(text, "id-") {
			if _, isText := want[name].(string); isText {
				continue
			}
			return false
		}
		if !reflect.DeepEqual(want[name], value) {
			return false
		}
	}
	return true
}

// The fake gives every probe the real answer of the table, envelope keys included,
// when the probes run against it as the official run sent them (the upload step
// first, for its file_token). None of them is a paid step, every one is opt-in, and
// none creates a task.
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
		if fmt.Sprint(envelope["code"]) != fmt.Sprint(probe.WantCode) {
			t.Errorf("%s: code %v, the table says %d", probe.Step.Name, envelope["code"], probe.WantCode)
		}
		if envelope["message"] != probe.WantMessage {
			t.Errorf("%s: message %q, the table says %q", probe.Step.Name, envelope["message"], probe.WantMessage)
		}
		if envelope["suggestion"] != probe.WantSuggestion {
			t.Errorf("%s: suggestion %q, the table says %q", probe.Step.Name, envelope["suggestion"], probe.WantSuggestion)
		}
		keys := []string{"code", "message", "status", "suggestion"}
		if probe.WantRequestID {
			keys = append(keys, "request_id")
		}
		slices.Sort(keys)
		var got []string
		for name := range envelope {
			got = append(got, name)
		}
		slices.Sort(got)
		if !slices.Equal(got, keys) {
			t.Errorf("%s: the envelope keys are %v, the real answer has %v", probe.Step.Name, got, keys)
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

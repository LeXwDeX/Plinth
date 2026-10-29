package align

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunPollsCapturesVariablesAndFetchesArtifacts(t *testing.T) {
	var polls atomic.Int32
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("POST /make", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"id":"t1"}}`)
	})
	mux.HandleFunc("GET /task/t1", func(w http.ResponseWriter, r *http.Request) {
		status := []string{"queued", "running", "done"}[min(int(polls.Add(1))-1, 2)]
		_, _ = fmt.Fprintf(w, `{"data":{"status":%q,"out":{"file":%q}}}`, status, server.URL+"/f.glb")
	})
	mux.HandleFunc("GET /f.glb", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "binary/octet-stream")
		_, _ = w.Write([]byte("glTF\x02\x00\x00\x00"))
	})
	server = httptest.NewServer(mux)
	defer server.Close()
	steps := []Step{
		{Name: "make", Method: "POST", Path: "/make", Body: map[string]any{"x": 1}, Paid: true, Capture: map[string]string{"id": "data.id"}},
		{Name: "no-auth", Method: "POST", Path: "/make", NoAuth: true, Body: map[string]any{}},
		{Name: "poll", Method: "GET", Path: "/task/{id}", RequireVar: "id", Fetch: []string{"data.out.*"},
			Poll: &Poll{StatusPath: "data.status", Terminal: []string{"done"}, Max: 5}},
		{Name: "skipped", Method: "GET", Path: "/x", RequireVar: "never"},
	}
	bundle, err := Run(context.Background(), "p", "v1", Target{Name: "t", BaseURL: server.URL, Key: "k", NoPollDelay: true}, steps, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Requests != 5 || bundle.PaidRequests != 1 {
		t.Fatalf("requests=%d paid=%d", bundle.Requests, bundle.PaidRequests)
	}
	poll := bundle.Obs[2]
	if got := distinct(poll.Polls); strings.Join(got, ",") != "queued,running,done" || len(poll.Artifacts) != 1 ||
		poll.Artifacts[0].Format != "glb" || poll.Artifacts[0].ContentType != "binary/octet-stream" {
		t.Fatalf("poll obs: %+v", poll)
	}
	if bundle.Obs[1].Status != http.StatusUnauthorized || bundle.Obs[1].AuthHeader != "none" || bundle.Obs[3].Skipped == "" {
		t.Fatalf("obs: %+v %+v", bundle.Obs[1], bundle.Obs[3])
	}
}

func TestRunPollGivesUpAtTheBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"s":"running"}`)
	}))
	defer server.Close()
	_, err := Run(context.Background(), "p", "v1", Target{BaseURL: server.URL, Key: "k", NoPollDelay: true}, []Step{
		{Name: "poll", Method: "GET", Path: "/", Poll: &Poll{StatusPath: "s", Terminal: []string{"done"}, Max: 3, Interval: time.Millisecond}},
	}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("err = %v", err)
	}
}

func bundleOf(obs ...Obs) *Bundle {
	return &Bundle{Provider: "p", API: "v1", Obs: obs, Requests: len(obs)}
}

func obs(step string, status int, body string, polls ...string) Obs {
	o := Obs{Step: step, Method: "GET", Path: "/x", AuthHeader: "Authorization: Bearer <key>", Status: status, Content: "application/json", Body: json.RawMessage(body)}
	for _, p := range polls {
		o.Polls = append(o.Polls, PollObs{Status: p})
	}
	return o
}

func TestCompareReportsEveryStructuralDifference(t *testing.T) {
	official := bundleOf(
		obs("same", 200, `{"a":1,"b":{"c":"x"}}`),
		obs("status", 200, `{"a":1}`),
		obs("fields", 200, `{"a":1,"b":2}`),
		obs("types", 200, `{"a":1}`),
		obs("flow", 200, `{}`, "running", "success"),
		obs("flow-missing", 200, `{}`, "queued", "success"),
	)
	fake := bundleOf(
		obs("same", 200, `{"b":{"c":"yy"},"a":99}`),
		obs("status", 404, `{"a":1}`),
		obs("fields", 200, `{"a":1,"z":2}`),
		obs("types", 200, `{"a":"1"}`),
		obs("flow", 200, `{}`, "queued", "running", "success"),
		obs("flow-missing", 200, `{}`, "running", "failed"),
	)
	report := Compare(official, fake, "https://official.example/v1", "2026-10-29", Notes{
		"fields|field-extra-in-fake|$.z": "the fake adds z on purpose",
	})
	have := map[string]bool{}
	for _, diff := range report.Diffs {
		have[diff.Key()] = diff.Note != ""
	}
	want := []string{
		"status|status|official 200, fake 404", "fields|field-missing-in-fake|$.b", "fields|field-extra-in-fake|$.z",
		"types|field-type|$.a: real number, fake string", "flow-missing|task-status-flow|official queued>success, fake running>failed",
	}
	for _, key := range want {
		if _, ok := have[key]; !ok {
			t.Errorf("missing diff %q in %v", key, have)
		}
	}
	for _, key := range []string{"same", "flow"} {
		for diff := range have {
			if strings.HasPrefix(diff, key+"|") {
				t.Errorf("unexpected diff %q", diff)
			}
		}
	}
	if !have["fields|field-extra-in-fake|$.z"] || len(report.Unexplained()) != len(want)-1 {
		t.Fatalf("notes not applied: unexplained %d", len(report.Unexplained()))
	}
	if !strings.Contains(report.Markdown(), "**未说明**") {
		t.Fatal("markdown hides the unexplained differences")
	}
}

func TestCompareSSEUsesTheFieldUnion(t *testing.T) {
	chunks := func(items ...string) []json.RawMessage {
		var out []json.RawMessage
		for _, item := range items {
			out = append(out, json.RawMessage(item))
		}
		return out
	}
	real := Obs{Step: "s", Method: "POST", Path: "/", Status: 200, Content: "text/event-stream", Done: true,
		Chunks: chunks(`{"d":{"content":null}}`, `{"d":{"content":"x"},"usage":{"n":1}}`)}
	same := real
	same.Chunks = chunks(`{"d":{"content":"a"}}`, `{"d":{"content":null},"usage":{"n":2}}`, `{"d":{"content":""}}`)
	if report := Compare(bundleOf(real), bundleOf(same), "u", "d", nil); len(report.Diffs) != 0 {
		t.Fatalf("diffs: %+v", report.Diffs)
	}
	broken := real
	broken.Chunks = chunks(`{"d":{"content":"a"}}`)
	broken.Done = false
	report := Compare(bundleOf(real), bundleOf(broken), "u", "d", nil)
	if len(report.Diffs) < 2 {
		t.Fatalf("a missing usage field and [DONE] must be reported: %+v", report.Diffs)
	}
}

func TestDetectFormatNamesTheDeliveredFormats(t *testing.T) {
	stl := append(make([]byte, 80), 1, 0, 0, 0)
	stl = append(stl, make([]byte, 50)...)
	for name, tc := range map[string]struct {
		data []byte
		want string
	}{
		"glb": {[]byte("glTF\x02\x00\x00\x00"), "glb"}, "fbx": {[]byte("Kaydara FBX Binary  \x00"), "fbx-binary"},
		"png": {[]byte("\x89PNG\r\n\x1a\n...."), "png"}, "webp": {[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "webp"},
		"jpeg": {[]byte("\xff\xd8\xff\xe0"), "jpeg"}, "zip": {[]byte("PK\x03\x04"), "zip"},
		"stl binary": {stl, "stl-binary"}, "stl ascii": {[]byte("solid x"), "stl-ascii"}, "binary": {[]byte{0, 1, 2}, "binary"},
	} {
		if got := DetectFormat(tc.data); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestTeeAndReplayServeASubsetOfARecording(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
	}))
	defer server.Close()
	tee := NewTee(nil)
	client := &http.Client{Transport: tee}
	for _, path := range []string{"/a", "/b", "/a"} {
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	file := t.TempDir() + "/raw.json"
	if err := tee.Save(file); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(file)
	if err != nil {
		t.Fatal(err)
	}
	server.Close() // the replay must not need the network
	client = &http.Client{Transport: replay}
	for i := 0; i < 2; i++ {
		response, err := client.Get(server.URL + "/a")
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		response.Body.Close()
	}
	if _, err := client.Get(server.URL + "/a"); err == nil {
		t.Fatal("a third /a has no recording")
	}
}

// Two steps hit one endpoint: a rejected empty create and the real one. A
// replay of the real step alone must not receive the rejection's recording.
func TestReplayOfASubsetKeepsStepsOnTheSameEndpointApart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if len(body) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"model is required"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"id":"t1"}}`)
	}))
	defer server.Close()
	steps := []Step{
		{Name: "create-invalid", Method: "POST", Path: "/create", Body: map[string]any{}},
		{Name: "create", Method: "POST", Path: "/create", Body: map[string]any{"model": "m"}, Capture: map[string]string{"id": "data.id"}},
	}
	tee := NewTee(nil)
	if _, err := Run(context.Background(), "p", "v1", Target{BaseURL: server.URL, Key: "k", Client: &http.Client{Transport: tee}}, steps, nil, nil); err != nil {
		t.Fatal(err)
	}
	file := t.TempDir() + "/raw.json"
	if err := tee.Save(file); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(file)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	bundle, err := Run(context.Background(), "p", "v1", Target{BaseURL: server.URL, Key: "k", Client: &http.Client{Transport: replay}}, steps[1:], nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := bundle.Obs[0]; got.Status != http.StatusOK || !strings.Contains(string(got.Body), `"t1"`) {
		t.Fatalf("the real create replayed %d %s", got.Status, got.Body)
	}
}

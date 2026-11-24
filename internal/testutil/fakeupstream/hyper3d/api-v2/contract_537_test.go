package hyper3dapiv2

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// The contract checks of #537: rules of the official pages (read 2026-11-24 from
// the pages' prose and their embedded OpenAPI schemas) that the fake did not hold
// the Adapter to. Every rejection here is constructed (no wire sample), except
// where a test says the page names the code.

func repeat(name string, count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = name
	}
	return names
}

// I14: the page's multipart field for the Texture reference is images (the schema
// allows 1 to 5, the prose says exactly one). Plinth sends the singular image,
// which the real service tolerated on 2026-10-29. The fake takes both until a
// real request settles it.
func TestTextureTakesTheReferenceUnderImagesOrImage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	fields := map[string]string{"material": "PBR"}
	for name, files := range map[string][]string{
		"singular image (observed)":     {"model", "image"},
		"images (the page)":             {"model", "images"},
		"five images":                   append([]string{"model"}, repeat("images", 5)...),
		"both fields, within the count": {"model", "image", "images"},
	} {
		if status, _, body := f.Do("POST", Prefix+"/rodin_texture_only", key, form(fields, files...)); status != http.StatusCreated || body["error"] != nil {
			t.Errorf("%s = %d %v", name, status, body)
		}
	}
	for name, files := range map[string][]string{
		"no reference":          {"model"},
		"six images":            append([]string{"model"}, repeat("images", 6)...),
		"six across both names": append([]string{"model", "image"}, repeat("images", 5)...),
		"no model":              {"images"},
	} {
		if status, _, body := f.Do("POST", Prefix+"/rodin_texture_only", key, form(fields, files...)); status != http.StatusCreated || body["error"] != "INVALID_REQUEST" {
			t.Errorf("%s = %d %v, want INVALID_REQUEST", name, status, body)
		}
	}
}

// The Texture page lists image_label with the same rule as the Gen-2.5 page.
func TestTextureImageLabelCountIsValidated(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	fields := map[string]string{"material": "PBR"}
	if status, _, body := f.Do("POST", Prefix+"/rodin_texture_only", key, labeledForm(fields, 1, "model", "images")); status != http.StatusCreated || body["error"] != nil {
		t.Fatalf("one label for one image = %d %v", status, body)
	}
	if status, _, body := f.Do("POST", Prefix+"/rodin_texture_only", key, labeledForm(fields, 2, "model", "images")); status != http.StatusCreated || body["error"] != "IMAGE_LABEL_LENGTH_TOO_LONG" {
		t.Fatalf("two labels for one image = %d %v", status, body)
	}
}

// I15: the submission refusals follow the same "first N requests" counter as the
// other injected faults, so "wait for one task to finish, then retry" can be
// replayed. Without a count the refusal stays permanent (the old behaviour).
func TestSubmissionRefusalsClearAfterTheConfiguredCount(t *testing.T) {
	refused := func(status int, body map[string]any, code string) bool {
		return status == http.StatusCreated && body["error"] == code
	}
	t.Run("header count", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		for attempt := 1; attempt <= 2; attempt++ {
			status, _, body := f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", "concurrency-limit", "X-Fake-Fail-Times", "2")
			if !refused(status, body, "API_PARALLELISM_LIMIT_REACHED") {
				t.Fatalf("attempt %d = %d %v, want the concurrency refusal", attempt, status, body)
			}
		}
		status, _, body := f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", "concurrency-limit", "X-Fake-Fail-Times", "2")
		if status != http.StatusCreated || body["error"] != nil || body["uuid"] == nil {
			t.Fatalf("the third attempt = %d %v, want an accepted task", status, body)
		}
	})
	t.Run("config count and every refusal code", func(t *testing.T) {
		for scenario, code := range map[string]string{
			"concurrency-limit": "API_PARALLELISM_LIMIT_REACHED", "permission-denied": "PERMISSION_DENIED",
			"no-active-subscription": "API_NO_ACTIVE_SUBSCRIPTION", "plan-too-low": "API_SUBSCRIPTION_PLAN_TOO_LOW",
			"object-not-found-on-image": "API_OBJECT_NOT_FOUND_ON_IMAGE", "content-violation": "IMAGE_CONTENT_VIOLATION",
			"api-unknown": "API_UNKNOWN",
		} {
			f := newFixture(t, fakeupstream.Config{FailTimes: 1})
			status, _, body := f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", scenario)
			if !refused(status, body, code) {
				t.Errorf("%s first request = %d %v", scenario, status, body)
			}
			if status, _, body := f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", scenario); status != http.StatusCreated || body["error"] != nil {
				t.Errorf("%s second request = %d %v, want accepted", scenario, status, body)
			}
		}
	})
	t.Run("key scenario with a count", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{FailTimes: 1})
		status, _, body := f.Do("POST", Prefix+"/rodin", "fake-concurrency-limit-run1", textForm())
		if !refused(status, body, "API_PARALLELISM_LIMIT_REACHED") {
			t.Fatalf("first = %d %v", status, body)
		}
		if status, _, body := f.Do("POST", Prefix+"/rodin", "fake-concurrency-limit-run1", textForm()); status != http.StatusCreated || body["error"] != nil {
			t.Fatalf("second = %d %v", status, body)
		}
	})
	t.Run("no count stays refused", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{})
		for attempt := 1; attempt <= 4; attempt++ {
			status, _, body := f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", "concurrency-limit")
			if !refused(status, body, "API_PARALLELISM_LIMIT_REACHED") {
				t.Fatalf("attempt %d = %d %v", attempt, status, body)
			}
		}
	})
}

func bangFinished(t *testing.T, f *testkit.Fixture, source submitted) submitted {
	t.Helper()
	bang := submit(t, f, "/bang", form(map[string]string{"asset_id": source.uuid, "material": "None"}))
	poll(t, f, bang)
	return bang
}

// I16: the Bang input rules of the page. The page says asset_id and model are
// mutually exclusive ("Sending both ... or neither, is rejected"), that asset_id
// mode omits model, image and prompt, and that HTTP 400 covers "conflicting input
// modes"; it names INVALID_REQUEST for a model without image when material is not
// None. The status of the conflict rejections is constructed from that sentence.
func TestBangInputRulesOfTheOfficialPage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := submit(t, f, "/rodin", textForm())
	poll(t, f, gen)

	conflict := func(name string, fields map[string]string, files ...string) {
		t.Helper()
		status, _, body := f.Do("POST", Prefix+"/bang", key, form(fields, files...))
		if status != http.StatusBadRequest || body["error"] != "Bad Request" {
			t.Errorf("%s = %d %v, want the 400 envelope", name, status, body)
		}
	}
	conflict("asset_id and model", map[string]string{"asset_id": gen.uuid, "material": "None"}, "model")
	conflict("asset_id and image", map[string]string{"asset_id": gen.uuid, "material": "PBR"}, "image")
	conflict("asset_id and prompt", map[string]string{"asset_id": gen.uuid, "material": "PBR", "prompt": "p"})

	invalid := func(name string, fields map[string]string, files ...string) {
		t.Helper()
		status, _, body := f.Do("POST", Prefix+"/bang", key, form(fields, files...))
		if status != http.StatusCreated || body["error"] != "INVALID_REQUEST" {
			t.Errorf("%s = %d %v, want INVALID_REQUEST", name, status, body)
		}
		if message, _ := body["message"].(string); message == "" {
			t.Errorf("%s: the page promises a descriptive message: %v", name, body)
		}
	}
	invalid("neither asset_id nor model", map[string]string{"material": "None"})
	invalid("model without image, material PBR", map[string]string{"material": "PBR"}, "model")
	invalid("model without image, material omitted", map[string]string{"strength": "5"}, "model")

	accepted := func(name string, fields map[string]string, files ...string) submitted {
		t.Helper()
		status, _, body := f.Do("POST", Prefix+"/bang", key, form(fields, files...))
		if status != http.StatusCreated || body["error"] != nil || body["uuid"] == nil {
			t.Fatalf("%s = %d %v, want accepted", name, status, body)
		}
		return submitted{uuid: body["uuid"].(string)}
	}
	accepted("model without image, material None", map[string]string{"material": "None"}, "model")
	accepted("model with image, material PBR", map[string]string{"material": "PBR"}, "model", "image")
	accepted("model with a prompt", map[string]string{"material": "PBR", "prompt": "split"}, "model", "image")
	accepted("asset_id alone", map[string]string{"asset_id": gen.uuid, "material": "PBR"})
}

// The page says asset_id is the uuid of "a completed Rodin generation task": a
// Bang or Texture result is not one. The code (ASSET_NOT_AVAILABLE, the page's
// "not in a state that can be operated on") is constructed for this case; an
// unfinished generation is the observed one.
func TestBangAssetMustBeACompletedRodinGeneration(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	gen := submit(t, f, "/rodin", textForm())
	poll(t, f, gen)
	bang := bangFinished(t, f, gen)
	texture := submit(t, f, "/rodin_texture_only", form(map[string]string{"material": "PBR"}, "model", "image"))
	poll(t, f, texture)
	for name, source := range map[string]submitted{"a Bang result": bang, "a Texture result": texture} {
		status, _, body := f.Do("POST", Prefix+"/bang", key, form(map[string]string{"asset_id": source.uuid, "material": "None"}))
		if status != http.StatusCreated || body["error"] != "ASSET_NOT_AVAILABLE" {
			t.Errorf("asset_id of %s = %d %v, want ASSET_NOT_AVAILABLE", name, status, body)
		}
	}
	if status, _, body := f.Do("POST", Prefix+"/bang", key, form(map[string]string{"asset_id": gen.uuid, "material": "None"})); status != http.StatusCreated || body["error"] != nil {
		t.Errorf("asset_id of the finished Rodin generation = %d %v", status, body)
	}
}

// m6: the page's queue_length is "an approximate count of jobs ahead" of a
// Waiting job and is "omitted once the job leaves the queue". The real polls
// carried it on the next job in line only (value 0); here that job's count falls
// poll by poll to the observed 0 while the task queues.
func TestQueueLengthCountsDownWhileQueued(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{QueuedPolls: 4})
	s := submit(t, f, "/rodin", textForm())
	var seen []int64
	for i := 0; i < 60; i++ {
		_, _, body := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": s.key})
		jobs := body["jobs"].([]any)
		first := jobs[0].(map[string]any)
		length, has := first["queue_length"]
		if first["status"] != statusWaiting {
			if has {
				t.Fatalf("a job that left the queue carries queue_length: %v", first)
			}
		} else if has {
			seen = append(seen, int64(length.(float64)))
		} else {
			t.Fatalf("a Waiting first job lacks queue_length: %v", first)
		}
		if _, ended := statusOf(t, body); ended {
			break
		}
	}
	want := []int64{3, 2, 1, 0}
	if len(seen) < len(want) {
		t.Fatalf("queue_length values %v, want to start %v", seen, want)
	}
	for i, value := range want {
		if seen[i] != value {
			t.Fatalf("queue_length values %v, want to start %v", seen, want)
		}
	}
}

// m6: Check Balance returns "usage" only when usage_page is given: one-based, up
// to 20 records a page, newest first, each with time, item, amount, task_uuid,
// api_key_id and api_key_description. The page-size and ordering are the page's;
// item values other than api_generation_rodin are constructed.
func TestBalanceUsageIsPaged(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, body := f.Do("GET", Prefix+"/check_balance", key, nil)
	if status != http.StatusOK || body["usage"] != nil {
		t.Fatalf("balance without usage_page = %d %v, want no usage", status, body)
	}
	var tasks []submitted
	for i := 0; i < 21; i++ {
		tasks = append(tasks, submit(t, f, "/rodin", textForm()))
	}
	status, _, body = f.Do("GET", Prefix+"/check_balance?usage_page=1", key, nil)
	usage, _ := body["usage"].([]any)
	if status != http.StatusOK || body["balance"] == nil || len(usage) != 20 {
		t.Fatalf("usage_page=1 = %d, %d records, want 20 (%v)", status, len(usage), body)
	}
	newest := usage[0].(map[string]any)
	if newest["task_uuid"] != tasks[20].uuid || newest["item"] != "api_generation_rodin" || newest["amount"].(float64) != 0.5 {
		t.Fatalf("the first record is not the newest task: %v", newest)
	}
	for _, field := range []string{"time", "item", "amount", "task_uuid", "api_key_id", "api_key_description"} {
		if _, ok := newest[field]; !ok {
			t.Errorf("usage record lacks %s: %v", field, newest)
		}
	}
	_, _, body = f.Do("GET", Prefix+"/check_balance?usage_page=2", key, nil)
	if rest, _ := body["usage"].([]any); len(rest) != 1 || rest[0].(map[string]any)["task_uuid"] != tasks[0].uuid {
		t.Fatalf("usage_page=2 = %v, want the oldest task only", body)
	}
	_, _, body = f.Do("GET", Prefix+"/check_balance?usage_page=3", key, nil)
	if rest, ok := body["usage"].([]any); !ok || len(rest) != 0 {
		t.Fatalf("usage_page=3 = %v, want an empty usage array", body)
	}
	for _, bad := range []string{"0", "-1", "x", ""} {
		if status, _, body := f.Do("GET", Prefix+"/check_balance?usage_page="+bad, key, nil); status != http.StatusBadRequest {
			t.Errorf("usage_page=%q = %d %v, want 400 (the page's minimum is 1)", bad, status, body)
		}
	}
}

// The errors page's example body for API_INSUFFICIENT_FUNDS (read 2026-11-24).
func TestInsufficientFundsCarriesTheOfficialExampleMessage(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, body := f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", "insufficient-balance")
	want := "The fund in your wallet is insufficient to perform this operation."
	if status != http.StatusCreated || body["error"] != "API_INSUFFICIENT_FUNDS" || body["message"] != want {
		t.Fatalf("%d %v, want the example message %q", status, body, want)
	}
}

// m6: the schema says is_micro is "only allowed when tier is Gen-2.5-Extreme-High;
// any other tier is rejected with INVALID_REQUEST", and prompt has maxLength 1024
// on the Gen-2.5 and Texture pages.
func TestIsMicroAndPromptLengthRules(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	send := func(route string, fields map[string]string, files ...string) (int, map[string]any) {
		status, _, body := f.Do("POST", Prefix+route, key, form(fields, files...))
		return status, body
	}
	base := func(extra map[string]string) map[string]string {
		fields := map[string]string{"geometry_file_format": "glb", "material": "PBR", "prompt": "a robot"}
		for name, value := range extra {
			fields[name] = value
		}
		return fields
	}
	for _, tc := range []struct {
		name   string
		fields map[string]string
		ok     bool
	}{
		{"is_micro on Extreme-High", base(map[string]string{"tier": extremeHighTier, "is_micro": "true"}), true},
		{"is_micro false on High", base(map[string]string{"tier": "Gen-2.5-High", "is_micro": "false"}), true},
		{"is_micro on High", base(map[string]string{"tier": "Gen-2.5-High", "is_micro": "true"}), false},
		{"is_micro on Extreme-Low", base(map[string]string{"tier": "Gen-2.5-Extreme-Low", "is_micro": "true"}), false},
		{"is_micro without a tier", base(map[string]string{"is_micro": "true"}), false},
		{"prompt of 1024 characters", base(map[string]string{"tier": "Gen-2.5-Extreme-Low", "prompt": strings.Repeat("a", 1024)}), true},
		{"prompt of 1025 characters", base(map[string]string{"tier": "Gen-2.5-Extreme-Low", "prompt": strings.Repeat("a", 1025)}), false},
		{"prompt of 1024 multibyte characters", base(map[string]string{"tier": "Gen-2.5-Extreme-Low", "prompt": strings.Repeat("模", 1024)}), true},
	} {
		status, body := send("/rodin", tc.fields)
		if tc.ok && (status != http.StatusCreated || body["error"] != nil) {
			t.Errorf("%s = %d %v, want accepted", tc.name, status, body)
		}
		if !tc.ok && (status != http.StatusCreated || body["error"] != "INVALID_REQUEST") {
			t.Errorf("%s = %d %v, want INVALID_REQUEST", tc.name, status, body)
		}
	}
	if status, body := send("/rodin_texture_only", map[string]string{"prompt": strings.Repeat("a", 1025)}, "model", "images"); status != http.StatusCreated || body["error"] != "INVALID_REQUEST" {
		t.Errorf("a Texture prompt of 1025 characters = %d %v, want INVALID_REQUEST", status, body)
	}
	if status, body := send("/rodin_texture_only", map[string]string{"prompt": strings.Repeat("a", 1024)}, "model", "images"); status != http.StatusCreated || body["error"] != nil {
		t.Errorf("a Texture prompt of 1024 characters = %d %v, want accepted", status, body)
	}
}

// The download of a task that exists keeps the observed answers (an empty list
// while unfinished or after Failed); the documented INVALID_REQUEST ("no
// downloadable files for this user") has no trigger the fake can tell apart, and
// a task of another key is NO_SUCH_TASK ("owned by this user"). Pinned here so a
// later change does not invent one silently.
func TestDownloadOfAnExistingTaskStaysAnEmptyListUntilFinished(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	s := submit(t, f, "/rodin", textForm())
	if list := download(t, f, s); len(list) != 0 {
		t.Fatalf("an unfinished task lists files: %v", list)
	}
	poll(t, f, s)
	if list := download(t, f, s); len(list) == 0 {
		t.Fatal("a finished task lists no files")
	}
}

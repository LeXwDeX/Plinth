package hyper3dapiv2

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

const key = "test-key"

func newFixture(t *testing.T, config fakeupstream.Config) *testkit.Fixture {
	t.Helper()
	return testkit.New(t, config, Version())
}

// form builds a multipart body: text fields plus empty files under the names
// listed in files.
func form(fields map[string]string, files ...string) testkit.Body {
	return labeledForm(fields, 0, files...)
}

// labeledForm is form plus count image_label text fields, each a mark of the
// page's list.
func labeledForm(fields map[string]string, labels int, files ...string) testkit.Body {
	marks := make([]string, 0, labels)
	for i := 0; i < labels; i++ {
		marks = append(marks, generationImageLabels[i%len(generationImageLabels)])
	}
	return markedForm(fields, marks, files...)
}

// markedForm is form plus one image_label text field per mark, in order, ahead
// of the files (the wire of the real requests of #376).
func markedForm(fields map[string]string, marks []string, files ...string) testkit.Body {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for name, value := range fields {
		_ = writer.WriteField(name, value)
	}
	for _, mark := range marks {
		_ = writer.WriteField("image_label", mark)
	}
	for _, name := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+name+`"; filename="`+name+`.bin"`)
		header.Set("Content-Type", "application/octet-stream")
		part, _ := writer.CreatePart(header)
		// A model the size of a real one; the tiny fixture GLB is its own test.
		if name == "model" {
			_, _ = part.Write(bytes.Repeat([]byte("m"), 2*tinyModelBytes))
		} else {
			_, _ = part.Write([]byte("data"))
		}
	}
	_ = writer.Close()
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

func textForm() testkit.Body {
	return form(map[string]string{"tier": "Gen-2.5-Extreme-Low", "geometry_file_format": "glb", "material": "PBR", "prompt": "a robot"})
}

type submitted struct {
	uuid, key string
	jobs      int
}

func submit(t *testing.T, f *testkit.Fixture, route string, body testkit.Body, headers ...string) submitted {
	t.Helper()
	status, _, response := f.Do("POST", Prefix+route, key, body, headers...)
	if status != http.StatusCreated {
		t.Fatalf("POST %s = %d %v", route, status, response)
	}
	jobs, _ := testkit.Field(t, response, "jobs", "uuids").([]any)
	return submitted{uuid: testkit.Field(t, response, "uuid").(string), key: testkit.Field(t, response, "jobs", "subscription_key").(string), jobs: len(jobs)}
}

// poll returns the aggregated status flow (the first job that is not Done, else
// Done or Failed) until every job ended.
func poll(t *testing.T, f *testkit.Fixture, s submitted) []string {
	t.Helper()
	var flow []string
	for i := 0; i < 60; i++ {
		status, _, body := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": s.key})
		if status != http.StatusCreated {
			t.Fatalf("status = %d %v", status, body)
		}
		state, ended := statusOf(t, body)
		if len(flow) == 0 || flow[len(flow)-1] != state {
			flow = append(flow, state)
		}
		if ended {
			return flow
		}
	}
	t.Fatalf("jobs never ended: %v", flow)
	return nil
}

func statusOf(t *testing.T, body map[string]any) (state string, ended bool) {
	t.Helper()
	jobs, _ := body["jobs"].([]any)
	state, ended = statusDone, true
	for _, job := range jobs {
		word, _ := job.(map[string]any)["status"].(string)
		switch word {
		case statusFailed:
			return statusFailed, true
		case statusDone:
		default:
			if ended {
				state = word
			}
			ended = false
		}
	}
	return state, ended
}

func download(t *testing.T, f *testkit.Fixture, s submitted) []any {
	t.Helper()
	status, _, body := f.Do("POST", Prefix+"/download", key, map[string]any{"task_uuid": s.uuid})
	if status != http.StatusCreated {
		t.Fatalf("download = %d %v", status, body)
	}
	return testkit.Field(t, body, "list").([]any)
}

func TestGenerationCompletesAndDeliversValidFiles(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	s := submit(t, f, "/rodin", textForm())
	// Asked early, the official API answers an empty list.
	if list := download(t, f, s); len(list) != 0 {
		t.Fatalf("download before the jobs ended = %v", list)
	}
	flow := poll(t, f, s)
	if got := strings.Join(flow, ">"); !strings.HasPrefix(got, "Waiting>Generating") || !strings.HasSuffix(got, "Done") {
		t.Fatalf("flow = %s", got)
	}
	list := download(t, f, s)
	if len(list) != 2 {
		t.Fatalf("list = %v", list)
	}
	names := map[string]string{}
	for _, item := range list {
		entry := item.(map[string]any)
		names[entry["name"].(string)] = entry["url"].(string)
	}
	for name, wantType := range map[string]string{"base_basic_pbr.glb": "model/gltf-binary", "preview.webp": "image/webp"} {
		status, contentType, data := f.Download(names[name])
		if status != http.StatusOK || contentType != wantType || len(data) == 0 {
			t.Fatalf("%s = %d %q %d bytes", name, status, contentType, len(data))
		}
		if strings.HasSuffix(name, ".glb") {
			if err := testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
				t.Fatalf("GLB rejected by the delivery check: %v", err)
			}
		}
	}
}

func TestBangAndTextureNeedTheirInputs(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	// A task id that names no task is the documented NO_SUCH_TASK; only an
	// existing task whose state does not allow the operation is
	// ASSET_NOT_AVAILABLE.
	status, _, body := f.Do("POST", Prefix+"/bang", key, form(map[string]string{"asset_id": "00000000-0000-4000-8000-000000000001"}))
	if status != http.StatusCreated || body["error"] != "NO_SUCH_TASK" {
		t.Fatalf("bang of an unknown asset = %d %v", status, body)
	}
	gen := submit(t, f, "/rodin", textForm())
	if _, _, body := f.Do("POST", Prefix+"/bang", key, form(map[string]string{"asset_id": gen.uuid})); body["error"] != "ASSET_NOT_AVAILABLE" {
		t.Fatalf("bang of an unfinished task = %v", body)
	}
	poll(t, f, gen)
	bang := submit(t, f, "/bang", form(map[string]string{"asset_id": gen.uuid, "material": "PBR", "resolution": "High"}))
	poll(t, f, bang)
	list := download(t, f, bang)
	if name := list[0].(map[string]any)["name"].(string); !strings.HasSuffix(name, "_pbr.glb") {
		t.Fatalf("a PBR bang must offer a _pbr.glb model, got %q", name)
	}
	if _, _, body := f.Do("POST", Prefix+"/rodin_texture_only", key, form(map[string]string{"prompt": "p"}, "model")); body["error"] != "INVALID_REQUEST" {
		t.Fatalf("texture without an image = %v", body)
	}
	texture := submit(t, f, "/rodin_texture_only", form(map[string]string{"material": "PBR"}, "model", "image"))
	poll(t, f, texture)
	if len(download(t, f, texture)) != 1 {
		t.Fatal("texture must offer one model")
	}
}

// Observed twice on the official service (#96 phase 3): a texture request whose
// model is the 400-byte one-triangle fixture GLB is accepted and then ends with
// every job Failed and a top-level message; the paid retry fails the same way.
func TestTinyTextureModelEndsFailed(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("material", "PBR")
	for _, name := range []string{"model", "image"} {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+name+`"; filename="`+name+`.bin"`)
		part, _ := writer.CreatePart(header)
		_, _ = part.Write(bytes.Repeat([]byte("t"), 400))
	}
	_ = writer.Close()
	s := submit(t, f, "/rodin_texture_only", testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()})
	if flow := poll(t, f, s); flow[len(flow)-1] != "Failed" {
		t.Fatalf("status flow = %v, want to end Failed", flow)
	}
	_, _, body := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": s.key})
	if body["message"] != "Invalid input." {
		t.Fatalf("a Failed poll carries the message: %v", body)
	}
	if list := download(t, f, s); len(list) != 0 {
		t.Fatalf("a failed task offers files: %v", list)
	}
}

// The official Rodin page documents 1-5 reference images; the upper bound is
// constructed from those docs (#191), while 0 images without a prompt reuses
// the observed INVALID_REQUEST shape.
func TestRodinImageCountBoundary(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	fields := map[string]string{"tier": "Gen-2.5-Extreme-Low", "geometry_file_format": "glb", "material": "PBR"}
	images := func(count int) []string {
		names := make([]string, count)
		for index := range names {
			names[index] = "images"
		}
		return names
	}
	for _, count := range []int{1, 5} {
		status, _, body := f.Do("POST", Prefix+"/rodin", key, form(fields, images(count)...))
		if status != http.StatusCreated || body["error"] != nil {
			t.Fatalf("%d images = %d %v", count, status, body)
		}
	}
	if status, _, body := f.Do("POST", Prefix+"/rodin", key, form(fields, images(6)...)); status != http.StatusCreated || body["error"] != "INVALID_REQUEST" {
		t.Fatalf("6 images = %d %v", status, body)
	}
	if status, _, body := f.Do("POST", Prefix+"/rodin", key, form(fields)); status != http.StatusCreated || body["error"] != "INVALID_REQUEST" {
		t.Fatalf("0 images without a prompt = %d %v", status, body)
	}
}

// The value rules of the Gen-2.5 form are constructed from the official page
// (https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5, checked
// 2026-11-12); no wire sample covers a rejected value. Valid combinations are
// accepted, and a value outside the page's ranges is the documented
// INVALID_REQUEST with a message.
func TestGenerationFormValueRules(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	base := func(extra map[string]string) map[string]string {
		fields := map[string]string{
			"tier": "Gen-2.5-Extreme-High", "geometry_file_format": "glb", "material": "PBR", "prompt": "a robot",
		}
		for name, value := range extra {
			fields[name] = value
		}
		return fields
	}
	for name, extra := range map[string]map[string]string{
		"the display request": {"mesh_mode": "Raw", "quality": "high", "quality_override": "2000000", "texture_mode": "extreme-high"},
		"a game character": {
			"mesh_mode": "Quad", "quality": "high", "quality_override": "10000", "texture_mode": "high",
			"TAPose": "true", "quad_normal": "true",
		},
		"the legacy texture mode":          {"texture_mode": "legacy"},
		"the lowest texture mode":          {"texture_mode": "extreme-low"},
		"a middle texture mode":            {"texture_mode": "medium"},
		"the Quad ceiling":                 {"mesh_mode": "Quad", "quality_override": "200000"},
		"the lowest face count":            {"mesh_mode": "Quad", "quality_override": "500"},
		"a Raw count above the Quad limit": {"mesh_mode": "Raw", "quality_override": "250000"},
		"a Raw default mode on Gen-2.5":    {"quality_override": "2000000"},
		"the seed bounds":                  {"seed": "0"},
		"the highest seed":                 {"seed": "65535"},
		"every quality preset in Quad":     {"mesh_mode": "Quad", "quality": "extra-low"},
		"the switches off":                 {"TAPose": "false", "quad_normal": "false"},
		"the lower-tier ceiling":           {"tier": "Gen-2.5-Medium", "quality_override": "1000000"},
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			status, _, body := f.Do("POST", Prefix+"/rodin", key, form(base(extra)))
			if status != http.StatusCreated || body["error"] != nil {
				t.Fatalf("%v = %d %v", extra, status, body)
			}
		})
	}
	for name, tc := range map[string]struct {
		extra   map[string]string
		message string
	}{
		"Quad above its ceiling":              {map[string]string{"mesh_mode": "Quad", "quality_override": "200001"}, "200000 in Quad mode"},
		"Quad at the display count":           {map[string]string{"mesh_mode": "Quad", "quality_override": "2000000"}, "200000 in Quad mode"},
		"an omitted tier defaults to Quad":    {map[string]string{"tier": "", "quality_override": "300000"}, "200000 in Quad mode"},
		"below the lowest count":              {map[string]string{"quality_override": "499"}, "between 500 and 2000000"},
		"above the highest count":             {map[string]string{"quality_override": "2000001"}, "between 500 and 2000000"},
		"a fractional count":                  {map[string]string{"quality_override": "1500.5"}, "between 500 and 2000000"},
		"a count that is not a number":        {map[string]string{"quality_override": "many"}, "between 500 and 2000000"},
		"a lower tier above its ceiling":      {map[string]string{"tier": "Gen-2.5-Medium", "quality_override": "1000001"}, "1000000 for tier Gen-2.5-Medium"},
		"a seed above the range":              {map[string]string{"seed": "65536"}, "between 0 and 65535"},
		"a negative seed":                     {map[string]string{"seed": "-1"}, "between 0 and 65535"},
		"a seed that is not a number":         {map[string]string{"seed": "lucky"}, "between 0 and 65535"},
		"an unknown mesh mode":                {map[string]string{"mesh_mode": "quad"}, "mesh_mode must be one of"},
		"an unknown quality preset":           {map[string]string{"quality": "ultra"}, "quality must be one of"},
		"an unknown texture mode":             {map[string]string{"texture_mode": "ultra"}, "texture_mode must be one of"},
		"a texture mode in the wrong case":    {map[string]string{"texture_mode": "Extreme-High"}, "texture_mode must be one of"},
		"a pose switch that is not a boolean": {map[string]string{"TAPose": "yes"}, "TAPose must be a boolean"},
		"a bake switch that is not a boolean": {map[string]string{"quad_normal": "1"}, "quad_normal must be a boolean"},
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			fields := base(tc.extra)
			if fields["tier"] == "" {
				delete(fields, "tier")
			}
			status, _, body := f.Do("POST", Prefix+"/rodin", key, form(fields))
			message, _ := body["message"].(string)
			if status != http.StatusCreated || body["error"] != "INVALID_REQUEST" || !strings.Contains(message, tc.message) {
				t.Fatalf("%v = %d %v, want INVALID_REQUEST mentioning %q", tc.extra, status, body, tc.message)
			}
			if body["uuid"] != nil {
				t.Fatalf("a rejected request must not create a task: %v", body)
			}
		})
	}
	// The rules belong to the Gen-2.5 generation form only: Bang and Texture
	// keep accepting what they accepted.
	if status, _, body := f.Do("POST", Prefix+"/rodin_texture_only", key, form(map[string]string{"material": "PBR", "quality_override": "250000"}, "model", "image")); status != http.StatusCreated || body["error"] != nil {
		t.Fatalf("texture = %d %v", status, body)
	}
}

func TestRequestValidation(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, tc := range map[string]struct {
		method, path, key string
		body              any
		status            int
		field, value      string
	}{
		"no key balance":     {"GET", "/check_balance", "", nil, http.StatusUnauthorized, "error", "UNAUTHORIZED"},
		"no key rodin":       {"POST", "/rodin", "", textForm(), http.StatusUnauthorized, "error", "UNAUTHORIZED"},
		"no prompt or image": {"POST", "/rodin", key, form(map[string]string{"tier": "Gen-2.5-Extreme-Low"}), http.StatusCreated, "error", "INVALID_REQUEST"},
		"not multipart":      {"POST", "/rodin", key, map[string]any{"prompt": "p"}, http.StatusBadRequest, "error", "Bad Request"},
		"status no key":      {"POST", "/status", key, map[string]any{}, http.StatusBadRequest, "error", "Bad Request"},
		"status unknown key": {"POST", "/status", key, map[string]any{"subscription_key": "nope"}, http.StatusCreated, "error", "NO_SUCH_TASK"},
		"download no uuid":   {"POST", "/download", key, map[string]any{}, http.StatusBadRequest, "error", "Bad Request"},
		"download unknown":   {"POST", "/download", key, map[string]any{"task_uuid": "nope"}, http.StatusCreated, "error", "NO_SUCH_TASK"},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.Do(tc.method, Prefix+tc.path, tc.key, tc.body)
			if status != tc.status || body[tc.field] != tc.value {
				t.Fatalf("status %d body %v", status, body)
			}
		})
	}
	status, _, body := f.Do("GET", Prefix+"/check_balance", key, nil)
	if status != http.StatusOK || testkit.Field(t, body, "balance").(float64) <= 0 {
		t.Fatalf("balance = %d %v", status, body)
	}
}

// #203: an unknown subscription_key or task_uuid is the documented application
// error (HTTP 201, {"error":"NO_SUCH_TASK"}), as observed on 2026-10-21
// (internal/providers/hyper3d/charge_evidence_test.go), not the 400
// malformed-body envelope the committed sample shows for a missing field.
func TestUnknownTaskReferencesAnswerNoSuchTask(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, tc := range map[string]struct {
		path string
		body map[string]any
	}{
		"status":   {"/status", map[string]any{"subscription_key": "fake-subscription-unknown"}},
		"download": {"/download", map[string]any{"task_uuid": "00000000-0000-4000-8000-000000000000"}},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, body := f.Do("POST", Prefix+tc.path, key, tc.body)
			if status != http.StatusCreated || body["error"] != "NO_SUCH_TASK" {
				t.Fatalf("%s = %d %v", tc.path, status, body)
			}
			if len(body) != 1 {
				t.Fatalf("the observed body carries only error: %v", body)
			}
		})
	}
	// A missing or non-string field stays a malformed body: HTTP 400 with the
	// validator envelope of the committed sample.
	status, _, body := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": json.Number("1")})
	if status != http.StatusBadRequest || body["error"] != "Bad Request" {
		t.Fatalf("non-string subscription_key = %d %v", status, body)
	}
	status, _, body = f.Do("POST", Prefix+"/download", key, map[string]any{"task_uuid": json.Number("1")})
	if status != http.StatusBadRequest || body["error"] != "Bad Request" {
		t.Fatalf("non-string task_uuid = %d %v", status, body)
	}
}

// IMAGE_LABEL_LENGTH_TOO_LONG is documented as more image_label entries than
// uploaded images (https://docs.hyper3d.ai/en/api-specification/errors) and was
// observed on 2026-11-19 (#376): two pictures with three repeated image_label
// parts were refused with HTTP 201 and the message "Input is invalid"
// (evidence_376_test.go holds the fake to the recorded exchange). Fewer labels
// than pictures were not observed; the fake accepts them.
func TestImageLabelCountIsValidated(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	fields := map[string]string{"tier": "Gen-2.5-Extreme-Low", "geometry_file_format": "glb", "material": "PBR"}
	for _, tc := range []struct {
		labels int
		code   string
	}{
		{0, ""}, {2, ""}, {3, "IMAGE_LABEL_LENGTH_TOO_LONG"},
	} {
		status, _, body := f.Do("POST", Prefix+"/rodin", key, labeledForm(fields, tc.labels, "images", "images"))
		if status != http.StatusCreated || (tc.code == "" && body["error"] != nil) || (tc.code != "" && body["error"] != tc.code) {
			t.Fatalf("%d labels for 2 images = %d %v", tc.labels, status, body)
		}
		if tc.code != "" && body["message"] != "Input is invalid" {
			t.Fatalf("%d labels for 2 images: message %v, the real service said \"Input is invalid\"", tc.labels, body["message"])
		}
	}
}

// The marks of image_label are the page's list: F, FL, FR, B, BL, BR, L, R, U, D
// and ? (docsRoot+"rodin-gen2-5", checked formDocs). A mark outside it is
// answered INVALID_REQUEST like the other constructed value rules; the real
// rejection was not observed (rodinLabelNote). A repeated mark is fine, and the
// observed count refusal still comes first.
func TestImageLabelValuesAreValidated(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	fields := map[string]string{"tier": "Gen-2.5-Extreme-Low", "geometry_file_format": "glb", "material": "PBR"}
	send := func(marks []string, files ...string) (int, map[string]any) {
		status, _, body := f.Do("POST", Prefix+"/rodin", key, markedForm(fields, marks, files...))
		return status, body
	}
	for _, mark := range []string{"F", "FL", "FR", "B", "BL", "BR", "L", "R", "U", "D", "?"} {
		if status, body := send([]string{mark}, "images"); status != http.StatusCreated || body["error"] != nil {
			t.Errorf("mark %q = %d %v, want accepted", mark, status, body)
		}
	}
	if status, body := send([]string{"?", "?", "F"}, "images", "images", "images"); status != http.StatusCreated || body["error"] != nil {
		t.Errorf("repeated marks = %d %v, want accepted", status, body)
	}
	for name, marks := range map[string][]string{
		"an unknown mark":         {"X"},
		"a lowercase mark":        {"f"},
		"a word":                  {"front"},
		"an empty mark":           {""},
		"a bad mark after a good": {"F", "label"},
	} {
		status, body := send(marks, "images", "images")
		if status != http.StatusCreated || body["error"] != "INVALID_REQUEST" || !strings.Contains(body["message"].(string), "image_label") {
			t.Errorf("%s = %d %v, want INVALID_REQUEST naming image_label", name, status, body)
		}
	}
	// The observed refusal is not replaced by the constructed one.
	if _, body := send([]string{"F", "label", "B"}, "images", "images"); body["error"] != "IMAGE_LABEL_LENGTH_TOO_LONG" {
		t.Errorf("three marks for two pictures, one of them bad = %v, want the observed count refusal", body)
	}
	// Fewer marks than pictures was not observed to be refused: accepted.
	if status, body := send([]string{"F"}, "images", "images", "images"); status != http.StatusCreated || body["error"] != nil {
		t.Errorf("one mark for three pictures = %d %v, want accepted", status, body)
	}
}

func TestFaultScenarios(t *testing.T) {
	short := fakeupstream.Config{TimeoutHold: 50 * time.Millisecond}
	post := func(f *testkit.Fixture, scenario string) (int, http.Header, map[string]any) {
		return f.Do("POST", Prefix+"/rodin", key, textForm(), "X-Fake-Scenario", scenario)
	}
	t.Run("insufficient balance", func(t *testing.T) {
		status, _, body := post(newFixture(t, short), "insufficient-balance")
		if status != http.StatusCreated || body["error"] != "API_INSUFFICIENT_FUNDS" {
			t.Fatalf("%d %v", status, body)
		}
	})
	t.Run("key quota maps to insufficient funds", func(t *testing.T) {
		// The documented table has no per-key quota code; a key's credit limit
		// is the insufficient-funds case.
		status, _, body := post(newFixture(t, short), "key-quota")
		if status != http.StatusCreated || body["error"] != "API_INSUFFICIENT_FUNDS" {
			t.Fatalf("%d %v", status, body)
		}
	})
	// The documented submission errors are application failures: HTTP 201 with
	// the documented code in the body (errors page checked 2026-11-08).
	for scenario, code := range map[string]string{
		"concurrency-limit":         "API_PARALLELISM_LIMIT_REACHED",
		"permission-denied":         "PERMISSION_DENIED",
		"no-active-subscription":    "API_NO_ACTIVE_SUBSCRIPTION",
		"plan-too-low":              "API_SUBSCRIPTION_PLAN_TOO_LOW",
		"object-not-found-on-image": "API_OBJECT_NOT_FOUND_ON_IMAGE",
		"content-violation":         "IMAGE_CONTENT_VIOLATION",
		"api-unknown":               "API_UNKNOWN",
	} {
		t.Run(scenario+" is an application error", func(t *testing.T) {
			f := newFixture(t, short)
			for _, route := range []string{"/rodin", "/bang", "/rodin_texture_only"} {
				status, _, body := f.Do("POST", Prefix+route, key, textForm(), "X-Fake-Scenario", scenario)
				if status != http.StatusCreated || body["error"] != code {
					t.Errorf("%s = %d %v", route, status, body)
				}
				if message, _ := body["message"].(string); message == "" {
					t.Errorf("%s: a documented application error carries a message: %v", route, body)
				}
			}
		})
	}
	t.Run("invalid key answers 401", func(t *testing.T) {
		f := newFixture(t, short)
		for _, request := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/check_balance", nil},
			{"POST", "/rodin", textForm()},
			{"POST", "/status", map[string]any{"subscription_key": "some-key"}},
			{"POST", "/download", map[string]any{"task_uuid": "some-id"}},
		} {
			status, _, body := f.Do(request.method, Prefix+request.path, "fake-invalid-key", request.body)
			if status != http.StatusUnauthorized || body["error"] != "UNAUTHORIZED" {
				t.Errorf("%s %s = %d %v", request.method, request.path, status, body)
			}
		}
	})
	t.Run("rate limit has Retry-After", func(t *testing.T) {
		status, header, _ := post(newFixture(t, short), "rate-limit")
		if status != http.StatusTooManyRequests || header.Get("Retry-After") != "1" {
			t.Fatalf("%d %v", status, header)
		}
	})
	t.Run("server error", func(t *testing.T) {
		if status, _, _ := post(newFixture(t, short), "server-error"); status != http.StatusInternalServerError {
			t.Fatalf("status %d", status)
		}
	})
	t.Run("timeout drops the connection", func(t *testing.T) {
		f := newFixture(t, short)
		start := time.Now()
		body := textForm()
		request, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/rodin", bytes.NewReader(body.Data))
		request.Header.Set("Content-Type", body.ContentType)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "timeout")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		if time.Since(start) < 40*time.Millisecond {
			t.Fatalf("dropped after %v, before the hold", time.Since(start))
		}
	})
	t.Run("lost response keeps the work", func(t *testing.T) {
		f := newFixture(t, short)
		body := textForm()
		request, _ := http.NewRequest("POST", f.Server.URL+Prefix+"/rodin", bytes.NewReader(body.Data))
		request.Header.Set("Content-Type", body.ContentType)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("X-Fake-Scenario", "lost-response")
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			t.Fatalf("got a response: %d", response.StatusCode)
		}
		// The work exists although the caller never saw its ids: the first task
		// took the first uuid and its jobs the next ones, so a later submission
		// counts on from them, and the lost task still answers its status.
		next := submit(t, f, "/rodin", textForm())
		lost := next.uuid[:len(next.uuid)-12] + "000000000001"
		status, _, polled := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": "fake-subscription-" + lost})
		if status != http.StatusCreated || len(testkit.Field(t, polled, "jobs").([]any)) != next.jobs {
			t.Fatalf("lost task %s = %d %v", lost, status, polled)
		}
	})
	// #563: the overloaded service's explicit "not now" is a 503 with
	// Retry-After and takes no work; under the fail-times bound the next
	// submission is accepted.
	t.Run("service unavailable is a 503 with Retry-After", func(t *testing.T) {
		f := newFixture(t, fakeupstream.Config{TimeoutHold: 50 * time.Millisecond, FailTimes: 1})
		status, header, body := post(f, "service-unavailable")
		if status != http.StatusServiceUnavailable || header.Get("Retry-After") != "1" || body["error"] != "Service Unavailable" {
			t.Fatalf("%d %v %v", status, header, body)
		}
		next := submit(t, f, "/rodin", textForm(), "X-Fake-Scenario", "service-unavailable")
		if !strings.HasSuffix(next.uuid, "000000000001") {
			t.Fatalf("the refused submission took work: the next task is %s", next.uuid)
		}
	})
	// #563: a gateway 502 after the create was accepted: the work exists (and
	// is billed) although the caller saw only the 502.
	t.Run("accepted then 502 keeps the work", func(t *testing.T) {
		f := newFixture(t, short)
		if status, _, body := post(f, "accepted-502"); status != http.StatusBadGateway || body["error"] != "Bad Gateway" {
			t.Fatalf("%d %v", status, body)
		}
		next := submit(t, f, "/rodin", textForm())
		accepted := next.uuid[:len(next.uuid)-12] + "000000000001"
		status, _, polled := f.Do("POST", Prefix+"/status", key, map[string]any{"subscription_key": "fake-subscription-" + accepted})
		if status != http.StatusCreated || len(testkit.Field(t, polled, "jobs").([]any)) != next.jobs {
			t.Fatalf("accepted task %s = %d %v", accepted, status, polled)
		}
	})
	t.Run("task failed", func(t *testing.T) {
		f := newFixture(t, short)
		s := submit(t, f, "/rodin", textForm(), "X-Fake-Scenario", "task-failed")
		flow := poll(t, f, s)
		if flow[len(flow)-1] != statusFailed {
			t.Fatalf("flow = %v", flow)
		}
		if list := download(t, f, s); len(list) != 0 {
			t.Fatalf("a failed task offers files: %v", list)
		}
	})
	t.Run("artifact URL expired, a later download hands out working URLs", func(t *testing.T) {
		f := newFixture(t, short)
		s := submit(t, f, "/rodin", textForm(), "X-Fake-Scenario", "artifact-expired")
		poll(t, f, s)
		first := download(t, f, s)[0].(map[string]any)["url"].(string)
		status, contentType, raw := f.Download(first)
		if status != http.StatusForbidden || !strings.Contains(contentType, "json") || !strings.Contains(string(raw), `"Message":"Request has expired"`) {
			t.Fatalf("expired URL = %d %s %s", status, contentType, raw)
		}
		second := download(t, f, s)[0].(map[string]any)["url"].(string)
		status, _, data := f.Download(second)
		if status != http.StatusOK || testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary") != nil {
			t.Fatalf("refetched URL = %d", status)
		}
	})
	t.Run("artifact 404 and corrupt", func(t *testing.T) {
		for scenario, ok := range map[string]bool{"artifact-404": false, "artifact-corrupt": true} {
			f := newFixture(t, short)
			s := submit(t, f, "/rodin", textForm(), "X-Fake-Scenario", scenario)
			poll(t, f, s)
			url := download(t, f, s)[0].(map[string]any)["url"].(string)
			status, _, data := f.Download(url)
			switch {
			case !ok && status != http.StatusNotFound:
				t.Fatalf("%s = %d", scenario, status)
			case ok && (status != http.StatusOK || testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary") == nil):
				t.Fatalf("%s: %d, delivery check passed", scenario, status)
			}
		}
	})
}

// The fake keeps the structure of the real samples for every route.
func TestDriftFakeResponsesMatchTheRealSamples(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	shapeOf := func(raw []byte) fakeupstream.Shape {
		shape, err := fakeupstream.ShapeOf(raw)
		if err != nil {
			t.Fatal(err)
		}
		return shape
	}
	for _, file := range sampleFiles {
		samples, err := fakeupstream.LoadSamples(sampleFS, file)
		if err != nil {
			t.Fatal(err)
		}
		route := ""
		for _, exchange := range samples.Exchanges {
			body, _ := fakeupstream.JSONBody(exchange)
			if body != nil && body["uuid"] != nil && body["jobs"] != nil {
				route = strings.TrimPrefix(exchange.Request.Path, samplePrefix)
				break
			}
		}
		fields := map[string]string{"prompt": "p", "asset_id": "", "material": "PBR"}
		var gen submitted
		files := []string{}
		switch route {
		case "/rodin":
			files = nil
		case "/bang":
			gen = submit(t, f, "/rodin", textForm())
			poll(t, f, gen)
			fields["asset_id"] = gen.uuid
			// The page's asset_id mode takes no prompt (#537).
			delete(fields, "prompt")
		case "/rodin_texture_only":
			files = []string{"model", "image"}
		}
		for _, exchange := range samples.Exchanges {
			body, _ := fakeupstream.JSONBody(exchange)
			apiPath := strings.TrimPrefix(exchange.Request.Path, samplePrefix)
			var raw []byte
			var status int
			switch {
			case apiPath == "/check_balance" && exchange.Response.Status == 200:
				status, _, raw = f.Raw("GET", Prefix+apiPath, key, nil)
			case apiPath == "/check_balance":
				status, _, raw = f.Raw("GET", Prefix+apiPath, "", nil)
			case exchange.Response.Status == 401:
				status, _, raw = f.Raw("POST", Prefix+route, "", textForm())
			case apiPath == route && body["error"] != nil:
				status, _, raw = f.Raw("POST", Prefix+route, key, form(map[string]string{"tier": "x"}))
			case apiPath == "/status" && exchange.Response.Status == 400, apiPath == "/download" && exchange.Response.Status == 400:
				status, _, raw = f.Raw("POST", Prefix+apiPath, key, map[string]any{})
			default:
				continue // submit, status and download of a task are compared below
			}
			if status != exchange.Response.Status {
				t.Errorf("%s %s: status %d, sample %d", file, apiPath, status, exchange.Response.Status)
			}
			if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(raw)); !diff.Empty() {
				t.Errorf("%s %s drifted from the real sample: %s", file, apiPath, diff)
			}
		}
		// A real task: submit, every sampled status, then the download list.
		s := submit(t, f, route, form(fields, files...))
		_, _, submitRaw := f.Raw("POST", Prefix+route, key, form(fields, files...))
		compared := 0
		for _, exchange := range samples.Exchanges {
			apiPath := strings.TrimPrefix(exchange.Request.Path, samplePrefix)
			body, _ := fakeupstream.JSONBody(exchange)
			if apiPath == route && exchange.Response.Status == 201 && body["jobs"] != nil {
				if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(submitRaw)); !diff.Empty() {
					t.Errorf("%s submit drifted: %s", route, diff)
				}
				compared++
			}
		}
		for i := 0; i < 60; i++ {
			_, _, polled := f.Raw("POST", Prefix+"/status", key, map[string]any{"subscription_key": s.key})
			var decoded map[string]any
			_ = json.Unmarshal(polled, &decoded)
			if _, ended := statusOf(t, decoded); ended {
				for _, exchange := range samples.Exchanges {
					if strings.HasSuffix(exchange.Request.Path, "/status") && exchange.Response.Status == 201 {
						if hasFailedJob(mustBody(exchange)) {
							continue // the Failed poll is compared in TestFailedPollMatchesTheRealSample
						}
						if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(polled)); !diff.Empty() && allDone(exchange) {
							t.Errorf("%s done poll drifted: %s", route, diff)
						}
					}
				}
				break
			}
		}
		_, _, listed := f.Raw("POST", Prefix+"/download", key, map[string]any{"task_uuid": s.uuid})
		for _, exchange := range samples.Exchanges {
			if strings.HasSuffix(exchange.Request.Path, "/download") && exchange.Response.Status == 201 {
				if list, _ := mustBody(exchange)["list"].([]any); len(list) == 0 {
					continue // an empty list (a failed task) has no element structure to compare
				}
				if diff := fakeupstream.DiffShape(shapeOf(exchange.Response.Body.JSON), shapeOf(listed)); !diff.Empty() {
					t.Errorf("%s download drifted: %s", route, diff)
				}
			}
		}
		if compared == 0 {
			t.Errorf("%s has no submit sample", file)
		}
	}
}

func mustBody(exchange wirefixture.Exchange) map[string]any {
	body, err := fakeupstream.JSONBody(exchange)
	if err != nil {
		panic(err)
	}
	return body
}

// The Failed poll (a real sample, captured twice) keeps its structure: every job
// Failed plus the top-level message.
func TestFailedPollMatchesTheRealSample(t *testing.T) {
	samples, err := fakeupstream.LoadSamples(sampleFS, sampleFiles...)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, fakeupstream.Config{})
	s := submit(t, f, "/rodin", textForm(), "X-Fake-Scenario", "task-failed")
	var polled []byte
	for i := 0; i < 60; i++ {
		_, _, polled = f.Raw("POST", Prefix+"/status", key, map[string]any{"subscription_key": s.key})
		var decoded map[string]any
		_ = json.Unmarshal(polled, &decoded)
		if state, ended := statusOf(t, decoded); ended && state == statusFailed {
			break
		}
	}
	compared := false
	for _, exchange := range samples.Exchanges {
		if strings.HasSuffix(exchange.Request.Path, "/status") && hasFailedJob(mustBody(exchange)) {
			testkit.AssertSameShape(t, "Failed poll", exchange.Response.Body.JSON, polled)
			compared = true
		}
	}
	if !compared {
		t.Fatal("no Failed status sample to compare")
	}
}

// allDone reports whether a status sample has every job Done (the final poll).
func allDone(exchange wirefixture.Exchange) bool {
	body, _ := fakeupstream.JSONBody(exchange)
	jobs, _ := body["jobs"].([]any)
	for _, job := range jobs {
		if job.(map[string]any)["status"] != statusDone {
			return false
		}
	}
	return len(jobs) > 0
}

func TestEndpointTableIsCompleteAndHonest(t *testing.T) {
	samples := map[string]bool{}
	for _, file := range sampleFiles {
		loaded, err := fakeupstream.LoadSamples(sampleFS, file)
		if err != nil {
			t.Fatal(err)
		}
		for _, exchange := range loaded.Exchanges {
			samples[fakeupstream.SampleKey(exchange.Request.Method, exchange.Request.Path)] = true
		}
	}
	f := newFixture(t, fakeupstream.Config{})
	for _, endpoint := range endpoints {
		switch endpoint.Evidence {
		case fakeupstream.EvidenceReal:
			if !samples[endpoint.Sample] {
				t.Errorf("%s names sample %q, which is not committed", endpoint.Key(), endpoint.Sample)
			}
		case fakeupstream.EvidenceConstructed:
			if !strings.HasPrefix(endpoint.DocURL, "https://") || len(endpoint.CheckedAt) != len("2026-10-29") {
				t.Errorf("%s: constructed endpoints need a documentation URL and a check date", endpoint.Key())
			}
		default:
			t.Errorf("%s has no evidence class", endpoint.Key())
		}
		if strings.Contains(endpoint.Path, "{") {
			continue
		}
		if status, _, _ := f.Do(endpoint.Method, Prefix+endpoint.Path, "", nil); status != http.StatusUnauthorized {
			t.Errorf("%s answers %d without a key, want 401", endpoint.Key(), status)
		}
	}
}

// #460 C32 / C30: the fake validates the documented Bang and Texture value
// domains, including material=Hybrid.
func TestBangAndTextureFormsValidateMaterialAndRanges(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	cases := []struct {
		name    string
		route   string
		fields  map[string]string
		files   []string
		wantErr string
	}{
		{"bang hybrid", "/bang", map[string]string{"material": "Hybrid", "geometry_file_format": "glb"}, []string{"model", "image"}, ""},
		{"bang none", "/bang", map[string]string{"material": "None", "geometry_file_format": "glb"}, []string{"model", "image"}, ""},
		{"bang unknown material", "/bang", map[string]string{"material": "Plaster"}, []string{"model", "image"}, "material"},
		{"bang strength range", "/bang", map[string]string{"material": "PBR", "strength": "13"}, []string{"model", "image"}, "strength"},
		{"texture hybrid", "/rodin_texture_only", map[string]string{"material": "Hybrid", "texture_mode": "high", "resolution": "High"}, []string{"model", "image"}, ""},
		{"texture unknown material", "/rodin_texture_only", map[string]string{"material": "None"}, []string{"model", "image"}, "material"},
		{"texture unknown resolution", "/rodin_texture_only", map[string]string{"material": "PBR", "resolution": "Ultra"}, []string{"model", "image"}, "resolution"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			status, _, body := f.Do("POST", Prefix+test.route, key, form(test.fields, test.files...))
			if status != http.StatusCreated {
				t.Fatalf("status = %d %v", status, body)
			}
			reason, _ := body["error"].(string)
			message, _ := body["message"].(string)
			if test.wantErr == "" {
				if reason != "" {
					t.Fatalf("error = %q, want none", reason)
				}
				return
			}
			if reason != "INVALID_REQUEST" || !strings.Contains(message, test.wantErr) {
				t.Fatalf("error = %q message = %q, want INVALID_REQUEST mentioning %q", reason, message, test.wantErr)
			}
		})
	}
}

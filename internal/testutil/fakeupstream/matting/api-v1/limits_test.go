package mattingapiv1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// I23: one table holds every pipeline parameter's bound and says where it comes
// from. The two process resolutions were read from the service source on
// limitsChecked; the rest are the product Schema's declared bounds and stay
// constructed until the service's /openapi.json is captured (I22).
func TestPipelineLimitsCoverEveryPipelineFieldWithAProvenance(t *testing.T) {
	var declared map[string]any
	if err := json.Unmarshal([]byte(pipeline), &declared); err != nil {
		t.Fatal(err)
	}
	var fields []string
	for _, limit := range PipelineLimits() {
		fields = append(fields, limit.Field)
		want := SourceConstructed
		if limit.Field == "birefnet_process_res" || limit.Field == "sdmatte_process_res" {
			want = SourceServiceCode
		}
		if limit.Source != want {
			t.Errorf("%s: source %q, want %q", limit.Field, limit.Source, want)
		}
	}
	for name := range declared {
		if !slices.Contains(fields, name) {
			t.Errorf("pipeline field %s has no limit entry", name)
		}
	}
	if len(fields) != len(declared) || len(fields) != 9 {
		t.Fatalf("limits cover %v, the declared pipeline has %d fields", fields, len(declared))
	}
	if limits := UploadLimits(); limits.Source != SourceConstructed || limits.MaxFiles != 20 ||
		limits.MaxFileBytes != 100<<20 || !slices.Equal(limits.Extensions, []string{"png", "jpg", "jpeg", "webp", "bmp", "tiff"}) {
		t.Fatalf("upload limits = %+v", limits)
	}
}

func TestEveryNumericPipelineBoundIsEnforced(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, limit := range PipelineLimits() {
		if limit.Kind == KindBoolean {
			continue
		}
		step := 1.0
		if !limit.Integer {
			step = 0.01
		}
		for _, value := range []float64{limit.Min, limit.Max, (limit.Min + limit.Max) / 2} {
			form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":%v}`, limit.Field, value), "input_1.png")
			if status, _, body := f.Do("POST", Prefix+"/jobs", key, form); status != http.StatusOK {
				t.Errorf("%s=%v: %d %v", limit.Field, value, status, body)
			}
		}
		for _, test := range []struct {
			value float64
			kind  string
		}{{limit.Min - step, "greater_than_equal"}, {limit.Max + step, "less_than_equal"}} {
			form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":%v}`, limit.Field, test.value), "input_1.png")
			status, _, body := f.Do("POST", Prefix+"/jobs", key, form)
			if status != http.StatusUnprocessableEntity {
				t.Errorf("%s=%v: %d %v", limit.Field, test.value, status, body)
				continue
			}
			if got := testkit.Field(t, body, "detail", 0, "type"); got != test.kind {
				t.Errorf("%s=%v: type %v, want %s", limit.Field, test.value, got, test.kind)
			}
			if got := testkit.Field(t, body, "detail", 0, "loc", 2); got != limit.Field {
				t.Errorf("%s=%v: loc %v", limit.Field, test.value, got)
			}
		}
		// A value of the wrong kind is a type error, not a bound error.
		form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":"wide"}`, limit.Field), "input_1.png")
		status, _, body := f.Do("POST", Prefix+"/jobs", key, form)
		if status != http.StatusUnprocessableEntity {
			t.Errorf("%s as a string: %d %v", limit.Field, status, body)
		}
	}
}

func TestFractionsAreOnlyRefusedWhereTheFieldIsAnInteger(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, limit := range PipelineLimits() {
		if limit.Kind == KindBoolean {
			continue
		}
		form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":%v}`, limit.Field, limit.Min+0.5), "input_1.png")
		status, _, _ := f.Do("POST", Prefix+"/jobs", key, form)
		if want := map[bool]int{true: http.StatusUnprocessableEntity, false: http.StatusOK}[limit.Integer]; status != want {
			t.Errorf("%s with a fraction: %d, want %d", limit.Field, status, want)
		}
	}
}

func TestBooleanPipelineFieldsAreTypeChecked(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, limit := range PipelineLimits() {
		if limit.Kind != KindBoolean {
			continue
		}
		for _, value := range []string{"true", "false", "1", "0", `"true"`} {
			form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":%s}`, limit.Field, value), "input_1.png")
			if status, _, body := f.Do("POST", Prefix+"/jobs", key, form); status != http.StatusOK {
				t.Errorf("%s=%s: %d %v", limit.Field, value, status, body)
			}
		}
		for value, kind := range map[string]string{`"maybe"`: "bool_parsing", `7`: "bool_parsing", `[1]`: "bool_type", `null`: "bool_type"} {
			form := jobFormWithPipeline(t, fmt.Sprintf(`{"%s":%s}`, limit.Field, value), "input_1.png")
			status, _, body := f.Do("POST", Prefix+"/jobs", key, form)
			if status != http.StatusUnprocessableEntity {
				t.Errorf("%s=%s: %d %v", limit.Field, value, status, body)
				continue
			}
			if got := testkit.Field(t, body, "detail", 0, "type"); got != kind {
				t.Errorf("%s=%s: type %v, want %s", limit.Field, value, got, kind)
			}
		}
	}
}

// Every field that is out of bounds is named, in declaration order.
func TestSeveralBadFieldsAreAllNamed(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	form := jobFormWithPipeline(t, `{"expand":1001,"blur_radius":-1,"sensitivity":1.5,"birefnet_mask_blur":51,"mask_refine":"x"}`, "input_1.png")
	status, _, body := f.Do("POST", Prefix+"/jobs", key, form)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("%d %v", status, body)
	}
	detail, _ := body["detail"].([]any)
	var named []string
	for index := range detail {
		named = append(named, testkit.Field(t, body, "detail", index, "loc", 2).(string))
	}
	want := []string{"birefnet_mask_blur", "expand", "blur_radius", "mask_refine", "sensitivity"}
	if !slices.Equal(named, want) {
		t.Fatalf("named %v, want %v", named, want)
	}
}

// I23: a batch of more than the file limit is refused whole (a pydantic
// too_long on the files field, constructed); files that the service would not
// take are REJECTED tasks like any other refusal of a file.
func TestUploadLimitsAreEnforced(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	names := func(n int) []string {
		list := make([]string, n)
		for i := range list {
			list[i] = fmt.Sprintf("input_%d.png", i+1)
		}
		return list
	}
	if status, created := submitFiles(f, t, names(UploadLimits().MaxFiles)); status != http.StatusOK || count(t, created, "total_count") != 20 {
		t.Fatalf("the file limit itself is refused: %d %v", status, created)
	}
	status, created := submitFiles(f, t, names(UploadLimits().MaxFiles+1))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("21 files: %d %v", status, created)
	}
	entry := testkit.Field(t, created, "detail", 0).(map[string]any)
	if entry["type"] != "too_long" || testkit.Field(t, entry, "loc", 0) != "body" || testkit.Field(t, entry, "loc", 1) != "files" ||
		testkit.Field(t, entry, "ctx", "max_length") != float64(20) || !strings.Contains(entry["msg"].(string), "at most 20") {
		t.Fatalf("too_long entry = %v", entry)
	}
	// Nothing was created by the refused request.
	if status, _, _ := f.Do("GET", Prefix+"/batches/batch_fake0002", key, nil); status != http.StatusNotFound {
		t.Fatalf("the refused batch exists: %d", status)
	}
}

func TestFilesOfAnUnsupportedExtensionAreRejectedTasks(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, name := range []string{"input_1.PNG", "input_2.Jpeg", "input_3.tiff", "input_4.bmp", "input_5.webp", "input_6.jpg"} {
		status, created := submitFiles(f, t, []string{name})
		if status != http.StatusOK || testkit.Field(t, created, "tasks", 0, "status") != "READY" {
			t.Errorf("%s is refused: %d %v", name, status, created)
		}
	}
	status, created := submitFiles(f, t, []string{"input_1.png", "input_2.gif", "notes.txt", "noextension", "input_5.tif"})
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, created)
	}
	if count(t, created, "accepted_count") != 1 || count(t, created, "rejected_count") != 4 {
		t.Fatalf("counts = %v", created)
	}
	for index, want := range []string{"READY", "REJECTED", "REJECTED", "REJECTED", "REJECTED"} {
		task := testkit.Field(t, created, "tasks", index).(map[string]any)
		if task["status"] != want {
			t.Errorf("task %d = %v, want %s", index, task, want)
		}
		if want == "REJECTED" && (task["invalid_reason"] == nil || task["downloadable"] != false) {
			t.Errorf("rejected task %d = %v", index, task)
		}
	}
}

func TestAFileOverTheSizeLimitIsARejectedTask(t *testing.T) {
	previous := uploadLimits
	t.Cleanup(func() { uploadLimits = previous })
	uploadLimits.MaxFileBytes = 1 << 10
	f := newFixture(t, fakeupstream.Config{})
	for _, test := range []struct {
		size int
		want string
	}{{1 << 10, "READY"}, {1<<10 + 1, "REJECTED"}} {
		status, _, created := f.Do("POST", Prefix+"/jobs", key, fileOfSizeForm(t, "input_1.png", test.size))
		if status != http.StatusOK {
			t.Fatalf("%d bytes: %d %v", test.size, status, created)
		}
		task := testkit.Field(t, created, "tasks", 0).(map[string]any)
		if task["status"] != test.want || (test.want == "REJECTED" && task["invalid_reason"] == nil) {
			t.Fatalf("a file of %d bytes = %v, want %s", test.size, task, test.want)
		}
	}
}

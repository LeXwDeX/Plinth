package modeldelivery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// #330 second follow-ups (dev-test note 99645).

// Observation 3: a format whose conversion failed was reported as
// not_converted, and the README told the reader to use 补齐格式 although the
// panel offered no such button. A failed conversion now has its own reason,
// the same in the formats API, the README and (as the retry hint) the panel.
func TestFailedConversionIsNotReportedAsNeverConverted330(t *testing.T) {
	f := failedConvert330(t)
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	fbx := formatByID(t, coverage, "fbx")
	if fbx.Status != StatusObtainable || fbx.Reason != reasonConversionFailed || fbx.ReasonText != f.service.schema.Reasons.ConversionFailed {
		t.Fatalf("fbx = %#v", fbx)
	}
	if strings.TrimSpace(f.service.schema.Reasons.ConversionFailed) == "" || strings.Contains(f.service.schema.Reasons.ConversionFailed, "补齐格式") {
		t.Fatalf("conversion_failed text = %q", f.service.schema.Reasons.ConversionFailed)
	}
	readme := readmeOf(t, f.fixture, "meshy-task")
	if !strings.Contains(readme, f.service.schema.Reasons.ConversionFailed) || strings.Contains(readme, f.service.schema.Reasons.NotConverted) {
		t.Fatalf("README must say the conversion failed, not that it never ran:\n%s", readme)
	}

	// A format nobody tried to convert keeps not_converted.
	fresh := newPaidCompletionFixture(t)
	fresh.meshyGlbOnly("meshy-task", "owner")
	coverage, err = fresh.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Reason != reasonNotConverted {
		t.Fatalf("never converted fbx = %#v", fbx)
	}
}

// failNotRetryable fails every plan of the fixture the way a source whose
// original provider task expired does: the step cannot be retried.
func failNotRetryable(f *fixture, reason string) []string {
	var ids []string
	for id, plan := range f.store.plans {
		plan.State, plan.Failure = processing.Failed, reason
		plan.Steps[0].State, plan.Steps[0].Failure, plan.Steps[0].Retryable = processing.Failed, reason, false
		f.store.plans[id] = plan
		ids = append(ids, id)
	}
	return ids
}

// Coordinator follow-up (#313 review): a completion plan that failed with
// plan_source_expired (or any failure that cannot be retried) held its plan
// key, so neither 重试 (409) nor 补齐格式 (skipped, 409) could get the format
// again. Such a plan now gives the formats back: they can be converted again
// under a new key, which binds today's source (the saved file), and the step
// says what happened last time.
func TestUnretryableFailedPlanLetsTheFormatsBeConvertedAgain330(t *testing.T) {
	f := newPaidCompletionFixture(t)
	f.meshyGlbOnly("meshy-task", "owner")
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task"); err != nil {
		t.Fatal(err)
	}
	failed := failNotRetryable(f, processing.ReasonSourceExpired)
	if len(failed) != 1 {
		t.Fatalf("plans = %v", failed)
	}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	step := convertStep330(t, coverage)
	if step.State != "" || step.PlanID != "" || step.PreviousFailure != processing.ReasonSourceExpired || !coverage.CanComplete {
		t.Fatalf("step = %#v can_complete = %v", step, coverage.CanComplete)
	}
	if fbx := formatByID(t, coverage, "fbx"); fbx.Status != StatusObtainable || fbx.Reason != reasonConversionFailed {
		t.Fatalf("fbx = %#v", fbx)
	}
	coverage, err = f.service.Complete(context.Background(), "owner", "meshy-task", "fbx")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.store.plans) != 2 {
		t.Fatalf("plans = %d, want the failed one and its successor", len(f.store.plans))
	}
	if step := convertStep330(t, coverage); step.PlanID == "" || step.PlanID == failed[0] {
		t.Fatalf("the successor plan must be what the step shows: %#v", step)
	}
	// Asking again never starts a second paid run.
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task", "fbx"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("second complete = %v", err)
	}
	if len(f.store.plans) != 2 {
		t.Fatalf("plans = %d after a repeated request", len(f.store.plans))
	}
}

// A failed plan whose step can still be retried keeps its key: the retry is
// the way to get the format, and no second plan is offered beside it.
func TestRetryableFailedPlanKeepsItsKey330(t *testing.T) {
	f := failedConvert330(t)
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if step := convertStep330(t, coverage); step.State != processing.Failed || step.PreviousFailure != "" {
		t.Fatalf("step = %#v", step)
	}
	if _, err := f.service.Complete(context.Background(), "owner", "meshy-task", "fbx"); !errors.Is(err, ErrNothingToComplete) {
		t.Fatalf("complete = %v", err)
	}
}

// A failed charged run whose free refetch is still offered is not given
// away to a new paid run, even when its own retry is not possible.
func TestRefetchableFailedPlanKeepsItsKey330(t *testing.T) {
	f := failedConvert330(t)
	failNotRetryable(f.fixture, processing.ReasonSourceExpired)
	f.eligibility["convert-failed"] = generation.RefetchEligibility{SourceTaskID: "convert-failed", Eligible: true, ChargedCredits: 1}
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	if step := convertStep330(t, coverage); step.State != processing.Failed || step.Refetch == nil || !step.Refetch.Eligible {
		t.Fatalf("step = %#v", step)
	}
}

// Coordinator follow-up (#313 review): when the only way to convert is the
// source's original provider task and that task has passed its retention,
// the format says so instead of the generic "no conversion service applies".
func TestExpiredSourceIsNamedAsTheReason330(t *testing.T) {
	f := newPaidCompletionFixture(t)
	task := f.meshyGlbOnly("meshy-task", "owner")
	task.Offering.ID = "meshy/meshy-7.1/text-to-3d"
	task.UpstreamJobGone = true
	// The saved GLB is above what the Convert source-asset route accepts.
	task.Assets[0].Size = 200 << 20
	f.tasks[task.ID] = task
	coverage, err := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if err != nil {
		t.Fatal(err)
	}
	fbx := formatByID(t, coverage, "fbx")
	if fbx.Status != StatusUnavailable || fbx.Reason != reasonSourceExpired || fbx.ReasonText != f.service.schema.Reasons.SourceExpired || fbx.ReasonText == "" {
		t.Fatalf("fbx = %#v", fbx)
	}
}

// A failed step that cannot be retried and is not given up (an ambiguous
// submission) says so, so the panel shows no retry that would answer 409.
func TestAmbiguousFailedStepBlocksItsRetry330(t *testing.T) {
	f := failedConvert330(t)
	coverage, _ := f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if step := convertStep330(t, coverage); !step.RetryBlocked {
		t.Fatalf("a failed step without a retryable step must block its retry: %#v", step)
	}
	for id, plan := range f.store.plans {
		plan.Steps[0].Retryable = true
		f.store.plans[id] = plan
	}
	coverage, _ = f.service.Coverage(context.Background(), generation.OwnTasks("owner"), "meshy-task")
	if step := convertStep330(t, coverage); step.RetryBlocked || step.State != processing.Failed {
		t.Fatalf("a retryable step must keep its retry: %#v", step)
	}
}

// Observation 5: the download media type comes from the format, whatever
// label the file was saved with; a packaged variant (a ZIP of glTF or OBJ)
// keeps the ZIP type, and a file of no format is not named.
func TestDownloadMediaTypeFollowsTheFormat330(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		asset generation.Asset
		want  string
		named bool
	}{
		{generation.Asset{Role: "model", Variant: "glb", MediaType: "model/gltf-binary"}, "model/gltf-binary", true},
		{generation.Asset{Role: "model", Variant: "obj", MediaType: "application/x-tgif"}, "model/obj", true},
		{generation.Asset{Role: "model", Variant: "obj", MediaType: "text/plain"}, "model/obj", true},
		{generation.Asset{Role: "model", Variant: "usdz", MediaType: "application/zip"}, "model/vnd.usdz+zip", true},
		{generation.Asset{Role: "model", Variant: "3mf", MediaType: "application/zip"}, "model/3mf", true},
		{generation.Asset{Role: "model", Variant: "stl", MediaType: "application/octet-stream"}, "model/stl", true},
		{generation.Asset{Role: "model", Variant: "fbx", MediaType: "application/octet-stream"}, "application/octet-stream", true},
		{generation.Asset{Role: "model", Variant: "gltf", MediaType: "model/gltf+json"}, "model/gltf+json", true},
		{generation.Asset{Role: "model", Variant: "gltf", MediaType: "application/zip"}, "application/zip", true},
		{generation.Asset{Role: "converted_model", Variant: "obj", MediaType: "application/zip"}, "application/zip", true},
		{generation.Asset{Role: "model", Variant: "mtl", MediaType: "text/plain"}, "", false},
		{generation.Asset{Role: "thumbnail", Variant: "primary", MediaType: "image/png"}, "", false},
	} {
		got, named := f.service.DownloadMediaType(test.asset)
		if got != test.want || named != test.named {
			t.Errorf("%+v => %q %v; want %q %v", test.asset, got, named, test.want, test.named)
		}
	}
}

// Observation 2: the version chain folds a format conversion into the version
// it converted. The Service tells the chain which Tasks are conversions, by
// the same Schema conversions (and fixed inputs) the Formats tab plans with.
func TestConversionRuleFollowsTheSchemaConversions330(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		offering generation.OfferingID
		input    string
		offered  bool
		want     bool
	}{
		{"meshy/processing/convert", `{"source_task":"t1"}`, true, true},
		{"local/generated-glb/to-gltf", `{"source_asset":"a1"}`, true, true},
		{"tripo/tripo-v3.1/format-conversion", `{"format":"FBX"}`, true, true},
		{"tripo/tripo-v3.1/format-conversion", `{"format":"PLY"}`, true, false},
		// The retired Remesh route is a conversion only with its fixed input.
		{"meshy/processing/remesh", `{"convert_format_only":true}`, true, true},
		{"meshy/processing/remesh", `{"target_polycount":3000}`, true, false},
		{"meshy/meshy-7.1/text-to-3d", `{"prompt":"cat"}`, false, false},
		{"meshy/processing/convert", `not json`, true, false},
	} {
		if got := f.service.ConversionOffering(test.offering); got != test.offered {
			t.Errorf("ConversionOffering(%s) = %v", test.offering, got)
		}
		if got := f.service.IsConversion(test.offering, json.RawMessage(test.input)); got != test.want {
			t.Errorf("IsConversion(%s, %s) = %v", test.offering, test.input, got)
		}
	}
	var none *Service
	if none.ConversionOffering("meshy/processing/convert") || none.IsConversion("meshy/processing/convert", json.RawMessage(`{}`)) {
		t.Fatal("a nil Service folds nothing")
	}
}

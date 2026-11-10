package fakeupstream_test

import (
	"bytes"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// The fake's PNG is the picture the Agent's self-check and the three-view
// consistency check read (#243/#245): one centered subject on a plain
// background, not a 1x1 placeholder.
func TestImageFilePNGIsASingleSubjectPicture(t *testing.T) {
	file, err := fakeupstream.ImageFile("png")
	if err != nil {
		t.Fatal(err)
	}
	if file.MediaType != "image/png" {
		t.Fatalf("media type = %q", file.MediaType)
	}
	report, err := imageinspect.Inspect(file.Data, imageinspect.Limits{MaxBytes: 1 << 20, MaxPixels: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if report.Width != 64 || report.Height != 64 {
		t.Fatalf("size = %dx%d, want 64x64", report.Width, report.Height)
	}
	if report.SubjectRatio < 0.05 || report.SubjectRatio > 0.95 || report.TouchesEdge || report.ComponentCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	checks := report.Review(imageinspect.Checklist{
		RequirePlainBackground: true, MaxBackgroundSpread: 32,
		MinSubjectRatio: 0.05, MaxSubjectRatio: 0.95, RequireEdgeClear: true, MaxComponents: 1,
	})
	if imageinspect.Failed(checks) {
		t.Fatalf("fake picture fails the 3d_input checklist: %+v", checks)
	}
	again, err := fakeupstream.ImageFile("png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(file.Data, again.Data) {
		t.Fatal("the fake picture is not deterministic")
	}
}

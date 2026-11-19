package hyper3dapiv2

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #376: the two real requests of 2026-11-19 that settled how a multipart Rodin
// request carries image_label. The user approved both (0.5 credit in all).
//
// rodin-label-overflow sent two pictures and three image_label parts (F, L and
// B, the name repeated). It was refused before any generation with HTTP 201
// {"error":"IMAGE_LABEL_LENGTH_TOO_LONG","message":"Input is invalid"} and was not
// charged (the balance was the same before and after). Only an array of three
// labels is longer than two pictures, so the service reads the repeated parts as
// one array. rodin-labeled sent three pictures (front, left, back) and the same
// three label parts ahead of the files (Gen-2.5-Extreme-Low, texture_mode
// extreme-low, quality medium, Raw, PBR, glb): accepted, consumed 0.5, six
// sub-jobs polled from Generating and Waiting to Done, a PBR GLB of 19,371,768
// bytes and a WebP preview of 2,034 bytes.
//
// The sanitized exchanges (testdata/evidence-376-*) are evidence for the fake, not
// one of the samples its templates come from: sampleFS embeds hyper3d-api-v2-*
// only (TestLabelEvidenceIsNotATemplateSample). The tests below hold the fake's
// answers to them. What the exchanges do not keep is stated from the runs
// themselves: the capture replaces amounts with placeholders (the profile's
// Projection covers consumed, balance, queue_length and submit_time, so the sample
// reads consumed 0, like every other Hyper3D sample) and never stores file bytes
// (so the fetched files' types come from the run's own fetch step).
//
//go:embed testdata/evidence-376-rodin-label-overflow.candidate.json testdata/evidence-376-rodin-labeled.candidate.json
var labelEvidenceFS embed.FS

const (
	overflowEvidenceFile = "testdata/evidence-376-rodin-label-overflow.candidate.json"
	labeledEvidenceFile  = "testdata/evidence-376-rodin-labeled.candidate.json"

	// labeledCharge is what the receipt of the labeled request said: the page's
	// 0.5 for the cheapest Gen-2.5 request, so the labels add nothing. The sample
	// holds the capture's placeholder, 0.
	labeledCharge = 0.5
)

// realFileTypes are the content types the run's own fetch of the two listed files
// saw, by extension.
var realFileTypes = map[string]string{".glb": "model/gltf-binary", ".webp": "image/webp"}

func loadLabelEvidence(t *testing.T, file string) []wirefixture.Exchange {
	t.Helper()
	samples, err := fakeupstream.LoadSamples(labelEvidenceFS, file)
	if err != nil {
		t.Fatal(err)
	}
	return samples.Exchanges
}

// labeledEvidence is the exchanges of the labeled run by role.
type labeledEvidence struct {
	submit   wirefixture.Exchange
	polls    []wirefixture.Exchange
	download wirefixture.Exchange
}

func loadLabeledEvidence(t *testing.T) labeledEvidence {
	t.Helper()
	var run labeledEvidence
	submits, downloads := 0, 0
	for _, exchange := range loadLabelEvidence(t, labeledEvidenceFile) {
		switch strings.TrimPrefix(exchange.Request.Path, samplePrefix) {
		case "/rodin":
			run.submit, submits = exchange, submits+1
		case "/status":
			run.polls = append(run.polls, exchange)
		case "/download":
			run.download, downloads = exchange, downloads+1
		default:
			t.Fatalf("the labeled run holds an exchange with %s", exchange.Request.Path)
		}
	}
	if submits != 1 || downloads != 1 || len(run.polls) == 0 {
		t.Fatalf("the labeled run holds %d submissions, %d polls and %d downloads", submits, len(run.polls), downloads)
	}
	return run
}

// recordedForm rebuilds a recorded multipart request: the parts in the recorded
// order under the recorded names, a text part with its value and a file part with
// the bytes of the recipe the capture substituted for the upload (a 1x1 PNG: all
// the fake reads of a picture is that it is a file).
func recordedForm(t *testing.T, exchange wirefixture.Exchange) testkit.Body {
	t.Helper()
	recorded := exchange.Request.Body
	if recorded.Encoding != wirefixture.PayloadMultipart || recorded.Multipart == nil {
		t.Fatalf("the recorded request is %q, not multipart", recorded.Encoding)
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for i, part := range recorded.Multipart.Parts {
		if part.Media == nil {
			if err := writer.WriteField(part.Name, part.Value); err != nil {
				t.Fatal(err)
			}
			continue
		}
		media, data, err := wirefixture.SyntheticMedia(part.Media.Recipe)
		if err != nil || !reflect.DeepEqual(media, *part.Media) {
			t.Fatalf("part %d: the recipe %q gives %+v (%v), the exchange recorded %+v", i, part.Media.Recipe, media, err, *part.Media)
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q; filename=\"picture-%d.png\"", part.Name, i))
		header.Set("Content-Type", media.MediaType)
		file, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

// labelParts reads a recorded request: its image_label values in order, the number
// of pictures and whether every label part comes before every picture.
func labelParts(t *testing.T, exchange wirefixture.Exchange) (labels []string, pictures int, labelsFirst bool) {
	t.Helper()
	recorded := exchange.Request.Body.Multipart
	if recorded == nil {
		t.Fatal("the recorded request has no multipart body")
	}
	labelsFirst = true
	for _, part := range recorded.Parts {
		switch {
		case part.Name == "image_label" && part.Media == nil:
			labels = append(labels, part.Value)
			if pictures > 0 {
				labelsFirst = false
			}
		case part.Name == "images" && part.Media != nil:
			pictures++
		}
	}
	return labels, pictures, labelsFirst
}

// textParts lists the text parts of a recorded request as name=value, in order.
func textParts(exchange wirefixture.Exchange) []string {
	var parts []string
	for _, part := range exchange.Request.Body.Multipart.Parts {
		if part.Media == nil {
			parts = append(parts, part.Name+"="+part.Value)
		}
	}
	return parts
}

// pollJobs reads a status response: each job's status and uuid, in order.
func pollJobs(t *testing.T, raw []byte) (statuses, uuids []string) {
	t.Helper()
	var body struct {
		Jobs []struct {
			Status string `json:"status"`
			UUID   string `json:"uuid"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("status response %s: %v", raw, err)
	}
	for _, job := range body.Jobs {
		statuses, uuids = append(statuses, job.Status), append(uuids, job.UUID)
	}
	return statuses, uuids
}

// jobsInOrder reports whether job statuses read like sub-jobs that run one after
// another: the finished ones first, then at most one running, then those waiting.
func jobsInOrder(statuses []string) bool {
	stage := 0 // 0: finished jobs, 1: the running job seen, 2: waiting jobs
	for _, status := range statuses {
		switch status {
		case statusDone:
			if stage != 0 {
				return false
			}
		case statusGenerating:
			if stage != 0 {
				return false
			}
			stage = 1
		case statusWaiting:
			stage = 2
		default:
			return false
		}
	}
	return true
}

// pollShape is the structure of a status response. queue_length is left out: the
// bang sample shows it on a Waiting job, and the labeled run never polled while
// its first job was Waiting, so its polls do not show it either way.
func pollShape(t *testing.T, raw []byte) fakeupstream.Shape {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("status response %s: %v", raw, err)
	}
	jobs, _ := body["jobs"].([]any)
	for _, job := range jobs {
		if entry, ok := job.(map[string]any); ok {
			delete(entry, "queue_length")
		}
	}
	trimmed, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := fakeupstream.ShapeOf(trimmed)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

// jobsAllDone reports whether every job of a status response is Done.
func jobsAllDone(statuses []string) bool {
	return len(statuses) > 0 && !slices.ContainsFunc(statuses, func(status string) bool { return status != statusDone })
}

// The evidence is what the tests below assume: the probe and the labeled request
// carry the same text parts, the same three labels ahead of the files, and differ in
// the number of pictures alone; the real answers are the ones the run recorded.
func TestLabelEvidenceRecordsTheTwoRuns(t *testing.T) {
	wantLabels := []string{"F", "L", "B"}

	overflow := loadLabelEvidence(t, overflowEvidenceFile)
	if len(overflow) != 1 {
		t.Fatalf("the overflow probe holds %d exchanges, want its one submission", len(overflow))
	}
	probe := overflow[0]
	if probe.Request.Method != http.MethodPost || probe.Request.Path != samplePrefix+"/rodin" {
		t.Fatalf("the probe is %s %s", probe.Request.Method, probe.Request.Path)
	}
	labels, pictures, labelsFirst := labelParts(t, probe)
	if !slices.Equal(labels, wantLabels) || pictures != 2 || !labelsFirst {
		t.Errorf("the probe sent labels %v and %d pictures (labels first: %v), want F, L, B and 2 pictures", labels, pictures, labelsFirst)
	}
	var refusal map[string]any
	if err := json.Unmarshal(probe.Response.Body.JSON, &refusal); err != nil || probe.Response.Status != http.StatusCreated ||
		!reflect.DeepEqual(refusal, map[string]any{"error": "IMAGE_LABEL_LENGTH_TOO_LONG", "message": "Input is invalid"}) {
		t.Errorf("the probe was answered %d %s (%v)", probe.Response.Status, probe.Response.Body.JSON, err)
	}

	run := loadLabeledEvidence(t)
	labels, pictures, labelsFirst = labelParts(t, run.submit)
	if !slices.Equal(labels, wantLabels) || pictures != 3 || !labelsFirst {
		t.Errorf("the labeled request sent labels %v and %d pictures (labels first: %v), want F, L, B and 3 pictures", labels, pictures, labelsFirst)
	}
	if !slices.Equal(textParts(probe), textParts(run.submit)) {
		t.Errorf("the probe's text parts %v differ from the labeled request's %v", textParts(probe), textParts(run.submit))
	}
	wantForm := []string{
		"tier=Gen-2.5-Extreme-Low", "geometry_file_format=glb", "material=PBR", "mesh_mode=Raw", "quality=medium",
		"texture_mode=extreme-low", "image_label=F", "image_label=L", "image_label=B",
	}
	if !slices.Equal(textParts(run.submit), wantForm) {
		t.Errorf("the labeled request's text parts are %v, want %v", textParts(run.submit), wantForm)
	}

	receipt, err := fakeupstream.JSONBody(run.submit)
	if err != nil || run.submit.Response.Status != http.StatusCreated || receipt["error"] != nil {
		t.Fatalf("the labeled request was answered %d %s (%v)", run.submit.Response.Status, run.submit.Response.Body.JSON, err)
	}
	uuids, _ := fakeupstream.Dig(receipt, "jobs", "uuids")
	jobs, _ := uuids.([]any)
	if len(jobs) != 6 {
		t.Fatalf("the receipt lists %d sub-jobs, want 6", len(jobs))
	}

	// The polls: one shape, six jobs in the receipt's order, running in order, ending
	// Done, and showing exactly the three statuses of the official API.
	seen := map[string]bool{}
	var wantUUIDs []string
	for _, id := range jobs {
		wantUUIDs = append(wantUUIDs, fmt.Sprint(id))
	}
	firstShape := pollShape(t, run.polls[0].Response.Body.JSON)
	for i, poll := range run.polls {
		statuses, ids := pollJobs(t, poll.Response.Body.JSON)
		if poll.Response.Status != http.StatusCreated || !slices.Equal(ids, wantUUIDs) || !jobsInOrder(statuses) {
			t.Errorf("poll %d = %d %v for %v, want six jobs running in order", i, poll.Response.Status, statuses, ids)
		}
		if diff := fakeupstream.DiffShape(firstShape, pollShape(t, poll.Response.Body.JSON)); !diff.Empty() {
			t.Errorf("poll %d differs in structure from poll 0: %s", i, diff)
		}
		for _, status := range statuses {
			seen[status] = true
		}
	}
	if last, _ := pollJobs(t, run.polls[len(run.polls)-1].Response.Body.JSON); !jobsAllDone(last) {
		t.Errorf("the last poll is %v, want every job Done", last)
	}
	if want := map[string]bool{statusWaiting: true, statusGenerating: true, statusDone: true}; !reflect.DeepEqual(seen, want) {
		t.Errorf("the polls show the statuses %v, want %v", seen, want)
	}

	list, err := fakeupstream.JSONBody(run.download)
	if err != nil || run.download.Response.Status != http.StatusCreated {
		t.Fatalf("the download was answered %d %s (%v)", run.download.Response.Status, run.download.Response.Body.JSON, err)
	}
	if names := listedNames(t, list); !slices.Equal(names, []string{"base_basic_pbr.glb", "preview.webp"}) {
		t.Errorf("the download list names %v, want the PBR GLB and the preview", names)
	}
}

// listedNames are the names of a download list, in order.
func listedNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	entries, _ := body["list"].([]any)
	var names []string
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		name, _ := item["name"].(string)
		if name == "" {
			t.Fatalf("a download list entry has no name: %v", entry)
		}
		names = append(names, name)
	}
	return names
}

// The fake answers the recorded probe the way the real service did: the same
// status and the same body, the error and its message ("Input is invalid", not the
// documented description).
func TestFakeRefusesMoreLabelsThanPicturesLikeTheRealProbe(t *testing.T) {
	probe := loadLabelEvidence(t, overflowEvidenceFile)[0]
	f := newFixture(t, fakeupstream.Config{})
	status, header, raw := f.Raw(http.MethodPost, Prefix+"/rodin", key, recordedForm(t, probe))
	if status != probe.Response.Status {
		t.Fatalf("the fake answered %d %s, the real service %d %s", status, raw, probe.Response.Status, probe.Response.Body.JSON)
	}
	if mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type")); err != nil || mediaType != probe.Response.ContentType {
		t.Errorf("the fake's content type is %q, the real service's %q", header.Get("Content-Type"), probe.Response.ContentType)
	}
	var recorded, answered map[string]any
	if err := json.Unmarshal(probe.Response.Body.JSON, &recorded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &answered); err != nil {
		t.Fatalf("the fake's answer %s: %v", raw, err)
	}
	if !reflect.DeepEqual(recorded, answered) {
		t.Errorf("the fake answered %s, the real service %s", raw, probe.Response.Body.JSON)
	}
}

// The fake accepts the recorded labeled request and answers it in the structure
// and with the values the run recorded, then walks the same statuses to Done and
// lists the same files with the types the run's fetch saw.
func TestFakeAnswersTheLabeledRequestLikeTheRealRun(t *testing.T) {
	run := loadLabeledEvidence(t)
	realReceipt, err := fakeupstream.JSONBody(run.submit)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, fakeupstream.Config{})

	// The receipt: the same fields, the real message and the real automatic prompt
	// (an image request without a prompt), the sample's six sub-jobs, and the 0.5
	// the receipt reported.
	status, header, raw := f.Raw(http.MethodPost, Prefix+"/rodin", key, recordedForm(t, run.submit))
	if status != run.submit.Response.Status {
		t.Fatalf("the fake answered %d %s, the real service %d", status, raw, run.submit.Response.Status)
	}
	if mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type")); err != nil || mediaType != run.submit.Response.ContentType {
		t.Errorf("the fake's content type is %q, the real service's %q", header.Get("Content-Type"), run.submit.Response.ContentType)
	}
	testkit.AssertSameShape(t, "the receipt", run.submit.Response.Body.JSON, raw)
	var receipt map[string]any
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatalf("the fake's receipt %s: %v", raw, err)
	}
	for _, field := range []string{"message", "prompt"} {
		if receipt[field] != realReceipt[field] {
			t.Errorf("%s = %v, the real receipt said %v", field, receipt[field], realReceipt[field])
		}
	}
	if got := testkit.Field(t, receipt, "consumed").(float64); got != labeledCharge {
		t.Errorf("consumed = %v, the real receipt said %v", got, labeledCharge)
	}
	realJobs, _ := fakeupstream.Dig(realReceipt, "jobs", "uuids")
	jobs := testkit.Field(t, receipt, "jobs", "uuids").([]any)
	if len(jobs) != len(realJobs.([]any)) {
		t.Fatalf("the receipt lists %d sub-jobs, the real one %d", len(jobs), len(realJobs.([]any)))
	}
	var jobIDs []string
	for _, id := range jobs {
		jobIDs = append(jobIDs, id.(string))
	}
	subscription := testkit.Field(t, receipt, "jobs", "subscription_key").(string)
	task := testkit.Field(t, receipt, "uuid").(string)

	// The polls: the real shape, the receipt's jobs in order, running one after
	// another, showing every status the run showed (and no other), ending Done.
	wantShape := pollShape(t, run.polls[0].Response.Body.JSON)
	realStatuses := map[string]bool{}
	for _, poll := range run.polls {
		statuses, _ := pollJobs(t, poll.Response.Body.JSON)
		for _, status := range statuses {
			realStatuses[status] = true
		}
	}
	seen := map[string]bool{}
	var statuses []string
	for range 80 {
		status, _, polled := f.Raw(http.MethodPost, Prefix+"/status", key, map[string]any{"subscription_key": subscription})
		if status != run.polls[0].Response.Status {
			t.Fatalf("a poll was answered %d %s, the real service %d", status, polled, run.polls[0].Response.Status)
		}
		var ids []string
		statuses, ids = pollJobs(t, polled)
		if !slices.Equal(ids, jobIDs) || !jobsInOrder(statuses) {
			t.Fatalf("a poll shows %v for %v, want the receipt's jobs %v running in order", statuses, ids, jobIDs)
		}
		if diff := fakeupstream.DiffShape(wantShape, pollShape(t, polled)); !diff.Empty() {
			t.Fatalf("a poll drifted from the real one: %s (%s)", diff, polled)
		}
		for _, state := range statuses {
			seen[state] = true
		}
		if jobsAllDone(statuses) {
			break
		}
	}
	if !jobsAllDone(statuses) {
		t.Fatalf("the jobs never ended: %v", statuses)
	}
	for state := range realStatuses {
		if !seen[state] {
			t.Errorf("the fake never showed %s, which the run did", state)
		}
	}
	for state := range seen {
		if !realStatuses[state] {
			t.Errorf("the fake showed %s, which the run never did", state)
		}
	}

	// The download list: the real shape and names, and each file is served with the
	// type the run's fetch saw (the GLB also passes Plinth's delivery check).
	status, _, listed := f.Raw(http.MethodPost, Prefix+"/download", key, map[string]any{"task_uuid": task})
	if status != run.download.Response.Status {
		t.Fatalf("the download was answered %d %s, the real service %d", status, listed, run.download.Response.Status)
	}
	testkit.AssertSameShape(t, "the download list", run.download.Response.Body.JSON, listed)
	var list map[string]any
	if err := json.Unmarshal(listed, &list); err != nil {
		t.Fatalf("the fake's download list %s: %v", listed, err)
	}
	realList, err := fakeupstream.JSONBody(run.download)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := listedNames(t, list), listedNames(t, realList); !slices.Equal(got, want) {
		t.Fatalf("the fake lists %v, the real service %v", got, want)
	}
	for _, entry := range list["list"].([]any) {
		item := entry.(map[string]any)
		name, url := item["name"].(string), item["url"].(string)
		wantType, ok := realFileTypes[path.Ext(name)]
		if !ok {
			t.Fatalf("%s is a file type the run did not list", name)
		}
		status, contentType, data := f.Download(url)
		if status != http.StatusOK || contentType != wantType || len(data) == 0 {
			t.Errorf("%s = %d %q (%d bytes), the run's fetch saw %q", name, status, contentType, len(data), wantType)
		}
		if path.Ext(name) == ".glb" {
			if err := testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
				t.Errorf("%s fails Plinth's delivery check: %v", name, err)
			}
		}
	}
}

// The evidence stays evidence: neither run's file is one of the templates the fake
// reads its answers from, and both load as captured exchanges.
func TestLabelEvidenceIsNotATemplateSample(t *testing.T) {
	for _, name := range sampleFiles {
		if strings.Contains(name, "evidence-") {
			t.Errorf("%s is a template sample, it is evidence", name)
		}
	}
	for _, name := range []string{overflowEvidenceFile, labeledEvidenceFile} {
		if _, err := fs.Stat(sampleFS, name); err == nil {
			t.Errorf("sampleFS embeds %s", name)
		}
		if len(loadLabelEvidence(t, name)) == 0 {
			t.Errorf("%s holds no exchange", name)
		}
	}
}

// The runs travel with the endpoint table (and so with the alignment report): the
// date, the encoding, the refusal and its body, the charge, what the fake does not
// check, and where the evidence is. The code is observed on /rodin only: the other
// two routes never answer it, so their notes do not list it.
func TestRodinEndpointNoteRecordsTheLabelRuns(t *testing.T) {
	notes := map[string]string{}
	for _, endpoint := range endpoints {
		notes[endpoint.Method+" "+endpoint.Path] = endpoint.Note
	}
	for _, want := range []string{
		"image_label (#376)", labelRun, "the name repeated", "IMAGE_LABEL_LENGTH_TOO_LONG", "Input is invalid", "was not charged",
		"consumed 0.5", "19,371,768 bytes", "no code is invented", "evidence-376-rodin-label-overflow", "evidence-376-rodin-labeled",
		"evidence_376_test.go",
	} {
		if !strings.Contains(notes["POST /rodin"], want) {
			t.Errorf("the /rodin note does not record %q", want)
		}
	}
	if strings.Contains(notes["POST /rodin"], "IMAGE_CONTENT_VIOLATION, IMAGE_LABEL_LENGTH_TOO_LONG") {
		t.Error("the /rodin note still lists IMAGE_LABEL_LENGTH_TOO_LONG among the codes built from the docs")
	}
	for _, route := range []string{"POST /bang", "POST /rodin_texture_only"} {
		if strings.Contains(notes[route], "IMAGE_LABEL_LENGTH_TOO_LONG") {
			t.Errorf("the %s note mentions IMAGE_LABEL_LENGTH_TOO_LONG, which that route never answers", route)
		}
	}
}

// The alignment script's request notes, which the next report regeneration prints,
// record the two runs.
func TestProfileNotesRecordTheLabelRuns(t *testing.T) {
	for _, note := range Profile().RequestNotes {
		if !strings.Contains(note, "#376") {
			continue
		}
		for _, want := range []string{
			labelRun, "IMAGE_LABEL_LENGTH_TOO_LONG", "Input is invalid", "未扣费", "0.5", "2 个付费步骤", "evidence-376-rodin-label-overflow",
			"evidence-376-rodin-labeled",
		} {
			if !strings.Contains(note, want) {
				t.Errorf("the #376 request note does not record %q: %s", want, note)
			}
		}
		return
	}
	t.Fatal("the profile's request notes do not mention #376")
}

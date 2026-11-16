package mattingapiv1

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// markedPicture is a real PNG of the given size whose own bytes ask for the
// given faults (#301); with no fault it is a plain picture.
func markedPicture(t *testing.T, name string, width, height int, faults ...string) uploadedPicture {
	t.Helper()
	data := pngOf(t, width, height)
	if len(faults) > 0 {
		marked, err := MarkPNG(data, faults...)
		if err != nil {
			t.Fatal(err)
		}
		data = marked
	}
	return uploadedPicture{name, data}
}

// #301: a deployed stack cannot add a header or choose a key for the request
// the fake receives, so the fake also reads the faults of a file from the
// file's own bytes. The batch below carries no X-Fake-File-Faults header and no
// scenario; every outcome comes from the pictures. The rules are those of the
// header: a rejected file is refused in the create response already, a file that
// fails looks like the others until the batch finishes, rejected wins, and
// neither has an artifact.
func TestPictureMarkersChooseWhatBecomesOfEachFile(t *testing.T) {
	for _, test := range []struct {
		name    string
		faults  [][]string // per file, what its own bytes ask for
		created []string   // the task statuses of the create response
		final   []string   // the task statuses of the finished batch
		batch   string
	}{
		{"a file that fails among files that succeed", [][]string{nil, {FaultFailed}, nil},
			[]string{"READY", "READY", "READY"}, []string{"SUCCEEDED", "FAILED", "SUCCEEDED"}, "PARTIAL_SUCCESS"},
		{"a rejected file and a file that fails among files that succeed", [][]string{nil, {FaultFailed}, nil, {FaultRejected}},
			[]string{"READY", "READY", "READY", "REJECTED"}, []string{"SUCCEEDED", "FAILED", "SUCCEEDED", "REJECTED"}, "PARTIAL_SUCCESS"},
		{"a file the GPU cannot hold", [][]string{{FaultGPUOutOfMemory}, nil},
			[]string{"READY", "READY"}, []string{"FAILED", "SUCCEEDED"}, "PARTIAL_SUCCESS"},
		{"every file fails", [][]string{{FaultFailed}, {FaultGPUOutOfMemory}},
			[]string{"READY", "READY"}, []string{"FAILED", "FAILED"}, "PARTIAL_SUCCESS"},
		{"a file that is rejected and would fail stays rejected", [][]string{{FaultFailed, FaultRejected}, nil},
			[]string{"REJECTED", "READY"}, []string{"REJECTED", "SUCCEEDED"}, "PARTIAL_SUCCESS"},
		{"a renamed file is not a fault of its result", [][]string{nil, {FaultRenamed}},
			[]string{"READY", "READY"}, []string{"SUCCEEDED", "SUCCEEDED"}, "SUCCESS"},
		{"the same marker on the first and the last file", [][]string{{FaultFailed}, nil, {FaultFailed}},
			[]string{"READY", "READY", "READY"}, []string{"FAILED", "SUCCEEDED", "FAILED"}, "PARTIAL_SUCCESS"},
		{"pictures without a marker succeed", [][]string{nil, nil},
			[]string{"READY", "READY"}, []string{"SUCCEEDED", "SUCCEEDED"}, "SUCCESS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			pictures := make([]uploadedPicture, len(test.faults))
			for index, faults := range test.faults {
				pictures[index] = markedPicture(t, fmt.Sprintf("input_%d.png", index+1), 16, 16, faults...)
			}
			status, _, created := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t, pictures...))
			if status != http.StatusOK {
				t.Fatalf("create: %d %v", status, created)
			}
			if got := taskStatuses(t, created); !slices.Equal(got, test.created) {
				t.Fatalf("created statuses = %v, want %v", got, test.created)
			}
			final := pollUntil(f, t, created["batch_id"].(string), test.batch)
			if got := taskStatuses(t, final); !slices.Equal(got, test.final) {
				t.Fatalf("final statuses = %v, want %v", got, test.final)
			}
			for index, want := range test.final {
				task := testkit.Field(t, final, "tasks", index).(map[string]any)
				wantStatus := http.StatusNotFound
				if want == "SUCCEEDED" {
					wantStatus = http.StatusOK
				}
				if status, _, _ := f.Raw("GET", Prefix+"/tasks/"+task["task_id"].(string)+"/download", key, nil); status != wantStatus {
					t.Fatalf("download of the %s task %d = %d, want %d", want, index+1, status, wantStatus)
				}
			}
		})
	}
}

// A renamed file is answered under a name of the service's own, and only that
// file, however the fault was asked for.
func TestPictureMarkerRenamesOnlyItsOwnFile(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, _, created := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t,
		markedPicture(t, "input_1.png", 16, 16), markedPicture(t, "input_2.png", 16, 16, FaultRenamed), markedPicture(t, "input_3.png", 16, 16)))
	for index, want := range []string{"input_1.png", renamedSource, "input_3.png"} {
		if got := testkit.Field(t, created, "tasks", index, "source_name"); got != want {
			t.Fatalf("task %d source_name = %v, want %q", index+1, got, want)
		}
	}
}

// The batch of the 2026-11-13 real run (#299): a 2048x2048 picture the GPU
// could not hold and a 16x16 one that was matted. Here the picture chooses its
// own fate, and the failed task says what the service said, with the size read
// from that picture.
func TestPictureMarkerReplaysTheRealOutOfMemoryRun(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, created := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t,
		markedPicture(t, "input_1.png", 2048, 2048, FaultGPUOutOfMemory), markedPicture(t, "input_2.png", 16, 16)))
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, created)
	}
	final := pollUntil(f, t, created["batch_id"].(string), "PARTIAL_SUCCESS")
	big := testkit.Field(t, final, "tasks", 0).(map[string]any)
	if big["status"] != "FAILED" || big["downloadable"] != false || big["error_message"] != realOutOfMemory || big["invalid_reason"] != nil {
		t.Fatalf("the 2048x2048 task = %v", big)
	}
	small := testkit.Field(t, final, "tasks", 1).(map[string]any)
	if small["status"] != "SUCCEEDED" || small["downloadable"] != true || small["error_message"] != nil {
		t.Fatalf("the 16x16 task = %v", small)
	}
}

// What a picture asks for adds to what the request header asks for, position by
// position; a file that is refused by either stays refused.
func TestPictureMarkersAddToTheFileFaultsHeader(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, _, created := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t,
		markedPicture(t, "input_1.png", 16, 16),
		markedPicture(t, "input_2.png", 16, 16, FaultGPUOutOfMemory),
		markedPicture(t, "input_3.png", 16, 16, FaultRejected),
		markedPicture(t, "input_4.png", 16, 16),
		markedPicture(t, "input_5.png", 16, 16, FaultFailed)),
		FileFaultsHeader, "1:failed,3:failed")
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, created)
	}
	if got, want := taskStatuses(t, created), []string{"READY", "READY", "REJECTED", "READY", "READY"}; !slices.Equal(got, want) {
		t.Fatalf("created statuses = %v, want %v", got, want)
	}
	final := pollUntil(f, t, created["batch_id"].(string), "PARTIAL_SUCCESS")
	if got, want := taskStatuses(t, final), []string{"FAILED", "FAILED", "REJECTED", "SUCCEEDED", "FAILED"}; !slices.Equal(got, want) {
		t.Fatalf("final statuses = %v, want %v", got, want)
	}
}

// The scenario of the request still applies on top of the pictures: a picture
// that asks for nothing is not exempt from task-failed.
func TestPictureMarkersCombineWithABatchScenario(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, _, created := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t,
		markedPicture(t, "input_1.png", 16, 16), markedPicture(t, "input_2.png", 2048, 2048, FaultGPUOutOfMemory)),
		"X-Fake-Scenario", "task-failed")
	final := pollUntil(f, t, created["batch_id"].(string), "FAILED")
	if got := testkit.Field(t, final, "tasks", 0, "error_message"); got != genericFailure {
		t.Fatalf("task 1 error_message = %v, want the generic failure", got)
	}
	if got := testkit.Field(t, final, "tasks", 1, "error_message"); got != realOutOfMemory {
		t.Fatalf("task 2 error_message = %v, want the service's out-of-memory text", got)
	}
}

// Nothing a picture says outlives its batch: the next batch of plain pictures
// is untouched by an earlier one that carried markers, and a poll that carries a
// marker-looking header changes nothing.
func TestPictureMarkersDoNotLeakIntoOtherBatches(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, _, marked := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t,
		markedPicture(t, "input_1.png", 16, 16, FaultFailed), markedPicture(t, "input_2.png", 16, 16, FaultRejected)))
	_, _, plain := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t,
		markedPicture(t, "input_1.png", 16, 16), markedPicture(t, "input_2.png", 16, 16)))
	plainFinal := pollUntil(f, t, plain["batch_id"].(string), "SUCCESS")
	if got, want := taskStatuses(t, plainFinal), []string{"SUCCEEDED", "SUCCEEDED"}; !slices.Equal(got, want) {
		t.Fatalf("the plain batch = %v, want %v", got, want)
	}
	markedFinal := pollUntil(f, t, marked["batch_id"].(string), "PARTIAL_SUCCESS")
	if got, want := taskStatuses(t, markedFinal), []string{"FAILED", "REJECTED"}; !slices.Equal(got, want) {
		t.Fatalf("the marked batch = %v, want %v", got, want)
	}
}

// decodeJSON sends one request with plain net/http, so a goroutine can report
// through t.Errorf: the fixture's helpers stop the test with t.Fatalf, which only
// the test's own goroutine may do.
func decodeJSON(method, target string, body testkit.Body) (map[string]any, error) {
	var reader io.Reader
	if body.Data != nil {
		reader = bytes.NewReader(body.Data)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if body.ContentType != "" {
		request.Header.Set("Content-Type", body.ContentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: %d %s", method, target, response.StatusCode, raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// Batches that are in flight together each keep their own pictures' faults: the
// faults are read from the creating request alone, so no request can consume or
// borrow another's. Run with -race: the fake's batch table is shared state.
func TestPictureMarkersOfConcurrentBatchesDoNotTouchEachOther(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	type wanted struct {
		faults []string
		final  []string
		batch  string
	}
	kinds := []wanted{
		{nil, []string{"SUCCEEDED", "SUCCEEDED"}, "SUCCESS"},
		{[]string{FaultFailed}, []string{"SUCCEEDED", "FAILED"}, "PARTIAL_SUCCESS"},
		{[]string{FaultGPUOutOfMemory}, []string{"SUCCEEDED", "FAILED"}, "PARTIAL_SUCCESS"},
		{[]string{FaultRejected}, []string{"SUCCEEDED", "REJECTED"}, "PARTIAL_SUCCESS"},
	}
	const batches = 24
	bodies := make([]testkit.Body, batches)
	for index := range bodies {
		kind := kinds[index%len(kinds)]
		bodies[index] = jobFormWithPictures(t, markedPicture(t, "input_1.png", 16, 16), markedPicture(t, "input_2.png", 16, 16, kind.faults...))
	}
	var group sync.WaitGroup
	for index := range batches {
		kind := kinds[index%len(kinds)]
		group.Add(1)
		go func() {
			defer group.Done()
			created, err := decodeJSON(http.MethodPost, f.Server.URL+Prefix+"/jobs", bodies[index])
			if err != nil {
				t.Errorf("batch %d: %v", index, err)
				return
			}
			id, _ := created["batch_id"].(string)
			var final map[string]any
			for range 8 {
				if final, err = decodeJSON(http.MethodGet, f.Server.URL+Prefix+"/batches/"+id, testkit.Body{}); err != nil {
					t.Errorf("batch %d: %v", index, err)
					return
				}
				if final["batch_status"] == kind.batch {
					break
				}
			}
			var statuses []string
			tasks, _ := final["tasks"].([]any)
			for _, task := range tasks {
				entry, _ := task.(map[string]any)
				status, _ := entry["status"].(string)
				statuses = append(statuses, status)
			}
			if final["batch_status"] != kind.batch || !slices.Equal(statuses, kind.final) {
				t.Errorf("batch %d (faults %v) = %v %v, want %s %v", index, kind.faults, final["batch_status"], statuses, kind.batch, kind.final)
			}
		}()
	}
	group.Wait()
}

// The fault words are the header's; a word the fake does not know, a marker that
// is never closed and one whose closing mark is out of reach ask for nothing.
func TestPictureMarkersThatAskForNothingAreIgnored(t *testing.T) {
	far := strings.Repeat("x", markerReach+1)
	for name, text := range map[string]string{
		"an unknown word":                     FileFaultMarker + "exploded;",
		"an empty list":                       FileFaultMarker + ";",
		"a marker that is never closed":       FileFaultMarker + "failed",
		"a closing mark that is out of reach": FileFaultMarker + "failed," + far + ";",
		"a different spelling":                "fake-file-fault:failed;",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, fakeupstream.Config{})
			picture := uploadedPicture{"input_1.png", []byte(text)}
			_, _, created := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t, picture))
			final := pollUntil(f, t, created["batch_id"].(string), "SUCCESS")
			if got := taskStatuses(t, final); !slices.Equal(got, []string{"SUCCEEDED"}) {
				t.Fatalf("final statuses = %v", got)
			}
		})
	}
}

// The fake looks at the head of a file only (the part it reads for the picture's
// size), so a marker deep in the file is not seen.
func TestPictureMarkerBeyondTheHeadIsNotSeen(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	data := append(bytes.Repeat([]byte{0}, headerRead), []byte(FaultMark(FaultFailed))...)
	_, _, created := f.Do("POST", Prefix+"/jobs", key, jobFormWithPictures(t, uploadedPicture{"input_1.png", data}))
	final := pollUntil(f, t, created["batch_id"].(string), "SUCCESS")
	if got := taskStatuses(t, final); !slices.Equal(got, []string{"SUCCEEDED"}) {
		t.Fatalf("final statuses = %v", got)
	}
}

// The marker grammar: "fake-file-faults:" then the fault words separated by
// commas, closed by ";". Spaces around a word are tolerated, several markers add
// up, and whatever follows the closing mark (the CRC of a PNG text chunk, the next
// segment of a JPEG) is not part of the list.
func TestMarkedFaultsReadsTheListsAFileCarries(t *testing.T) {
	for _, test := range []struct {
		name string
		head string
		want []string
	}{
		{"nothing", "", nil},
		{"no marker", "\x89PNG\r\n\x1a\nIHDR comment", nil},
		{"one word", "xx" + FileFaultMarker + "failed;yy", []string{"failed"}},
		{"several words", FileFaultMarker + "failed,gpu_oom,renamed;", []string{"failed", "gpu_oom", "renamed"}},
		{"spaces around the words", FileFaultMarker + " failed , gpu_oom ;", []string{"failed", "gpu_oom"}},
		{"bytes after the closing mark are not words", FileFaultMarker + "failed;\xde\xad\xbe\xefkeep", []string{"failed"}},
		{"two markers add up", FileFaultMarker + "failed;" + "\x00" + FileFaultMarker + "renamed;", []string{"failed", "renamed"}},
		{"a closed marker after an unclosed one", FileFaultMarker + FileFaultMarker + "rejected;", []string{"rejected"}},
		{"an unclosed marker", FileFaultMarker + "failed", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := markedFaults([]byte(test.head)); !slices.Equal(got, test.want) {
				t.Fatalf("markedFaults(%q) = %q, want %q", test.head, got, test.want)
			}
		})
	}
}

// The text of the contract, written out so a change to either side shows: the
// scripts that build marker pictures without Go (scripts/smoke) write the same
// bytes.
func TestFaultMarkIsThePlainText(t *testing.T) {
	if got := FaultMark(FaultFailed, FaultGPUOutOfMemory); got != "fake-file-faults:failed,gpu_oom;" {
		t.Fatalf("FaultMark = %q", got)
	}
	for word, want := range map[string]string{FaultRejected: "rejected", FaultFailed: "failed", FaultGPUOutOfMemory: "gpu_oom", FaultRenamed: "renamed"} {
		if word != want {
			t.Fatalf("fault word %q, want %q", word, want)
		}
	}
	// Every listed word is one the fake understands, and the list is complete.
	words := FileFaultWords()
	if want := []string{"rejected", "failed", "gpu_oom", "renamed"}; !slices.Equal(words, want) {
		t.Fatalf("FileFaultWords = %q, want %q", words, want)
	}
	for _, word := range words {
		var fault fileFault
		if !fault.add(word) || fault == (fileFault{}) {
			t.Fatalf("the fake does not act on its own fault word %q", word)
		}
	}
	var unknown fileFault
	if unknown.add("exploded") || unknown != (fileFault{}) {
		t.Fatal("an unknown word was taken for a fault")
	}
}

// MarkPNG leaves a PNG that every decoder still reads, the same size, with one
// text chunk right after the header carrying the marker.
func TestMarkPNGKeepsThePictureAndCarriesTheMarker(t *testing.T) {
	original := pngOf(t, 640, 480)
	marked, err := MarkPNG(original, FaultFailed, FaultRenamed)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(marked))
	if err != nil {
		t.Fatalf("the marked picture does not decode: %v", err)
	}
	if bounds := decoded.Bounds(); bounds != image.Rect(0, 0, 640, 480) {
		t.Fatalf("bounds = %v", bounds)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(marked[:min(len(marked), 128)]))
	if err != nil || config.Width != 640 || config.Height != 480 {
		t.Fatalf("the size is not readable from the head: %+v %v", config, err)
	}
	var kinds []string
	reader := bytes.NewReader(marked[8:])
	for reader.Len() > 0 {
		var length uint32
		var kind [4]byte
		if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(reader, kind[:]); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			t.Fatal(err)
		}
		var crc uint32
		if err := binary.Read(reader, binary.BigEndian, &crc); err != nil {
			t.Fatal(err)
		}
		if crc != crc32.ChecksumIEEE(append(kind[:], data...)) {
			t.Fatalf("chunk %s has a wrong CRC", kind)
		}
		kinds = append(kinds, string(kind[:]))
		if string(kind[:]) == "tEXt" && string(data) != "Comment\x00fake-file-faults:failed,renamed;" {
			t.Fatalf("text chunk = %q", data)
		}
	}
	if len(kinds) < 4 || kinds[0] != "IHDR" || kinds[1] != "tEXt" || kinds[len(kinds)-1] != "IEND" {
		t.Fatalf("chunks = %v, want IHDR, tEXt, ..., IEND", kinds)
	}
	if got := markedFaults(marked[:min(len(marked), headerRead)]); !slices.Equal(got, []string{FaultFailed, FaultRenamed}) {
		t.Fatalf("faults of the marked picture = %q", got)
	}
}

func TestMarkPNGRefusesWhatIsNotAPNG(t *testing.T) {
	for name, data := range map[string][]byte{"empty": nil, "text": []byte("not a picture at all, long enough to hold a header"), "truncated": pngOf(t, 4, 4)[:20]} {
		if _, err := MarkPNG(data, FaultFailed); err == nil {
			t.Fatalf("%s: a picture that is not a PNG was marked", name)
		}
	}
	if _, err := MarkPNG(pngOf(t, 4, 4)); err == nil {
		t.Fatal("a marker without a fault was written")
	}
	if _, err := MarkPNG(pngOf(t, 4, 4), "exploded"); err == nil {
		t.Fatal("a fault the fake does not know was written")
	}
}

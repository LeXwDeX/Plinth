package mattingapiv1

import "strings"

// Constructed is one behavior of this fake that no real exchange backs. The
// list is the single place they are named: the endpoint notes quote it (a test
// holds them to it), and the live round settles each entry, after which it is
// moved to a real sample or corrected.
type ConstructedItem struct {
	ID        string
	Endpoints []string
	Behavior  string
	Basis     string
	SettledBy string
}

const (
	pathJobs     = "/jobs"
	pathBatches  = "/batches/{batch_id}"
	pathDownload = "/tasks/{task_id}/download"
)

var constructedItems = []ConstructedItem{
	{ID: "I21", Endpoints: []string{pathDownload},
		Behavior:  "the download is an RGBA PNG of the source picture's pixel size (scaled down to " + maxEchoSideText + " px on the long side) with a transparent background and an opaque subject; a file whose header cannot be read gets 64x64",
		Basis:     "the committed live captures keep no artifact bytes, only image/png; matting semantics",
		SettledBy: "live round: download of a 512x512 and a 256x256 source, record width, height, color type and header"},
	{ID: "I23-bounds", Endpoints: []string{pathJobs},
		Behavior:  "bounds of birefnet_mask_blur (0..50), expand (0..1000), blur_radius (0..100), sensitivity (0..1) and the boolean type of the three switches are the product Schema's, refused as pydantic 422 entries (float_type, greater_than_equal, less_than_equal, bool_parsing, bool_type)",
		Basis:     "product Schema declarations; only the two process resolutions were read from the service source",
		SettledBy: "live round: /openapi.json PipelineParams (I22) and one out-of-range probe"},
	{ID: "I23-upload", Endpoints: []string{pathJobs},
		Behavior:  "more than 20 files refuses the whole batch with a pydantic too_long 422 on body.files; a file over 100 MiB or with an extension outside png/jpg/jpeg/webp/bmp/tiff is a REJECTED task",
		Basis:     "the Offering's source_assets declaration; the service's reaction was never observed",
		SettledBy: "live round: /openapi.json for the files parameter and one batch with a wrongly named file"},
	{ID: "I24-statuses", Endpoints: []string{pathJobs, pathBatches},
		Behavior:  "batch statuses PARTIAL_SUCCESS, FAILED and CANCELED; a batch whose every file is rejected or fails ends PARTIAL_SUCCESS",
		Basis:     "the service's FastAPI BatchStatus enum checked 2026-11-08; no batch ending in them was observed",
		SettledBy: "live round: a batch with one non-picture file, status word recorded"},
	{ID: "I24-task-fields", Endpoints: []string{pathJobs, pathBatches},
		Behavior:  "REJECTED and FAILED tasks: downloadable false, an invalid_reason (REJECTED) or error_message (FAILED), download_path empty for a rejected task, the other of the two fields null",
		Basis:     "the service's TaskStatusItem and TaskStatus enums checked 2026-11-08; the only observed failure message is the out-of-memory one (#299)",
		SettledBy: "live round: full JSON of the rejected or failed task"},
	{ID: "I24-bad-picture", Endpoints: []string{pathJobs, pathBatches},
		Behavior:  "a file whose bytes are not a readable picture is accepted and succeeds (it gets the fixed-size answer)",
		Basis:     "nothing observed; the service always reads the size, so this is certainly not its behavior for every bad file",
		SettledBy: "live round: a .png-named non-picture in a batch of two"},
	{ID: "m8-wrong-key", Endpoints: []string{pathJobs, pathBatches, pathDownload},
		Behavior:  "a key the service does not know (the invalid-key scenario, or a key starting fake-invalid-key) is 401 {\"detail\":\"Invalid authentication credentials\"} with WWW-Authenticate: Bearer; every other non-empty key is accepted",
		Basis:     "FastAPI HTTPBearer vocabulary; the service's own check and its text were never observed",
		SettledBy: "live round: one POST /jobs with a non-empty wrong key"},
	{ID: "m8-www-authenticate", Endpoints: []string{pathJobs, pathBatches, pathDownload},
		Behavior:  "the missing-key 401 {\"detail\":\"Not authenticated\"} carries WWW-Authenticate: Bearer",
		Basis:     "FastAPI documents 401 with a WWW-Authenticate header from 0.122.0 on; the body is the real sample, the header is not in the capture (headers outside the replay set are not stored) and the service's FastAPI version was not read",
		SettledBy: "live round: headers of the missing-key 401"},
	{ID: "m8-5xx", Endpoints: []string{pathJobs, pathBatches, pathDownload},
		Behavior:  "a 5xx answers text/plain \"Internal Server Error\" (Starlette's default for an unhandled exception); rate-limit 429 stays JSON {\"detail\":\"Too Many Requests\"} with Retry-After",
		Basis:     "Starlette's default error response; no 5xx or 429 was observed from the service or its proxy",
		SettledBy: "not reachable on purpose; stays constructed unless an incident log shows one"},
}

// Constructed returns every behavior of the fake that no real exchange backs.
func Constructed() []ConstructedItem {
	items := make([]ConstructedItem, len(constructedItems))
	for i, item := range constructedItems {
		item.Endpoints = append([]string(nil), item.Endpoints...)
		items[i] = item
	}
	return items
}

// constructedNote is the sentence an endpoint note appends: the constructed
// behaviors that touch the endpoint, by id, quoted from the list.
func constructedNote(endpoint string) string {
	var parts []string
	for _, item := range constructedItems {
		for _, path := range item.Endpoints {
			if path == endpoint {
				parts = append(parts, item.ID+": "+item.Behavior)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ". Constructed, pending the live round (" + strings.Join(parts, "; ") + ")"
}

// Batch and task status words the fake emits. READY, RUNNING and SUCCESS (batch)
// and READY, RUNNING and SUCCEEDED (task) are in the committed samples; the
// rest come from the service's FastAPI enums and were never observed.
func RealBatchStatuses() []string        { return []string{"READY", "RUNNING", "SUCCESS"} }
func ConstructedBatchStatuses() []string { return []string{"PARTIAL_SUCCESS", "FAILED", "CANCELED"} }
func RealTaskStatuses() []string         { return []string{"READY", "RUNNING", "SUCCEEDED"} }
func ConstructedTaskStatuses() []string  { return []string{"REJECTED", "FAILED", "CANCELED"} }

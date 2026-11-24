package hyper3dapiv2

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

//go:embed testdata/hyper3d-api-v2-*.candidate.json
var sampleFS embed.FS

var sampleFiles = func() []string {
	names, err := fs.Glob(sampleFS, "testdata/hyper3d-api-v2-*.candidate.json")
	if err != nil || len(names) == 0 {
		panic("hyper3d/api-v2: no committed samples")
	}
	return names
}()

// Prefix is where this version is mounted; hyper3d_base_url points at
// <fake>/hyper3d/api/v2.
const Prefix = "/hyper3d/api/v2"

// samplePrefix is the path prefix the official API uses; the committed samples
// record it.
const samplePrefix = "/api/v2"

const (
	docsRoot = "https://docs.hyper3d.ai/en/api-specification/"
	captured = "2026-10-29"
	// formDocs is when the value rules of the Gen-2.5 form (rodin-gen2-5) were
	// last checked, against the complete page (#303: the server-rendered HTML read
	// in full, after the 2026-11-12 construction and a 2026-11-13 summarizing
	// fetch that had dropped seed, TAPose, quad_normal, the Quad ceiling and
	// legacy). They are constructed from that page, like the error bodies: no wire
	// sample covers a rejected value (see validateGenerationForm). The delivered
	// model and the Texture price (delivery.go) are read from the same page and from
	// docsRoot+"generate-texture" on that day.
	formDocs = "2026-11-16"
	// errorDocs is when the error bodies without a wire sample were last
	// checked against docsRoot+"errors". They are constructed from that page:
	// application failures are HTTP 201 with the reason in error and the
	// detail in message, while 400, 401 and 429 are transport failures. The
	// status and download NO_SUCH_TASK bodies ({error} only) also match the
	// real observation recorded in
	// internal/providers/hyper3d/charge_evidence_test.go (2026-10-21).
	errorDocs = "2026-11-08"
	// realRun is the day of the one paid game-character run on dev-test (#294
	// follow-up, recorded in the /rodin endpoint note). The Gen-2.5 page was read
	// again that day: its price table, its texture_mode defaults by tier and the
	// extra mesh of the Extreme-High tier (see generationCharge and downloadNames)
	// are from that reading.
	realRun = "2026-11-13"
	// labelRun is the day of the two paid requests of #376, which settled how a
	// multipart request carries image_label (rodinLabelNote). Their sanitized
	// exchanges are testdata/evidence-376-*: evidence the fake is held to
	// (evidence_376_test.go), not samples its templates come from (sampleFS does
	// not embed them).
	labelRun = "2026-11-19"
	// contractDocs is when the pages of Check Balance, Check Status, Errors, Bang,
	// Generate Texture and Gen-2.5 were read again, both their prose and the
	// OpenAPI schemas the pages embed (#537). The rules added from that reading
	// (the notes below say which) are constructed: no wire sample covers a
	// rejection, and the two sources of the Texture page disagree on the reference
	// count (see textureReferenceNote).
	contractDocs = "2026-11-24"
)

// realRound568TextureNote records what the Rodin chain of the 1.9 real round
// (2026-11-24, #568) showed of Generate Texture and Bang. The sanitized evidence
// is testdata/evidence-568-processing-high; real_round_568_test.go holds the fake
// to it.
const realRound568TextureNote = "The 1.9 real round on dev-test (2026-11-24, #568) sent the Catalog's form (texture_mode=extreme-high, resolution=High, hd_texture=true): 4 sub-jobs, one listed file served as base_high_pbr.glb with a 12288 base color. The fake names the pack base_<resolution>_pbr.glb like Bang (Basic stays base_basic_pbr.glb, as on 2026-10-29) and delivers TextureModel: the plain fixture's geometry (the real service keeps the uploaded mesh) under a PBR pack from the generation's resolution table, resolution=High taken for HighPack and hd_texture for uhd_texture, so extreme-high at High is 12288 beside two 4096 maps; only that cell is observed. Not modeled: the sub-job count follows the template (3, from the 2026-10-29 Basic sample), not the 4 of the High form; Bang's count likewise stays the sample's 4 while the round's High PBR Bang on a Rodin task ran 6 and on an uploaded model with a picture 8 (both served base_high_pbr.glb, which the fake already names)"

// Job statuses of the official API.
const (
	statusWaiting    = "Waiting"
	statusGenerating = "Generating"
	statusDone       = "Done"
	statusFailed     = "Failed"
)

// tinyModelBytes is the size below which an uploaded texture model is taken for
// the unreadable one-triangle fixture (observed to end Failed).
const tinyModelBytes = 2048

// Fixed values. The fake promises structure, not realism.
const (
	fixedBalance   = 100.0
	fixedTimestamp = "2026-01-01T00:00:00Z"
	// fixedCharge is the receipt's consumed of Bang (0.5 on the page and in every
	// real receipt) and of a generation form outside the Gen-2.5 price table.
	fixedCharge = 0.5
)

// The price table of the Generate Texture page (docsRoot+"generate-texture", read
// in full on formDocs): 0.5 credits for the base texture generation and 2.0 for
// texture_mode=extreme-high. Unlike the Gen-2.5 page it does not say the charges
// add up, and the real receipts say they do not: the Texture Offering's requests
// (texture_mode=extreme-high at Basic and at High resolution, #187) were charged
// 2, not 2.5. A mode below extreme-high, which the page's default (high) is, is
// the base charge; that one is the table applied, not observed.
const (
	chargeTextureBase        = 0.5
	chargeTextureExtremeHigh = 2.0
)

// textureCharge is the credits a texture request is charged.
func textureCharge(fields map[string]string) float64 {
	if fields["texture_mode"] == "extreme-high" {
		return chargeTextureExtremeHigh
	}
	return chargeTextureBase
}

// The Gen-2.5 tier the price table and the extra mesh are about.
const extremeHighTier = "Gen-2.5-Extreme-High"

// extraMeshName is the additional mesh the Extreme-High tier returns next to the
// PBR pack and the preview (see downloadNames).
const extraMeshName = "model_export_raw.glb"

// The price table of the Gen-2.5 page (docsRoot+"rodin-gen2-5", read again on
// realRun): 0.5 credits for a generation, +0.5 for the Extreme-High tier and +2.0
// for texture_mode=extreme-high. The page says the charges are additive and that a
// parameter the table does not list adds nothing. Real receipts: 0.5 for the
// cheapest request of the alignment run, 3 for the display request (#188) and 1
// for the game-character run of realRun (Extreme-High with texture_mode=high,
// Quad, TAPose and quad_normal, so none of those adds a charge). The other
// combinations are the table applied, not observed.
const (
	chargeGeneration         = 0.5
	chargeExtremeHighTier    = 0.5
	chargeExtremeHighTexture = 2.0
)

// defaultTextureModes is the page's texture_mode default by tier, which decides
// the price of a form that omits the field.
var defaultTextureModes = map[string]string{
	"Gen-2.5-Extreme-Low": "extreme-low", "Gen-2.5-Low": "low", "Gen-2.5-Medium": "medium",
	"Gen-2.5-High": "high", extremeHighTier: "high",
}

// generationCharge is the credits a valid generation form is charged: the price
// table above for a Gen-2.5 tier. A form without one (an omitted tier is Regular,
// a Gen-1/1.5 asset) is outside that table and keeps the fixed charge.
func generationCharge(fields map[string]string) float64 {
	tier := fields["tier"]
	if !strings.HasPrefix(tier, "Gen-2.5-") {
		return fixedCharge
	}
	charge := chargeGeneration
	if tier == extremeHighTier {
		charge += chargeExtremeHighTier
	}
	texture, has := fields["texture_mode"]
	if !has {
		texture = defaultTextureModes[tier]
	}
	if texture == "extreme-high" {
		charge += chargeExtremeHighTexture
	}
	return charge
}

// kind describes one task-creating route.
type kind struct {
	Path string
	// Real marks routes whose submit, status and download structure comes from a
	// committed sample.
	DocPage string
	// Need says what the form must carry: "prompt-or-images" (generation), "asset"
	// (bang: a finished task's uuid or an uploaded model) or "model-and-image".
	Need string
}

var kinds = []kind{
	{Path: "/rodin", DocPage: "rodin-gen2-5", Need: "prompt-or-images"},
	{Path: "/bang", DocPage: "bang", Need: "asset"},
	{Path: textureRoute, DocPage: "generate-texture", Need: "model-and-image"},
}

// rodinRealRunNote records the one paid game-character run and what the fake does
// and does not reproduce of it. It is part of the /rodin endpoint note, so it
// travels with the alignment report.
const rodinRealRunNote = "One paid run of the game-character request was made on " + realRun + " (dev-test, #294 follow-up): " +
	"tier Gen-2.5-Extreme-High with mesh_mode=Quad, quality=high, quality_override=10000, texture_mode=high, TAPose=true, quad_normal=true, geometry_file_format=glb, material=PBR and a prompt was accepted, reached Done after about 160 seconds and was charged 1 credit (0.5 for the generation and 0.5 for the tier, nothing for Quad, the pose switch or the normal bake). " +
	"It delivered a PBR GLB of 9,437,496 bytes (one mesh, about 21,800 triangles for 10,000 quads, no skin, three embedded PNG images of 2048x2048, bounding box 1.363 x 1.889 x 0.475 in the A pose) and a WebP preview. " +
	"The raw bodies were not captured, so the fake answers from the page and from what the run showed, not from a recorded wire: consumed follows the page's price table (generationCharge), and the Extreme-High tier lists the extra mesh that the page and the earlier real lists record (" + extraMeshName + "; the real order of the list and the PBR pack's real name for this request were not kept). " +
	"The page's \"tier is required\" callout is advice, not enforcement: an omitted tier is Regular (a Gen-1/1.5 asset), which the fake accepts too, and Plinth always sends an explicit Gen-2.5 tier. The run does not verify a rejected value, TAPose on a subject that is not humanoid, or the Quad ceiling. " +
	"Since #303 the PBR pack follows the request (DeliveredModel, built by fakeupstream.SizedModelFile): its triangle count is the target face count (quality_override, else the page's quality preset, medium when omitted: Quad 50000, 18000, 8000 and 4000 for high to extra-low, Raw 1000000, 500000, 60000 and 20000, and for a form without a Gen-2.5 tier the Gen-1/1.5 page's 50000, 18000, 8000 and 4000 in both topologies) for a triangle mesh and 2.18 triangles per Quad face (this run's one sample: 10,000 quads gave about 21,800 triangles), held to the fixture's 121,032-triangle ceiling, and it carries three PNG textures (base color, metallic-roughness, normal) at the page's texture resolution table: 2048 for texture_mode extreme-low to high and legacy, 4096 with uhd_texture or the HighPack add-on, 8192 with both, and a 6144 base color for extreme-high (12288 with HighPack) beside two 2048 maps (observed in the #188 acceptance; the maps beside the 8192 and 12288 base colors are 4096, observed for 12288 on Bang). " +
	"Not modeled: the real files' scale (a display request of 2,000,000 faces delivers the 121,032-triangle ceiling, not millions, and the real pack is megabytes of pixels where the fake's flat textures are about 1 MB at 6144), the shape (the fixture humanoid in its rest pose whatever TAPose, seed, quad_normal or the prompt), material other than PBR (none, shaded, all and hybrid still deliver the PBR pack), geometry_file_format other than glb, the extra mesh of the Extreme-High tier (the plain fixture under its real name, not an untextured Draco mesh), the preview, and the Bang output (the plain fixture; the Texture output follows its form since #568, see the /rodin_texture_only note). " +
	"The page was read again on " + formDocs + " as the complete server-rendered HTML (the 2026-11-13 reading was a summarizing fetch that dropped fields): it lists seed 0-65535, TAPose and quad_normal as booleans that default to false, the Quad ceiling of 200000 and the 1000000 ceiling below the High tiers, legacy among the six texture_mode values and tier as required advice, so the value rules above stand. Fields it lists that Plinth cannot send (bbox_condition, detail_level, geometry_instruct_mode, is_symmetric) are not enforced here, while the length of prompt and is_micro are since #537 (see below); image_label is the one exception, for its count and its values (see the image_label paragraph below)"

// rodinLabelNote records the two paid requests of labelRun (#376) that settled how
// a multipart request carries image_label, and what the fake does and does not do
// with it. Like rodinRealRunNote it is part of the /rodin endpoint note, so it
// travels with the alignment report.
const rodinLabelNote = "image_label (#376): on " + labelRun + " two real requests (both approved, 0.5 credit in all) settled how a multipart request carries the array: one part per label, the name repeated, in picture order and ahead of the files, as images is sent. " +
	"A request of two pictures with three such parts (F, L, B) was refused before any generation with HTTP 201 {\"error\":\"IMAGE_LABEL_LENGTH_TOO_LONG\",\"message\":\"Input is invalid\"} and was not charged (the balance was the same before and after), so the service reads the repeated parts as one array and compares its length with the number of pictures. " +
	"A request of three pictures (front, left, back) with the labels F, L, B (Gen-2.5-Extreme-Low, texture_mode=extreme-low, quality=medium, mesh_mode=Raw, material=PBR, geometry_file_format=glb) was accepted: the receipt carries the usual fields and an automatic prompt (the fake keeps the sample's for an image request without a prompt), 6 sub-jobs and consumed 0.5 (the labels add nothing to the page's base price), the polls went from Generating and Waiting to Done, and the list named a PBR GLB of 19,371,768 bytes and a WebP preview of 2,034 bytes (the size is not reproduced, see Not modeled above). " +
	"The fake refuses more labels than pictures with the observed body. It also refuses a label outside the page's list (F, FL, FR, B, BL, BR, L, R, U, D and ?) as INVALID_REQUEST, the code of the other constructed value rules: no rejected value was observed, so that code and its message are constructed from the page, not recorded. It does not check a request with fewer labels than pictures: no rejection was observed, so no code is invented for it (Plinth never sends that, its Adapter refuses it locally). " +
	"The sanitized exchanges are testdata/evidence-376-rodin-label-overflow and testdata/evidence-376-rodin-labeled (the capture replaces amounts with placeholders, so their consumed reads 0). They are evidence, not the samples the fake derives its templates from, and evidence_376_test.go holds the fake's answers to them"

// textureReferenceNote records what the Texture reference field is, as of
// contractDocs (#537). It is part of the /rodin_texture_only endpoint note.
const textureReferenceNote = "The reference field of the Texture request: the page's cURL and Python examples and its OpenAPI schema name it images (an array of 1 to 5 files; the prose says \"exactly one reference image\" while the schema says up to five). " +
	"Plinth's Catalog and the committed texture samples (2026-10-29) use the singular image, which the real service tolerated then. " +
	"This fake takes both names, together 1 to 5 files, until a real request with the singular field says whether it is still tolerated: that is pending the real round, and if it is refused the Catalog's wire_name of the Texture reference and the Adapter's image check (internal/providers/hyper3d/operations.go) change to images. " +
	"image_label (the page lists it with the Gen-2.5 rule) and a prompt of more than 1024 characters are rejected like on the Gen-2.5 form (constructed)"

// bangInputNote records the Bang input rules of contractDocs (#537).
const bangInputNote = "Input modes (#537, " + contractDocs + "): the page says exactly one of asset_id and model is accepted (\"Sending both asset_id and model, or neither, is rejected\"), that asset_id mode omits model, image and prompt, that a model needs an image unless material is None (rejected with INVALID_REQUEST and a descriptive message) and that HTTP 400 covers conflicting input modes. " +
	"So asset_id with model, image or prompt is answered with the 400 validator envelope, a model without image while material is not None (an omitted material counts) with INVALID_REQUEST, and neither input with INVALID_REQUEST. The statuses of the two conflict rejections and of the neither case are constructed (not observed; a real probe settles them), the missing-image code is the page's. " +
	"asset_id must be \"a completed Rodin generation task\": the uuid of a Bang or Texture result is answered ASSET_NOT_AVAILABLE (constructed from that sentence); an unfinished generation is the observed ASSET_NOT_AVAILABLE"

// submissionRefusalNote records the submission refusals' counter (#537).
const submissionRefusalNote = "The injected submission refusals (concurrency-limit, permission-denied, no-active-subscription, plan-too-low, object-not-found-on-image, content-violation, api-unknown) follow the fake's fail-times counter (Config.FailTimes or the X-Fake-Fail-Times header, per scenario and key): the first n requests are refused and the next one is accepted, so \"retry once a running task finishes\" can be replayed; without a count the refusal is permanent. The API_INSUFFICIENT_FUNDS message is the errors page's example body. ANIMATION_PREVIEW_UNAVAILABLE is in the error enum of every generation page but has no description or trigger there, so the fake never emits it; the INVALID_REQUEST of a download with \"no downloadable files\" has no trigger the fake can tell from the observed empty list, so an existing task keeps that list"

var endpoints = buildEndpoints()

func buildEndpoints() []fakeupstream.Endpoint {
	out := []fakeupstream.Endpoint{
		{Method: "GET", Path: "/check_balance", Evidence: fakeupstream.EvidenceReal, Sample: "GET /api/v2/check_balance",
			DocURL: docsRoot + "check-balance", CheckedAt: captured,
			Note: "usage_page (#537, " + contractDocs + "): \"usage\" is returned only when the one-based usage_page is given, at most 20 records a page, newest first, each with time, item, amount, task_uuid, api_key_id and api_key_description (the page's schema). The fake lists the charges of the tasks it accepted; item is api_generation_rodin for a generation (the page's example) and a constructed name for Bang and Texture; a page below 1 or not a number is the 400 envelope (constructed). Real usage samples are pending the real round"},
		{Method: "POST", Path: "/status", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v2/status",
			DocURL: docsRoot + "check-status", CheckedAt: captured,
			Note: "Waiting, Generating, Done and Failed polls are real samples. The Failed poll was captured twice (2026-10-29, phase 3): a texture request whose model is the 400-byte one-triangle fixture GLB is accepted and every job is Failed on the first poll with the top-level message \"Invalid input.\" (the fake walks its normal poll cycle first, then answers that Failed body). An unknown subscription_key answers HTTP 201 {\"error\":\"NO_SUCH_TASK\"} (real observation 2026-10-21, charge_evidence_test.go, not committed as a sample); a malformed body keeps the 400 envelope. queue_length (#537, " + contractDocs + "): the page says a Waiting job may carry an approximate count of jobs ahead of it, omitted once it leaves the queue; the real polls carried it on the next job in line only, with value 0 (the Bang sample). The fake keeps that shape and, while the task queues, counts the first job's value down to 0 over the queued polls (constructed; the real value while queued was never observed)"},
		{Method: "POST", Path: "/download", Evidence: fakeupstream.EvidenceReal, Sample: "POST /api/v2/download",
			DocURL: docsRoot + "download-results", CheckedAt: captured,
			Note: "a download asked before the jobs finished answers an empty list (observed); an unknown task_uuid answers HTTP 201 {\"error\":\"NO_SUCH_TASK\"} (real observation 2026-10-21, charge_evidence_test.go, not committed as a sample)"},
		{Method: "GET", Path: "/files/{task_uuid}/{name}", Evidence: fakeupstream.EvidenceConstructed,
			DocURL: docsRoot + "download-results", CheckedAt: captured,
			Note: "the official file host is not part of the API; this route serves the files the download list names (content types as observed): the fixed fixtures, except the PBR pack of a Gen-2.5 generation, which follows its request (see the /rodin note), and the Texture pack, which follows its form (see the /rodin_texture_only note)"},
	}
	for _, k := range kinds {
		note := "multipart request, HTTP 201 for a handled request; a rejected request is HTTP 201 with an error body (observed). API_NO_ACTIVE_SUBSCRIPTION was observed on 2026-11-12 (#294): a real submission was refused with it and the product recorded no HTTP status for the refusal (upstream_status 0), which is what the HTTP 201 error body sent here gives; the message text of the real body was not kept, so the message stays the documented description. The other documented submission codes without a wire sample — API_PARALLELISM_LIMIT_REACHED, PERMISSION_DENIED, API_SUBSCRIPTION_PLAN_TOO_LOW, API_OBJECT_NOT_FOUND_ON_IMAGE, IMAGE_CONTENT_VIOLATION, API_UNKNOWN and the unknown-asset NO_SUCH_TASK — are constructed from " + docsRoot + "errors (checked 2026-11-08)"
		if k.Need == "prompt-or-images" {
			note += ". The value rules of the Gen-2.5 form (seed, mesh_mode, quality, texture_mode, quality_override with its Quad and tier ceilings, the TAPose and quad_normal switches, and the image_label marks) are constructed from " + docsRoot + k.DocPage + " (checked " + formDocs + "): a value outside them is answered INVALID_REQUEST, the documented code for invalid values. The real service may answer a schema-level violation with its HTTP 400 validator envelope instead; neither the status nor the message text of a rejected value is observed. " + rodinRealRunNote + ". " + rodinLabelNote + ". is_micro is refused with INVALID_REQUEST unless the tier is Gen-2.5-Extreme-High (the schema's own rule, " + contractDocs + ") and a prompt over 1024 characters (the schema's maxLength) is refused the same way (constructed message). " + submissionRefusalNote
		}
		if k.Need == "asset" {
			note += ". " + bangInputNote
		}
		if k.Need == "model-and-image" {
			note += ". The receipt's consumed is 2 for texture_mode=extreme-high (real: the Offering's requests at Basic and at High resolution were charged 2, #187) and the base 0.5 for any other mode (the page's table, not observed): " + docsRoot + k.DocPage + " (read in full on " + formDocs + ") lists the two prices without saying they add up, and the receipts say they do not. " + realRound568TextureNote + " " + textureReferenceNote
		}
		out = append(out, fakeupstream.Endpoint{
			Method: "POST", Path: k.Path, Evidence: fakeupstream.EvidenceReal, Sample: "POST " + samplePrefix + k.Path,
			DocURL: docsRoot + k.DocPage, CheckedAt: captured, Note: note,
		})
	}
	return out
}

// Version is the Hyper3D API v2 fake.
func Version() fakeupstream.Version {
	return fakeupstream.Version{
		Provider: "hyper3d", API: "api-v2", Prefix: Prefix, Official: OfficialBase,
		Endpoints: endpoints, Handler: newHandler,
	}
}

type task struct {
	uuid     string
	key      string
	kind     kind
	jobs     []string
	scenario fakeupstream.Scenario
	// expiredIssues is how many of the first artifact URL issues expire under
	// ScenarioArtifactExpired.
	expiredIssues int
	polls         int
	// names are the files the download list offers.
	names []string
	// form is the form of a /rodin or /rodin_texture_only task (nil for Bang):
	// the PBR pack it delivers follows it (DeliveredModel, TextureModel).
	form map[string]string
	// charge is the receipt's consumed, which the usage records repeat.
	charge float64
}

type handler struct {
	env     *fakeupstream.Env
	mux     *http.ServeMux
	nonce   string
	counter atomic.Int64

	mu    sync.Mutex
	tasks map[string]*task
	byKey map[string]*task
	// order is the tasks in submission order, for the usage records.
	order []*task

	balance   map[string]any
	submit    map[string]map[string]any // by route path
	status    map[string]any
	download  map[string]map[string]any // by route path
	failed    map[string]any            // a status poll whose jobs ended Failed (real sample)
	authError map[string]any
	badBody   map[string]any // the 400 envelope
	invalid   map[string]any // INVALID_REQUEST
}

func newHandler(env *fakeupstream.Env) (http.Handler, error) {
	h, err := newFake(env)
	if err != nil {
		return nil, err
	}
	return h.mux, nil
}

func newFake(env *fakeupstream.Env) (*handler, error) {
	h := &handler{
		env: env, nonce: fmt.Sprintf("%08x", uint32(env.Now().UnixMilli())),
		tasks: map[string]*task{}, byKey: map[string]*task{},
		submit: map[string]map[string]any{}, download: map[string]map[string]any{},
	}
	// Each committed sample file holds one route's exchanges; the submit
	// exchange names the route its status and download belong to.
	for _, file := range sampleFiles {
		samples, err := fakeupstream.LoadSamples(sampleFS, file)
		if err != nil {
			return nil, err
		}
		if err := h.index(samples.Exchanges); err != nil {
			return nil, err
		}
	}
	for _, k := range kinds {
		if h.submit[k.Path] == nil {
			return nil, fmt.Errorf("hyper3d/api-v2 samples lack the submit response of %s", k.Path)
		}
		if h.download[k.Path] == nil {
			return nil, fmt.Errorf("hyper3d/api-v2 samples lack the download list of %s", k.Path)
		}
	}
	if h.balance == nil || h.authError == nil || h.badBody == nil || h.invalid == nil || h.status == nil || h.failed == nil {
		return nil, fmt.Errorf("hyper3d/api-v2 samples lack the balance, error, status or Failed status exchanges")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /check_balance", h.getBalance)
	mux.HandleFunc("POST /status", h.postStatus)
	mux.HandleFunc("POST /download", h.postDownload)
	mux.HandleFunc("GET /files/{task_uuid}/{name}", h.getFile)
	for _, k := range kinds {
		mux.HandleFunc("POST "+k.Path, h.submitHandler(k))
	}
	h.mux = mux
	return h, nil
}

func (h *handler) index(exchanges []wirefixture.Exchange) error {
	route := ""
	for _, exchange := range exchanges {
		if exchange.Request.Method != http.MethodPost || exchange.Response.Body.Encoding != wirefixture.PayloadJSON ||
			exchange.Response.Status != http.StatusCreated {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return err
		}
		if jobs, _ := body["jobs"].(map[string]any); jobs != nil && body["uuid"] != nil {
			route = strings.TrimPrefix(exchange.Request.Path, samplePrefix)
			break
		}
	}
	for _, exchange := range exchanges {
		if exchange.Response.Body.Encoding != wirefixture.PayloadJSON {
			continue
		}
		body, err := fakeupstream.JSONBody(exchange)
		if err != nil {
			return err
		}
		apiPath := strings.TrimPrefix(exchange.Request.Path, samplePrefix)
		switch {
		case apiPath == "/check_balance" && exchange.Response.Status == http.StatusOK:
			h.balance = body
		case exchange.Response.Status == http.StatusUnauthorized:
			h.authError = body
		case exchange.Response.Status == http.StatusBadRequest:
			h.badBody = body
		case body["error"] == "INVALID_REQUEST":
			h.invalid = body
		case apiPath == "/status" && hasFailedJob(body):
			h.failed = body
		case apiPath == "/status" && body["jobs"] != nil:
			if h.status == nil {
				h.status = body
			}
		case apiPath == "/download":
			if list, _ := body["list"].([]any); len(list) > 0 && route != "" && h.download[route] == nil {
				h.download[route] = body
			}
		case body["jobs"] != nil && body["uuid"] != nil && h.submit[apiPath] == nil:
			h.submit[apiPath] = body
		}
	}
	return nil
}

// hasFailedJob reports whether a status body has a job that ended Failed.
func hasFailedJob(body map[string]any) bool {
	jobs, _ := body["jobs"].([]any)
	for _, job := range jobs {
		if entry, _ := job.(map[string]any); entry != nil && entry["status"] == statusFailed {
			return true
		}
	}
	return false
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func (h *handler) newUUID() string {
	return fmt.Sprintf("%s-0000-4000-8000-%012d", h.nonce, h.counter.Add(1))
}

// auth answers 401 unless the request carries a bearer key. ScenarioInvalidKey
// rejects even a supplied key: 401 is the documented transport-level failure
// for missing or invalid authentication (docsRoot+"errors", checked errorDocs).
func (h *handler) auth(w http.ResponseWriter, r *http.Request) bool {
	if fakeupstream.BearerKey(r) == "" || fakeupstream.ScenarioOf(r) == fakeupstream.ScenarioInvalidKey {
		writeJSON(w, http.StatusUnauthorized, h.authError)
		return false
	}
	return true
}

// insufficientFundsMessage is the message of the example body on the errors page
// (docsRoot+"errors", read 2026-11-24).
const insufficientFundsMessage = "The fund in your wallet is insufficient to perform this operation."

// render writes the Hyper3D-shaped error of an injected scenario. Rodin reports
// a rejected request with HTTP 201 and an error code (observed for
// INVALID_REQUEST); the codes follow the Catalog's error mapping.
func (h *handler) render(w http.ResponseWriter, _ *http.Request, scenario fakeupstream.Scenario) {
	switch scenario {
	case fakeupstream.ScenarioInsufficientBalance, fakeupstream.ScenarioKeyQuota:
		// A per-key credit limit surfaces as the documented insufficient-funds
		// code: the table has no separate per-key quota code.
		body := fakeupstream.Clone(h.invalid)
		body["error"], body["message"] = "API_INSUFFICIENT_FUNDS", insufficientFundsMessage
		writeJSON(w, http.StatusCreated, body)
	case fakeupstream.ScenarioRateLimit:
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		body := fakeupstream.Clone(h.badBody)
		body["error"], body["statusCode"], body["message"] = "Too Many Requests", json.Number("429"), []any{"Too many requests"}
		writeJSON(w, http.StatusTooManyRequests, body)
	case fakeupstream.ScenarioServiceUnavailable:
		// The overloaded service's explicit "not now": HTTP 503 with
		// Retry-After, the work not taken (#563). The body follows the
		// observed NestJS error envelope; the 503 itself is constructed.
		w.Header().Set("Retry-After", h.env.RetryAfterSeconds())
		body := fakeupstream.Clone(h.badBody)
		body["error"], body["statusCode"], body["message"] = "Service Unavailable", json.Number("503"), []any{"Service unavailable"}
		writeJSON(w, http.StatusServiceUnavailable, body)
	case fakeupstream.ScenarioAcceptedServerError:
		body := fakeupstream.Clone(h.badBody)
		body["error"], body["statusCode"], body["message"] = "Bad Gateway", json.Number("502"), []any{"Bad gateway"}
		writeJSON(w, http.StatusBadGateway, body)
	default:
		body := fakeupstream.Clone(h.badBody)
		body["error"], body["statusCode"], body["message"] = "Internal Server Error", json.Number("500"), []any{"Internal server error"}
		writeJSON(w, http.StatusInternalServerError, body)
	}
}

// renderSubmissionScenario writes the documented submission errors that
// Env.Intercept does not route. Their bodies are constructed from
// docsRoot+"errors" (checked errorDocs): HTTP 201 with the documented
// description as message. No wire sample covers them (API_NO_ACTIVE_SUBSCRIPTION
// was observed once, 2026-11-12, #294, as a refusal the product recorded with no
// HTTP status; the shape here is that of the other HTTP 201 refusals and the
// message is still the documented description). Env.Intercept does not route
// them, so the FailTimes counter is applied here (Env.StillFails).
func (h *handler) renderSubmissionScenario(w http.ResponseWriter, r *http.Request) bool {
	scenario := fakeupstream.ScenarioOf(r)
	code, message := "", ""
	switch scenario {
	case fakeupstream.ScenarioConcurrencyLimit:
		code, message = "API_PARALLELISM_LIMIT_REACHED", "Too many of the account's tasks are already running. Retry once one finishes."
	case fakeupstream.ScenarioPermissionDenied:
		code, message = "PERMISSION_DENIED", "The account may not perform this action."
	case fakeupstream.ScenarioNoActiveSubscription:
		code, message = "API_NO_ACTIVE_SUBSCRIPTION", "The account lacks the API access required by the endpoint or Rodin tier."
	case fakeupstream.ScenarioPlanTooLow:
		code, message = "API_SUBSCRIPTION_PLAN_TOO_LOW", "The subscription plan does not allow API access."
	case fakeupstream.ScenarioObjectNotFoundOnImage:
		code, message = "API_OBJECT_NOT_FOUND_ON_IMAGE", "No object was detected in the input image, or the image is unsupported."
	case fakeupstream.ScenarioContentViolation:
		code, message = "IMAGE_CONTENT_VIOLATION", "The input image is not allowed in your region. Try another image."
	case fakeupstream.ScenarioAPIUnknown:
		code, message = "API_UNKNOWN", "Unexpected server-side error."
	default:
		return false
	}
	// The refusal follows the counter of the other injected faults (#537): with
	// Config.FailTimes or X-Fake-Fail-Times set to n, the first n requests are
	// refused and the next one is accepted, so "wait for a running task to finish,
	// then retry" can be replayed. Without a count the refusal is permanent.
	if !h.env.StillFails(r, scenario) {
		return false
	}
	h.reject(w, code, message)
	return true
}

// ---- endpoints ----

func (h *handler) getBalance(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	body := fakeupstream.Clone(h.balance)
	body["balance"] = fixedBalance
	// "usage" is returned only when usage_page is given (the page's schema, read
	// 2026-11-24): one-based, at most usagePageSize records a page, newest first.
	// The page's minimum of 1 is enforced as the 400 validator envelope, which is
	// how the real service answers a schema violation (not observed for this
	// parameter, so the rejection is constructed).
	if values, present := r.URL.Query()["usage_page"]; present {
		page, err := strconv.Atoi(values[0])
		if err != nil || page < 1 {
			h.badRequest(w, "usage_page must not be less than 1")
			return
		}
		body["usage"] = h.usagePage(page)
	}
	writeJSON(w, http.StatusOK, body)
}

// usagePageSize is the number of usage records of a page ("up to 20").
const usagePageSize = 20

// usageItems are the wallet-ledger item of a task's charge. api_generation_rodin
// is the page's example; the Bang and Texture names are constructed (their real
// values were not observed).
var usageItems = map[string]string{
	"/rodin": "api_generation_rodin", "/bang": "api_bang_rodin", "/rodin_texture_only": "api_texture_rodin",
}

// usagePage lists the charges of the accepted tasks, newest first. Every record
// carries the six fields the schema requires; the key id and description are the
// fake's fixed values.
func (h *handler) usagePage(page int) []any {
	h.mu.Lock()
	defer h.mu.Unlock()
	records := []any{}
	start := (page - 1) * usagePageSize
	for index := len(h.order) - 1 - start; index >= 0 && len(records) < usagePageSize; index-- {
		t := h.order[index]
		records = append(records, map[string]any{
			"time": fixedUsageTime, "item": usageItems[t.kind.Path], "amount": t.charge,
			"task_uuid": t.uuid, "api_key_id": 1, "api_key_description": "Fake key",
		})
	}
	return records
}

// fixedUsageTime has the millisecond form of the page's example.
const fixedUsageTime = "2026-01-01T00:00:00.000Z"

// badRequest writes the 400 validator envelope of the real service (the sampled
// body's shape) with the given message.
func (h *handler) badRequest(w http.ResponseWriter, message string) {
	body := fakeupstream.Clone(h.badBody)
	body["message"] = []any{message}
	writeJSON(w, http.StatusBadRequest, body)
}

// formData is a parsed multipart submission.
type formData struct {
	fields map[string]string
	files  map[string]int
	sizes  map[string]int
	// labels counts the image_label parts (the name repeated, one per label, as
	// the real service reads them, #376): the IMAGE_LABEL_LENGTH_TOO_LONG
	// rejection compares their number with the uploaded images.
	labels int
	// labelValues are those parts' values in wire order (fields keeps only the
	// last one of a repeated name).
	labelValues []string
}

func readForm(r *http.Request) (formData, error) {
	mediaType, params, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return formData{}, fmt.Errorf("not multipart")
	}
	reader, err := r.MultipartReader()
	if err != nil {
		return formData{}, err
	}
	parsed := formData{fields: map[string]string{}, files: map[string]int{}, sizes: map[string]int{}}
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			return parsed, nil
		}
		if nextErr != nil {
			return formData{}, nextErr
		}
		raw, _ := io.ReadAll(io.LimitReader(part, 128<<20))
		name := part.FormName()
		if part.FileName() != "" {
			parsed.files[name]++
			parsed.sizes[name] = len(raw)
			continue
		}
		if name == "image_label" {
			parsed.labels++
			parsed.labelValues = append(parsed.labelValues, string(raw))
		}
		parsed.fields[name] = string(raw)
	}
}

// reject writes a documented application-level error as HTTP 201 with
// {"error": code} and, when not empty, the message (docsRoot+"errors", checked
// errorDocs). An empty message leaves only error, which is the observed shape
// of the status and download NO_SUCH_TASK bodies.
func (h *handler) reject(w http.ResponseWriter, code, message string) {
	body := fakeupstream.Clone(h.invalid)
	body["error"] = code
	if message == "" {
		delete(body, "message")
	} else {
		body["message"] = message
	}
	writeJSON(w, http.StatusCreated, body)
}

// maxGenerationImages is the page's upper bound of reference images (the Gen-2.5
// page and the Texture schema both say 1 to 5).
const maxGenerationImages = 5

// Value rules of the Gen-2.5 form, constructed from the official page
// (docsRoot+"rodin-gen2-5", checked formDocs). A value outside them is answered
// like the other invalid requests, HTTP 201 with INVALID_REQUEST, the documented
// code for invalid values; the message text is the fake's own. Only the fields
// whose values the page restricts and Plinth can send are checked; the rest of
// the form is accepted as before.
const (
	maxSeed                 = 65535
	minQualityOverride      = 500
	maxQualityOverride      = 2000000
	maxQualityOverrideLower = 1000000 // every Gen-2.5 tier below High
	maxQualityOverrideQuad  = 200000  // the additional ceiling of Quad mode
)

var (
	generationMeshModes      = []string{"Raw", "Quad"}
	generationQualityPresets = []string{"high", "medium", "low", "extra-low"}
	// generationTextureModes are the documented texture_mode values; the
	// resolution each gives is the page's texture resolution table (2K for
	// extreme-low to high, 6K for extreme-high, without HighPack), which the
	// fake does not need to model.
	generationTextureModes = []string{"legacy", "extreme-low", "low", "medium", "high", "extreme-high"}
	generationHighTiers    = []string{"Gen-2.5-High", "Gen-2.5-Extreme-High"}
	// generationSwitches are the documented boolean fields.
	generationSwitches = []string{
		"TAPose", "quad_normal", "use_original_alpha", "preview_render", "hd_texture",
		"is_micro", "texture_delight", "uhd_texture", "soft",
	}
)

// validateGenerationForm returns the rejection message of the first field that
// breaks a rule, or "" when the form is valid.
func validateGenerationForm(fields map[string]string) string {
	if message := validatePromptLength(fields); message != "" {
		return message
	}
	if fields["is_micro"] == "true" && fields["tier"] != extremeHighTier {
		// The schema (rodin-gen2-5, read 2026-11-24): "Only allowed when tier is
		// Gen-2.5-Extreme-High; any other tier is rejected with INVALID_REQUEST".
		// An omitted tier is Regular, which is another tier.
		return "is_micro is only allowed when tier is " + extremeHighTier + "."
	}
	if value, present := fields["seed"]; present {
		if number, ok := wholeNumber(value); !ok || number < 0 || number > maxSeed {
			return fmt.Sprintf("seed must be an integer between 0 and %d.", maxSeed)
		}
	}
	mode, hasMode := fields["mesh_mode"]
	if hasMode && !slices.Contains(generationMeshModes, mode) {
		return "mesh_mode must be one of Raw, Quad."
	}
	if quality, present := fields["quality"]; present && !slices.Contains(generationQualityPresets, quality) {
		return "quality must be one of high, medium, low, extra-low."
	}
	if texture, present := fields["texture_mode"]; present && !slices.Contains(generationTextureModes, texture) {
		return "texture_mode must be one of legacy, extreme-low, low, medium, high, extreme-high."
	}
	for _, name := range generationSwitches {
		if value, present := fields[name]; present && value != "true" && value != "false" {
			return name + " must be a boolean."
		}
	}
	value, present := fields["quality_override"]
	if !present {
		return ""
	}
	number, ok := wholeNumber(value)
	if !ok || number < minQualityOverride || number > maxQualityOverride {
		return fmt.Sprintf("quality_override must be an integer between %d and %d.", minQualityOverride, maxQualityOverride)
	}
	tier := fields["tier"]
	if strings.HasPrefix(tier, "Gen-2.5-") && !slices.Contains(generationHighTiers, tier) && number > maxQualityOverrideLower {
		return fmt.Sprintf("quality_override must not exceed %d for tier %s.", maxQualityOverrideLower, tier)
	}
	// Gen-2.5 tiers default to Raw; every other tier (an omitted tier is
	// Regular) defaults to Quad.
	if !hasMode {
		mode = "Quad"
		if strings.HasPrefix(tier, "Gen-2.5-") {
			mode = "Raw"
		}
	}
	if mode == "Quad" && number > maxQualityOverrideQuad {
		return fmt.Sprintf("quality_override must not exceed %d in Quad mode.", maxQualityOverrideQuad)
	}
	return ""
}

// The value domains of the Bang and Texture forms, from
// docsRoot+"bang" and docsRoot+"generate-texture" (read 2026-11-22, re-read
// 2026-11-23 for #487): material accepts PBR, Shaded, All and Hybrid on both
// pages, and Bang additionally takes None (the Bang page lists all five, the
// Generate Texture page lists the first four); resolution is Basic or High, and
// the geometry format one of the supported model formats.
var (
	bangMaterials         = []string{"PBR", "Shaded", "All", "Hybrid", "None"}
	textureMaterials      = []string{"PBR", "Shaded", "All", "Hybrid"}
	processingResolutions = []string{"Basic", "High"}
	processingFormats     = []string{"glb", "usdz", "fbx", "obj", "stl", "usd", "usda", "usdc"}
)

// validateProcessingForm returns the rejection message of the first Bang or
// Texture field outside its documented value domain, or "". The rejection was
// not observed; the code and message are constructed from the pages.
func validateProcessingForm(endpoint string, fields map[string]string) string {
	materials := textureMaterials
	if endpoint == "/bang" {
		materials = bangMaterials
	}
	if value := fields["material"]; value != "" && !slices.Contains(materials, value) {
		return "material must be one of " + strings.Join(materials, ", ") + "."
	}
	if value := fields["resolution"]; value != "" && !slices.Contains(processingResolutions, value) {
		return "resolution must be Basic or High."
	}
	if value := fields["geometry_file_format"]; value != "" && !slices.Contains(processingFormats, value) {
		return "geometry_file_format must be one of " + strings.Join(processingFormats, ", ") + "."
	}
	if endpoint == "/bang" {
		if value := fields["mesh_mode"]; value != "" && !slices.Contains(generationMeshModes, value) {
			return "mesh_mode must be one of Raw, Quad."
		}
		if value := fields["strength"]; value != "" {
			if number, ok := wholeNumber(value); !ok || number < 1 || number > 12 {
				return "strength must be an integer from 1 to 12."
			}
		}
		if value := fields["quality_override"]; value != "" {
			if number, ok := wholeNumber(value); !ok || number < 500 || number > 2000000 {
				return "quality_override must be an integer between 500 and 2000000."
			}
		}
	}
	if endpoint == "/rodin_texture_only" {
		if value := fields["texture_mode"]; value != "" && !slices.Contains(generationTextureModes, value) {
			return "texture_mode must be one of " + strings.Join(generationTextureModes, ", ") + "."
		}
	}
	if value := fields["seed"]; value != "" {
		if number, ok := wholeNumber(value); !ok || number < 0 || number > 65535 {
			return "seed must be an integer between 0 and 65535."
		}
	}
	if value := fields["escore"]; value != "" {
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || number < 0 || number > 10 {
			return "escore must be a number between 0 and 10."
		}
	}
	if value := fields["reference_scale"]; value != "" {
		number, err := strconv.ParseFloat(value, 64)
		minimum := 0.0
		if endpoint == "/rodin_texture_only" {
			minimum = 0.5
		}
		if err != nil || number < minimum || number > 1 {
			return "reference_scale must be a number between " + strconv.FormatFloat(minimum, 'f', -1, 64) + " and 1."
		}
	}
	return ""
}

// generationImageLabels are the image_label values the Gen-2.5 page lists
// (docsRoot+"rodin-gen2-5", checked formDocs): front, front-left, front-right,
// back, back-left, back-right, left, right, up and down, and ? when the side is
// not known. A value outside the list is answered like the other invalid values;
// the rejection was not observed, so the code and the message are constructed.
var generationImageLabels = []string{"F", "FL", "FR", "B", "BL", "BR", "L", "R", "U", "D", "?"}

// validateImageLabels returns the rejection message of the first label outside
// the page's list, or "" when every label is on it.
func validateImageLabels(labels []string) string {
	for _, label := range labels {
		if !slices.Contains(generationImageLabels, label) {
			return "image_label must be one of F, FL, FR, B, BL, BR, L, R, U, D, ?."
		}
	}
	return ""
}

// maxPromptLength is the schema's maxLength of prompt on the Gen-2.5 and the
// Texture pages (read 2026-11-24).
const maxPromptLength = 1024

// validatePromptLength rejects a prompt longer than maxPromptLength characters
// (INVALID_REQUEST, the documented code for invalid values; the message is the
// fake's own).
func validatePromptLength(fields map[string]string) string {
	if utf8.RuneCountInString(fields["prompt"]) > maxPromptLength {
		return fmt.Sprintf("prompt must not exceed %d characters.", maxPromptLength)
	}
	return ""
}

// wholeNumber parses a form value that must be an integer.
func wholeNumber(value string) (int64, bool) {
	number, err := strconv.ParseInt(value, 10, 64)
	return number, err == nil
}

// rejectBangInput applies the input-mode rules of the Bang page (read 2026-11-24)
// and reports whether it answered. The page: "BANG to Parts accepts exactly one of
// two input modes. Sending both asset_id and model, or neither, is rejected";
// asset_id mode takes no model, image or prompt ("Mutually exclusive"); the custom
// model mode needs an image "unless material is None ... omitting it is rejected
// with INVALID_REQUEST and a descriptive message"; and "HTTP 400 covers invalid
// fields, unsupported files, or conflicting input modes". So the conflicting
// modes answer the 400 validator envelope (constructed from that sentence: no
// rejection was observed), the missing image answers INVALID_REQUEST (named by
// the page), and neither input keeps the fake's earlier INVALID_REQUEST.
func (h *handler) rejectBangInput(w http.ResponseWriter, form formData) bool {
	hasAsset, hasModel := form.fields["asset_id"] != "", form.files["model"] > 0
	switch {
	case hasAsset && hasModel:
		h.badRequest(w, "asset_id and model are mutually exclusive; provide exactly one")
		return true
	case hasAsset && (form.files["image"] > 0 || form.fields["prompt"] != ""):
		h.badRequest(w, "model, image and prompt must be omitted when asset_id is given")
		return true
	case hasModel && form.files["image"] == 0 && form.fields["material"] != "None":
		h.reject(w, "INVALID_REQUEST", "A reference image is required unless material is None.")
		return true
	}
	return false
}

func (h *handler) submitHandler(k kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseCreate, h.render) {
			return
		}
		if h.renderSubmissionScenario(w, r) {
			return
		}
		if scenario := fakeupstream.ScenarioOf(r); scenario == fakeupstream.ScenarioServiceUnavailable &&
			h.env.StillFails(r, scenario) {
			h.render(w, r, scenario)
			return
		}
		form, err := readForm(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, h.badBody)
			return
		}
		switch k.Need {
		case "prompt-or-images":
			// IMAGE_LABEL_LENGTH_TOO_LONG is documented as more image_label
			// entries than uploaded images (docsRoot+"errors"), and observed on
			// labelRun (#376): two pictures with three repeated image_label parts
			// were refused with HTTP 201 and this body, which also shows that the
			// repeated parts are read as one array (rodinLabelNote). A request
			// with fewer labels than pictures is not checked: no rejection was
			// observed.
			if form.labels > form.files["images"] {
				h.reject(w, "IMAGE_LABEL_LENGTH_TOO_LONG", "Input is invalid")
				return
			}
			if message := validateImageLabels(form.labelValues); message != "" {
				h.reject(w, "INVALID_REQUEST", message)
				return
			}
			// The official Rodin page documents 1-5 reference images. The upper
			// bound is constructed from those docs: no live rejection sample
			// exists for it. Zero images stays valid for text-to-3D when the
			// form carries a prompt.
			if form.files["images"] > maxGenerationImages {
				h.reject(w, "INVALID_REQUEST", "Please upload between 1 and 5 images.")
				return
			}
			if strings.TrimSpace(form.fields["prompt"]) == "" && form.files["images"] == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload at least one image or give a prompt.")
				return
			}
			if message := validateGenerationForm(form.fields); message != "" {
				h.reject(w, "INVALID_REQUEST", message)
				return
			}
		case "asset":
			if h.rejectBangInput(w, form) {
				return
			}
			if message := validateProcessingForm(k.Path, form.fields); message != "" {
				h.reject(w, "INVALID_REQUEST", message)
				return
			}
			if id := form.fields["asset_id"]; id != "" {
				// A task id that names no task is the documented NO_SUCH_TASK
				// ("The referenced task does not exist or has expired"); only an
				// existing task whose state does not allow the operation is
				// ASSET_NOT_AVAILABLE (observed: an unfinished one). The unknown
				// branch is constructed from docsRoot+"errors" (checked
				// errorDocs), no wire sample covers it. The page says asset_id is
				// "a completed Rodin generation task": a Bang or Texture result is
				// not one, which is answered ASSET_NOT_AVAILABLE too (constructed
				// from that sentence and the errors page's description, #537).
				h.mu.Lock()
				source := h.tasks[id]
				h.mu.Unlock()
				if source == nil {
					h.reject(w, "NO_SUCH_TASK", "The referenced task does not exist or has expired.")
					return
				}
				if source.kind.Path != "/rodin" || !h.finished(source) {
					h.reject(w, "ASSET_NOT_AVAILABLE", "")
					return
				}
			} else if form.files["model"] == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload a model or give an asset id.")
				return
			}
		case "model-and-image":
			if message := validateProcessingForm(k.Path, form.fields); message != "" {
				h.reject(w, "INVALID_REQUEST", message)
				return
			}
			if message := validatePromptLength(form.fields); message != "" {
				h.reject(w, "INVALID_REQUEST", message)
				return
			}
			// The Texture page names the reference field images (the schema: 1 to 5
			// files; the prose: exactly one). Plinth sends the singular image, which
			// the real service tolerated on 2026-10-29 (the committed texture
			// samples), so both are taken until a real request settles whether the
			// singular is still tolerated (#537, textureReferenceNote).
			references := form.files["images"] + form.files["image"]
			if form.files["model"] == 0 || references == 0 {
				h.reject(w, "INVALID_REQUEST", "Please upload one model and one image.")
				return
			}
			if references > maxGenerationImages {
				h.reject(w, "INVALID_REQUEST", "Please upload between 1 and 5 images.")
				return
			}
			// The Texture page lists image_label with the Gen-2.5 rule.
			if form.labels > references {
				h.reject(w, "IMAGE_LABEL_LENGTH_TOO_LONG", "Input is invalid")
				return
			}
			if message := validateImageLabels(form.labelValues); message != "" {
				h.reject(w, "INVALID_REQUEST", message)
				return
			}
		}
		sample := h.submit[k.Path]
		uuids, _ := fakeupstream.Dig(sample, "jobs", "uuids")
		count := len(uuids.([]any))
		t := &task{uuid: h.newUUID(), kind: k, scenario: fakeupstream.ScenarioOf(r), expiredIssues: fakeupstream.ExpiredIssues(r)}
		// Observed on the official service: a texture request whose model is the tiny
		// one-triangle fixture GLB is accepted and then ends with every job Failed
		// ("Invalid input."), twice in a row (see the alignment report).
		if k.Need == "model-and-image" && form.sizes["model"] < tinyModelBytes && t.scenario == fakeupstream.ScenarioNone {
			t.scenario = fakeupstream.ScenarioTaskFailed
		}
		t.key = "fake-subscription-" + t.uuid
		for i := 0; i < count; i++ {
			t.jobs = append(t.jobs, h.newUUID())
		}
		t.names = h.downloadNames(k, form.fields)
		if k.Need == "prompt-or-images" || k.Path == textureRoute {
			t.form = form.fields
		}
		t.charge = fixedCharge
		switch k.Need {
		case "prompt-or-images":
			t.charge = generationCharge(form.fields)
		case "model-and-image":
			t.charge = textureCharge(form.fields)
		}
		h.mu.Lock()
		h.tasks[t.uuid], h.byKey[t.key] = t, t
		h.order = append(h.order, t)
		h.mu.Unlock()
		if t.scenario == fakeupstream.ScenarioLostResponse {
			fakeupstream.AbortConnection(w)
			return
		}
		if t.scenario == fakeupstream.ScenarioAcceptedServerError {
			// Accepted and billed above; the answer is the gateway's 502.
			h.render(w, r, t.scenario)
			return
		}
		body := fakeupstream.Clone(sample)
		body["uuid"], body["submit_time"], body["consumed"] = t.uuid, fixedTimestamp, t.charge
		if prompt, has := body["prompt"]; has && form.fields["prompt"] != "" {
			_ = prompt
			body["prompt"] = form.fields["prompt"]
		}
		jobs, _ := fakeupstream.DigMap(body, "jobs")
		list := make([]any, len(t.jobs))
		for i, id := range t.jobs {
			list[i] = id
		}
		jobs["uuids"], jobs["subscription_key"] = list, t.key
		writeJSON(w, http.StatusCreated, body)
	}
}

// downloadNames are the files a finished task offers. Plinth selects the model
// by the "_pbr.glb" suffix and the preview by name, so a PBR request names its
// model like the official PBR download.
//
// The Extreme-High tier also returns "an additional mesh with up to 10M of faces"
// (docsRoot+"rodin-gen2-5", read again on realRun): an untextured Draco GLB that
// the real list of 2026-10-21 names model_export_raw.glb. Where it stands in the
// real list is not settled (the sanitized capture of 2026-10-26 lists a GLB, the
// preview and a third file; listed before the PBR pack it would take the model
// slot of an Offering without download_files), so it is listed last here. The
// fake serves its fixed GLB under that name (the real file is untextured and
// Draco-compressed); Plinth takes only the files the Offering's download_files
// names, so it never reads it.
func (h *handler) downloadNames(k kind, fields map[string]string) []string {
	sample, _ := h.download[k.Path]["list"].([]any)
	var names []string
	for _, item := range sample {
		entry, _ := item.(map[string]any)
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		if k.Path == "/bang" && fields["material"] != "None" && !strings.Contains(name, "_pbr") {
			name = "base_" + strings.ToLower(orDefault(fields["resolution"], "basic")) + "_pbr.glb"
		}
		// The Texture pack is named after its resolution like Bang's: the
		// 2026-10-29 Basic request listed base_basic_pbr.glb and the 2026-11-24
		// High request (#568) served base_high_pbr.glb.
		if k.Path == textureRoute && strings.HasSuffix(name, "_pbr.glb") {
			name = "base_" + strings.ToLower(orDefault(fields["resolution"], "basic")) + "_pbr.glb"
		}
		names = append(names, name)
	}
	if k.Path == "/rodin" && fields["tier"] == extremeHighTier {
		names = append(names, extraMeshName)
	}
	return names
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// finished reports whether the task's jobs have all been polled to Done.
func (h *handler) finished(t *task) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return t.scenario != fakeupstream.ScenarioTaskFailed && t.polls > h.pollsBeforeDone(t)
}

func (h *handler) postStatus(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	var request map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request)
	key, _ := request["subscription_key"].(string)
	if key == "" {
		body := fakeupstream.Clone(h.badBody)
		body["message"] = []any{"subscription_key must be a string"}
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	h.mu.Lock()
	t := h.byKey[key]
	if t != nil {
		t.polls++
	}
	var n int
	if t != nil {
		n = t.polls
	}
	h.mu.Unlock()
	if t == nil {
		// A supplied key that names no task is an application error with HTTP
		// 201, not the 400 malformed-body envelope: real observation 2026-10-21
		// (charge_evidence_test.go), docsRoot+"errors" checked errorDocs.
		h.reject(w, "NO_SUCH_TASK", "")
		return
	}
	body := fakeupstream.Clone(h.status)
	if t.scenario == fakeupstream.ScenarioTaskFailed && n > h.pollsBeforeDone(t) {
		body = fakeupstream.Clone(h.failed) // real sample: every job Failed and a top-level message
	}
	list := make([]any, len(t.jobs))
	for i, id := range t.jobs {
		list[i] = map[string]any{"uuid": id, "status": h.jobStatus(t, i, n)}
	}
	body["jobs"] = list
	if first, _ := list[0].(map[string]any); first != nil && first["status"] == statusWaiting {
		first["queue_length"] = json.Number(strconv.Itoa(h.queueLength(n)))
	}
	if t.scenario == fakeupstream.ScenarioTaskFailed && n > h.pollsBeforeDone(t) {
		body["message"] = "Invalid input."
	}
	if !h.finished(t) {
		// Hyper3D asks clients to honor Retry-After on a non-final poll
		// (docs.hyper3d.ai/en/api-specification/check-status, read 2026-11-22).
		w.Header().Set("Retry-After", h.env.PollRetryAfterSeconds())
	}
	writeJSON(w, http.StatusCreated, body)
}

// queueLength is the "approximate count of jobs ahead" of the first job while it
// is Waiting (docsRoot+"check-status", read 2026-11-24: the field is carried
// while a job is Waiting and omitted once it leaves the queue). The real polls
// carried it on the next job in line only, with the value 0 (the committed Bang
// sample), which is what every poll gave here before; while the task queues it now
// falls by one per poll to that observed 0, a count that is constructed. It is 0
// for a job that waits on the one before it.
func (h *handler) queueLength(poll int) int {
	if ahead := h.env.QueuedPolls - poll; ahead > 0 {
		return ahead
	}
	return 0
}

// pollsBeforeDone is how many polls answer a non-final state: the Queued polls
// with every job Waiting, then the jobs run one after another, each with a
// Waiting poll (except the first) and a Generating poll.
func (h *handler) pollsBeforeDone(t *task) int {
	return h.env.QueuedPolls + h.env.RunningPolls*(2*len(t.jobs)-1)
}

// jobStatus is the status of job i at poll n, as observed: jobs run in order,
// the finished ones are Done, the running one Generating, the rest Waiting. The
// task-failed scenario ends with every job Failed.
func (h *handler) jobStatus(t *task, i, n int) string {
	if n > h.pollsBeforeDone(t) {
		if t.scenario == fakeupstream.ScenarioTaskFailed {
			return statusFailed
		}
		return statusDone
	}
	if n <= h.env.QueuedPolls {
		return statusWaiting
	}
	// phase 0 is the first job Generating; phase 2k-1 job k Waiting, 2k Generating.
	phase := (n - h.env.QueuedPolls - 1) / h.env.RunningPolls
	running, generating := (phase+1)/2, phase%2 == 0
	switch {
	case i < running:
		return statusDone
	case i == running && generating:
		return statusGenerating
	}
	return statusWaiting
}

func (h *handler) postDownload(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) || h.env.Intercept(w, r, fakeupstream.PhaseRead, h.render) {
		return
	}
	var request map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request)
	id, _ := request["task_uuid"].(string)
	if id == "" {
		body := fakeupstream.Clone(h.badBody)
		body["message"] = []any{"task_uuid must be a string"}
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	h.mu.Lock()
	t := h.tasks[id]
	h.mu.Unlock()
	if t == nil {
		// An unknown, expired or unowned task_uuid is an application error with
		// HTTP 201: real observation 2026-10-21 (charge_evidence_test.go),
		// docsRoot+"errors" checked errorDocs. An existing task that has no
		// files yet (unfinished or failed) keeps the observed empty list: the
		// real texture-failed sample answers {"list":[]}, so the documented
		// no-files INVALID_REQUEST has no distinguishable trigger here.
		h.reject(w, "NO_SUCH_TASK", "")
		return
	}
	template := h.download[t.kind.Path]
	body := fakeupstream.Clone(template)
	list := []any{}
	// Asked before the jobs finish, the official API answers an empty list.
	if h.finished(t) {
		issue := h.env.IssueArtifactURLs(t.uuid)
		for _, name := range t.names {
			list = append(list, map[string]any{
				"name": name, "url": h.env.SignArtifactURL(
					h.env.ArtifactOriginFor(r)+Prefix+"/files/"+t.uuid+"/"+name, t.scenario, issue),
			})
		}
	}
	body["list"] = list
	writeJSON(w, http.StatusCreated, body)
}

func (h *handler) getFile(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	t := h.tasks[r.PathValue("task_uuid")]
	h.mu.Unlock()
	if t == nil || t.scenario == fakeupstream.ScenarioArtifactNotFound {
		http.NotFound(w, r)
		return
	}
	if fakeupstream.ArtifactURLExpired(r, t.scenario, t.expiredIssues) {
		w.Header().Set("Content-Type", expiredContentType)
		w.WriteHeader(expiredStatus)
		_, _ = w.Write([]byte(expiredBody))
		return
	}
	name := r.PathValue("name")
	var file fakeupstream.File
	var err error
	switch path.Ext(name) {
	case ".glb":
		// The PBR pack of a generation and the textures of a Texture task follow
		// their request; the Extreme-High tier's extra mesh and the Bang outputs
		// are the plain fixture.
		switch {
		case t.form != nil && t.kind.Path == textureRoute:
			file, err = TextureModel(t.form)
		case t.form != nil && name != extraMeshName:
			file, err = DeliveredModel(t.form)
		default:
			file, err = fakeupstream.ModelFile("glb")
		}
	case ".webp":
		file, err = fakeupstream.ImageFile("webp")
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := file.Data
	if t.scenario == fakeupstream.ScenarioArtifactCorrupt {
		data = fakeupstream.Corrupt(data)
	}
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// An expired signed file URL is refused with 403 and the file host's JSON
// error: file.hyper3d.com is a Volcengine TOS host (X-Tos-* query parameters,
// seven days), which checks the lifetime before the signature. Observed
// 2026-10-29 by moving X-Tos-Date into the past; the identifiers are fixed.
const (
	expiredStatus      = http.StatusForbidden
	expiredContentType = "application/json"
	expiredBody        = `{"Code":"AccessDenied","RequestId":"fake-request","HostId":"fake-host","Message":"Request has expired","EC":"0002-00000032","ExpiresSeconds":604800,"Expires":"2026-01-08T00:00:00Z","ServerTime":"2026-01-09T00:00:00Z"}`
)

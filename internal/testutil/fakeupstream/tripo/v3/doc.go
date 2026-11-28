// Package tripov3 is the fake of the Tripo OpenAPI version the Catalog's
// tripo_base_url points at (https://openapi.tripo3d.ai/v3). It is mounted at
// /tripo/v3. This version is frozen once released: a new Tripo API version gets
// its own sibling package (tripo/v4, ...) and never edits this one.
//
// Task statuses. The committed samples cover queued, running, success and
// failed. The fake can also inject cancelled (X-Fake-Scenario: task-canceled,
// or a "fake-task-canceled..." key) and unknown (X-Fake-Scenario: task-unknown,
// or a "fake-task-unknown..." key). No real sample of either status was
// captured, so both poll bodies are constructed from the official pages read on
// 2026-11-08 (https://developers.tripo3d.ai/en/docs/task-lifecycle and
// https://developers.tripo3d.ai/en/docs/task-query): cancelled is a terminal
// status a running task can end in, unknown is not a success or failure signal
// and the caller keeps polling with a timeout. Structurally they derive from
// the real failed poll of the same task type with error_code and error_message
// removed. The legacy internal outcomes banned and expired are never emitted:
// the lifecycle page says they are reported as failed. "task-unknown" is a
// Tripo-local scenario word because fakeupstream.Scenario has no task-unknown
// yet; it is selected through the same header and key prefix, and moves to the
// shared list if another version needs it.
//
// Constructed error responses. The committed samples only cover the 401
// authentication envelope, the GET task 404/code 2001 envelope and one create
// 400/code 1004 envelope. Every other error the fake injects reuses that real
// error envelope and is documented by the official pages read on 2026-11-08
// (https://developers.tripo3d.ai/en/docs/error-handling, .../rate-limits,
// .../files):
//
//   - A create body that is not a JSON object or is syntactically broken is
//     400/code 1003 ("The request body is malformed"). An empty body names no
//     parameter and stays 400/code 1004; a missing or invalid parameter keeps
//     the real 400/code 1004 answer.
//   - A create request whose input names a task the fake does not know is
//     404/code 2001 (the task does not exist or is not the caller's). File
//     tokens ("file_...") and http(s) URLs pass through unchanged.
//   - POST /files refuses an empty part with 400/code 2003 and a type outside
//     JPEG/PNG/WebP pictures and GLB/GLTF/FBX/OBJ/STL models with 400/code
//     2004. The part is streamed, not capped, because the official model limit
//     is 150 MB; the per-type size limits themselves are not enforced.
//   - Request-rate limiting is 429/code 1007 with X-RateLimit-Limit,
//     X-RateLimit-Remaining and X-RateLimit-Reset; a full per-category concurrency
//     pool (creation only) is the distinct 429/code 2000 with Retry-After. The
//     pools are independent by task category (3D generation H/P, image generation,
//     animation, model processing, mesh operations), so one category filling up
//     never blocks another.
//
// Rig and retarget requests (#277). The samples hold one request of each (rig_type
// biped, spec tripo, preset:walk). The fake also checks the values the official
// pages read on 2026-11-12 list (https://developers.tripo3d.ai/en/docs/animations-rig,
// .../animations-retarget, .../animations-rig-check) and refuses others with the
// create envelope of the samples (400/code 1004): model, rig_type (the seven rig
// check types plus others, which exists only under v3.0), spec (actorcore, unity,
// unreal and vrm exist only under v3.0; a spec has no effect on a non-biped rig and
// is not refused), out_format, and animation XOR animations (at most 5). A poll
// repeats the accepted spec, out_format and rig type (named topology, as captured)
// and the retarget's presets and switches. A retarget whose preset does not fit the
// source rig (the 11 v3.0 biped presets need a v3.0 biped rig, a creature preset a
// rig of its type built with v2.5 or v3.0) ends "failed" with code 1004, the one
// failure that was captured (a retarget of a v2 rig); that every other mismatch
// fails the same way is an assumption. None of these values has been requested from
// the real API: they are constructed, not observed.
//
// Model conversion (POST /models/convert). The request is checked against the
// parameters of the official page read on 2026-11-12
// (https://developers.tripo3d.ai/en/docs/models-convert, convert.go): format must
// be GLTF, FBX, USDZ, OBJ, STL or 3MF (GLB is not a target); texture_format,
// export_orientation, fbx_preset and texture_packaging take only the listed
// values; texture_size and face_limit are integers; the other options have the
// documented JSON type; texture_packaging is refused for any format but FBX.
// A refusal is the real 400/code 1004 envelope (the value-level refusals are
// constructed). The task object shows the request it ran under input, next to
// original_model_id, exactly as the committed real sample does. The exported FBX
// embeds one texture in the requested texture_format, and in JPEG, the official
// default, when the request names none, so a delivered file shows which format
// was asked for; quad forces the FBX output. A conversion that names a texture
// size, a texture format or pivot_to_center_bottom writes them (#305, convert.go):
// the GLB and the FBX carry a texture of the requested format at the requested
// size (default JPEG at 4096), a GLB or FBX of a rigged source keeps its skin
// (and, with with_animation, its clip), the GLB of a decimated source keeps its
// triangles, and the origin is the middle of the bounding box without
// pivot_to_center_bottom and the middle of the bottom face with it; the finished
// GLTF and FBX tasks report that move as output.normalize_scale and
// output.normalize_translation. A conversion that names none of the three is the
// fixed fixture it always was. fbx_preset, bake and the options the page lists but
// Plinth never sends (texture_packaging zip, part_names, scale_factor, ...) are
// validated and echoed, not modelled in the bytes. The creation of a conversion
// from a URL waits fakeupstream.Config.ConvertURLDelay (10 ms by default; the real
// API took 47 to 48 seconds). Billing follows billing.go: 5 credits, 10 when
// texture size, texture format, the pivot or another option leaves the page's
// default (quad, face_limit, flatten_bottom, flatten_bottom_threshold,
// scale_factor), shown from the running poll on.
//
// Retopology (POST /mesh/decimate, #276). The request is checked against the
// official page read on 2026-11-12 (https://developers.tripo3d.ai/en/docs/mesh-decimate,
// decimate.go): the source is a text, image or multiview generation task, a file
// token or a URL (any other task is 400/code 1004; the page names no model version
// of the source, so a P-series task passes); model is v3.0, v2.0 (the default) or
// v1.0; face_limit follows the ranges of the page; smart_uv exists only with v3.0.
// The task object repeats bake, quad and smart_uv with the page's defaults. The
// delivered GLB is the fixed textured humanoid built to the requested face_limit,
// not a reduction of the source (fakeupstream.LowPolyModelFile), and quad=true
// delivers an FBX (the page's v2.0 line and the one real v3.0 quad task). All of
// it is constructed from the page except the one captured valid v3.0 request.
//
// P2 quad output (#294). A P-series generation request (text, image or
// multiview) with quad=true delivers an FBX and no GLB, like the real P2
// task did on 2026-11-12: every model URL of the task's output names
// model.fbx (p2_quad.go), served as binary/octet-stream, which the Tripo adapter
// labels application/octet-stream with no variant. Nothing else about the task
// changes. Only P2 was observed, so a quad request to an H-series model still
// delivers a GLB, and the multiview endpoint is assumed to behave like the other
// two.
//
// Generation textures (#704). A generation request with texture=false (and no
// pbr=true) delivers a GLB with one material and no texture or image, and so does
// a conversion of its model, like the real v3.1 text task of 2026-11-28 whose
// model GLB had none; a request with PBR on (Plinth's default, and the page's)
// delivers a GLB with three textures and three images in one material (base
// color, metallic-roughness, normal), and so does a plain conversion of its
// model, like the same task at the defaults (untextured.go). Only the text
// endpoint was observed, and only from Plinth's side: the exchanges and the bytes
// were not recorded, so which maps the three textures are is constructed.
//
// Multiview model and its 3MF export (#386). One approved real run of 2026-11-19
// (evidence-386-multiview-model.candidate.json and evidence-386-convert-3mf.candidate.json,
// held to the fake by evidence_multiview_3mf_test.go) requested a multiview model
// at Plinth's production defaults from three views and then its 3MF export. It
// settled what had been constructed from the documentation:
//
//   - the poll of a multiview_to_model task shows the request's own parameters under
//     input, the request's model as model_version and a files array of four
//     entries (front, left, back, right: a stored object each, null for a view that
//     was not sent), where the fake used to answer with the input of a rig task
//     because the poll template skipped the image_to_model base (multiview.go,
//     pollBody);
//   - the render of that task is a WebP file (rendered_image.webp, binary/octet-stream
//     like every artifact), where the text, image, texture and decimation tasks of
//     2026-10-29 delivered PNG bytes;
//   - the 3MF a conversion delivers is a package of three ZIP members, every one
//     framed with ZIP64 however small, whose model root declares Tripo's vendor
//     namespace https://www.tripo3d.ai/ns/aigc/1.0 for one AIGC label. The fake
//     delivers a package of that shape (tripo3mf.go), not the generic 3MF fixture,
//     because Plinth's archive isolation refused the real file for that namespace.
//
// Not modelled because the sanitized evidence cannot show it or nothing real backs
// it: the price of both tasks (70 and 5 credits) shows from the queued poll on in
// the real run, where the fake keeps the sample's value until the task runs; the
// real multiview poll shows the render (output.rendered_image_url) from progress 99,
// before the model, where the fake's finished poll shows both; and a parameter the
// request leaves out is not echoed, because the run sent every parameter.
//
// Request contracts, source tasks and prices (#541). The official pages read on
// 2026-11-24 (generation.go, processing.go, billing.go) replaced what had been a body
// that only needed a model and a prompt or an input:
//
//   - The three generation endpoints check the request like the real API checks it for
//     the families the pages list: model names (H v3.1/v3.0/v2.5, P P2/P1; any other
//     name, a retired one included, is 400/code 1004), the enumerations, the v3.0 gate of
//     the new parameters, the face_limit ceilings, quad for P2 only, generate_parts, fast
//     with a v2.5 texture, and the text lengths. Real evidence exists for the H3.1 model
//     only: the P series, the rule that P2 refuses the H-only parameters and every refusal
//     text the free probes of 2026-11-25 did not capture are constructed, and the wire
//     coverage matrix counts an Offering of another model as having no real sample of
//     those endpoints.
//   - The processing endpoints check their values (texture, segmentation, completion,
//     Smart Segmentation) and their source tasks (processing.go): a rig check and a rig
//     take a text, image or multiview task; a retarget takes a rigged model or a task run
//     on it; a completion needs a segmentation; an edit needs an Image to Multiview task; a
//     conversion by task id looks the task up. Smart Segmentation answers 2007 for a task
//     that has not succeeded and 2006 for one with no usable picture or GLB.
//   - A retarget of a v2.5 biped rig ends "failed"/1004 by default, the one observed run
//     (2026-10-29). The rig page now says such a rig is rigged as v3.0, which would make it
//     take the v3.0 presets; the two conflict, so that reading is only the
//     retarget-v25-biped-as-v3 scenario (rig.go), constructed, until one real
//     rig-and-retarget round settles which is true (#541).
//   - A generation, texture, segmentation, completion, Smart Segmentation, retarget and
//     multiview task reports the price of the pricing page (billing.go); P1 and image to
//     splat have no published price and keep the sample's placeholder.
//   - X-RateLimit-Reset is a Unix timestamp in seconds; rate-limit-reset-only is the 1007
//     answer with that header and no Retry-After; businessErrors and the task-failed-<word>
//     words inject 2008, 2015, 2018, 2002 and 4001 (fake.go). Those five answers are the
//     error-handling page's, constructed: no request has drawn one from the real API.
//     2015 in particular is not what a retired model name gets (the probes, below).
//
// The real round of 2026-11-24 (#577). One run of smoke-l5-tripo-endpoints.py through
// Plinth on dev-test (17 functions, 855 credits as quoted) was recorded by the runtime
// recorder; evidence-577-real-round.candidate.json keeps the minimal set and
// real_round_577_test.go holds the fake to it (every create sent with its recorded
// request: the structure of the first and the finished poll, the price, the input).
// Prices, task types, status words, output keys and the CDN content types matched the
// fake already. What it changed:
//
//   - A poll repeats the request under input (echo.go): every parameter as sent over
//     the template's defaults, the model as model_version where the type shows one. The
//     fake used to keep the 2026-10-29 template, so a request's face_limit,
//     texture_quality or segmentation_granularity never showed.
//   - credits_consumed shows the price from the queued poll on (billing.go); image to
//     splat costs 30.
//   - A conversion of an imported model (URL or file token) reports normalize_scale and
//     normalize_translation for every format; one of a task named by its id reports
//     neither (convert.go). A Smart Segmentation of a model reports them too.
//   - Every render (rendered_image) is a WebP.
//   - A decimation's face_limit is a target the real task overshot by 57% (decimate.go);
//     the fake does not model the overshoot.
//
// Not modelled: the real running poll of a conversion sometimes has no output, and
// sometimes output.model_url before the task succeeds (the fake's running poll has an
// empty output); the real progress values of the other task types (the fake answers
// 0, 50 and 100, and the real Smart Segmentation went 50, 10, 25, 75); and the real
// first poll of those is usually already running (the fake answers one queued poll
// first). The running polls of a text to model task follow the real round of
// 2026-11-25 (#608, poll.go). Not run in that round, so still
// constructed: the P2 Offerings (text, image, multiview), Smart Segmentation of a
// picture, the 2000 concurrency refusal and the 1007 rate limit.
//
// The free negative probes of 2026-11-25 (#577). The eleven probes of align.go (Probes)
// ran against the official API after the upload: twelve requests, no credit spent, no
// task made, every probe refused. evidence-577-negative-probes.candidate.json keeps the
// nine JSON probes (the recorder keeps no empty or text/plain file part) and
// probes_577_test.go holds the probe table to it and the fake to the table. What the
// fake took from it:
//
//   - A model no family has is 1004 "invalid model '<name>', allowed values: P1-20260311,
//     P2-20260801, v2.5-20250123, v3.0-20250812, v3.1-20260211" (the names in byte
//     order). The retired v2.0-20240919 got exactly that, not the 2015 "version has been
//     deprecated" the fake answered for its list of retired names, which is gone. The
//     allowed list is the fake's own five names; only their order changed.
//   - generate_parts with texture omitted: "generate_parts is incompatible with texture;
//     set texture=false to generate parts". fast with the v2.5 texture: "texture_quality
//     'fast' requires texture v3.5-20260815; got v2.5-20250123". An unknown seg_type:
//     "invalid seg_type; allowed: image, model". Conversion to GLB: "invalid format
//     'GLB', allowed values: 3MF, FBX, GLTF, OBJ, STL, USDZ" (byte order again).
//   - Upload refusals: 2003 "The image file is empty" with the suggestion "Check if the
//     file is corrupted", 2004 "This image file type is not supported" with "Refer to API
//     documentation for supported image file types" (the codes and messages were right,
//     the suggestions were not).
//   - A wrong key is 401 code 2 "Invalid API key", its suggestion naming the tsk_ prefix;
//     a missing Authorization is the captured "Authentication required". Neither 401
//     carries a request_id; every 400 and the 404 does.
//   - A rig check of a task id that does not exist is 404/2001 "The task is not found",
//     as the task poll's sample already said.
//
// Still constructed, because no free request draws them: the text of every other 1004
// (the remaining enumerations, ranges, source-task and v3.0-gate refusals, the generate_parts
// refusal for pbr=true, fast implied by a v2.5 model), the business codes 2008, 2015,
// 2018, 2002 and 4001, the task-creation refusals 2006 and 2007, the credit refusals,
// the 1007 rate limit and the 2000 concurrency refusal, and the answer to a tsk_ key the
// real API does not know.
//
// Balance scenarios. insufficient-balance and key-quota are credit refusals of
// task creation, so they never apply to POST /files; the files page defines no
// such refusal for an upload.
package tripov3

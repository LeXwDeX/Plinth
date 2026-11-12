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
//     X-RateLimit-Remaining and X-RateLimit-Reset; a full concurrency pool
//     (creation only) is the distinct 429/code 2000 with Retry-After.
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
// was asked for; quad forces the FBX output. pivot_to_center_bottom, fbx_preset,
// texture_size and the options the page lists but Plinth never sends
// (texture_packaging zip, part_names, scale_factor, ...) are validated and
// echoed, not modelled in the bytes: the fixture is one fixed small model.
// Billing is not modelled; the pricing page treats any non-default value of
// quad, face_limit, flatten_bottom, flatten_bottom_threshold, texture_size,
// texture_format, pivot_to_center_bottom or scale_factor as an advanced
// conversion (10 credits instead of 5).
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
// Balance scenarios. insufficient-balance and key-quota are credit refusals of
// task creation, so they never apply to POST /files; the files page defines no
// such refusal for an upload.
package tripov3

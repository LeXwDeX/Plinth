// Package openaiv1 is the fake of the OpenAI-compatible /v1 chat API that the
// Catalog's llm_base_url points at (chat/completions with streaming, tool
// calls and reasoning_content, responses, and models). It is mounted at
// /openai/v1 and is frozen once released; another wire version gets a sibling
// package.
//
// The committed samples record the dev-test gateway's wire. Where that gateway
// diverges from the OpenAI documents, the fake mirrors the gateway and records
// the divergence here and in the alignment report instead of inventing a
// sample:
//   - the gateway error object is {code, message, path, type}; the official
//     Error required fields are type, message, param and code, and there is no
//     path (components.schemas.Error;
//     https://github.com/openai/openai-openapi/blob/master/openapi.yaml,
//     checked 2026-11-08). The sample-derived faults keep the gateway
//     envelope; only the constructed documentation-based answers below carry
//     param;
//   - a request without a key answers the gateway's bare text/plain
//     "unauthorized", while the official 401 declares a JSON ErrorResponse or
//     text/plain JSON-encoded error text; that 401 is a real sample.
//
// The fault scenarios the samples do not cover are constructed from the
// OpenAPI and the error-code guide
// (https://developers.openai.com/api/docs/guides/error-codes, checked
// 2026-11-08): invalid-key 401, forbidden 403, model-not-found 404,
// service-unavailable 503, quota-exceeded 429 and a streamed error frame. Two
// more stream faults (#509) are constructed from the streaming contract (every
// choice ends with a finish_reason chunk, the stream with data: [DONE]): a
// stream closed cleanly before its last chunk (StreamCutMarker) and a
// streaming request answered with the plain completion object
// (StreamPlainMarker).
//
// The chat forms and refusals #536 added are constructed too, from the
// vendors' documents and the Plinth Schema (no committed sample has them): the
// DashScope stream whose usage rides on a last chunk of empty choices
// (constraints.go, streamUsageTrailing; that is also the official
// include_usage shape, while the default shape, usage on the finish_reason
// chunk, is the observed gateway form and differs from it), the refusals of a request's pictures
// (image_refusal.go; the 1-pixel refusal of a qwen model and the
// "no image input" refusal are the observed wordings the #497 and #253 tests
// hold, the other kinds are in the vendor's style), the DSML tool-call reply,
// the picture the model draws itself (delta.images), the quota codes the Schema
// declares (429), the closed reasoning_effort list of DeepSeek and the broad
// reading of its reasoning_content rule behind a marker (chat_forms.go). Each
// is asked for by a fake-only request convention or by the model's rules, and
// each waits for a real round to become a sample.
//
// Model constraints (#401, constraints.go) are not scenarios: a request that
// names gpt-astra or gpt-6.2-astra is judged by that model's rules on every
// call, as the real gateway does. The tools and temperature refusals are the
// observed answers of the gateway's logs of 2026-11-20 (the bodies, with the
// gateway's own key order, are kept without request IDs or prompt text); the
// reasoning_effort none refusal is constructed from OpenAI's latest-model
// guide and model page (read 2026-11-20). Which of the two sources a rule
// comes from is stated in constraints.go.
//
// POST /responses (#401, responses*.go) is the wire those models serve
// function tools on. Its answers are cloned from
// testdata/responses-2026-11-20.sample.json, a sanitized excerpt of two real
// probes of the gateway: the streamed tool call, text answer and parallel
// calls (an event: line equal to data.type, no [DONE]), the reasoning item and
// its events, an input_image request, the non-stream response and the 400s the
// constrained models answer (temperature present, an unknown reasoning.effort,
// none, minimal, a function_call_output or function_call without its pair), byte
// for byte. The probes are not wirefixture candidates (the capture pipeline
// refused SSE with event: lines then; since #536 it records such a stream, so
// the next real one is recorded instead of hand-sanitized), so the endpoint is
// declared constructed and says what the sample does not cover (response.incomplete and
// response.failed, the in-stream error event, response.refusal.delta and
// response.refusal.done, the reasoning summary events, the 400 for a
// reasoning item without its following item): those parts are built from the
// OpenAI documents. The fake never sends a stream line over a client's line
// bound: the bound is the Adapter's own setting, not an upstream contract, and
// the Adapter's tests serve such a line from a scripted handler. A Responses
// request is also read as a Chat Completions one (responsesRequest.chatView),
// so the fake-only request conventions work on both wires, and the Responses
// ones are ReasoningMarker, IncompleteMarker, FailedMarker, UnknownEventMarker
// and RefusalMarker.
package openaiv1

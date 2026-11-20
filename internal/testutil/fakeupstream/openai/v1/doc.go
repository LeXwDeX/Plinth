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
// service-unavailable 503, quota-exceeded 429 and a streamed error frame.
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
// for byte. The probes are not wirefixture candidates, because the capture
// pipeline refuses SSE with event: lines, so the endpoint is declared
// constructed and says what the sample does not cover (response.incomplete and
// response.failed, the in-stream error event, response.refusal.delta, the
// reasoning summary events, a stream line over the bound, the 400 for a
// reasoning item without its following item): those parts are built from the
// OpenAI documents. A Responses request is also read as a Chat Completions
// one (responsesRequest.chatView), so the fake-only request conventions work on
// both wires, and the Responses ones are ReasoningMarker, IncompleteMarker,
// FailedMarker and UnknownEventMarker.
package openaiv1

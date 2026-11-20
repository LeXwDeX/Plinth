// Package openaiv1 is the fake of the OpenAI-compatible /v1 chat API that the
// Catalog's llm_base_url points at (chat/completions with streaming, tool
// calls and reasoning_content, and models). It is mounted at /openai/v1 and is
// frozen once released; another wire version gets a sibling package.
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
package openaiv1

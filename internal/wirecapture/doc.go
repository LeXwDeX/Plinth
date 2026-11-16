// Package wirecapture sanitizes upstream HTTP exchanges into public, fail-closed
// values and can record them: JSON, SSE (normalized), narrow multipart file
// uploads and binary payloads (descriptors only) become Exchange values that
// carry structure (field names, types, status codes, error codes, status words)
// and no credential, signed URL, address, prompt text, media byte or person.
//
// It holds, once, the rules every consumer shares: the sanitizer (Sanitize*),
// Validate, the fixed synthetic media recipes, the draft derivation that builds
// a private policy from one raw exchange, UpstreamRecorder and its record
// directory, and the candidate/draft writers. Three callers use them:
//
//   - the application (internal/app), which wraps each Provider and chat
//     connection's transport with NewRuntimeRecorder when the operator sets
//     UPSTREAM_CAPTURE_DIR (#233);
//   - the test tooling (internal/testutil/wirefixture, cmd/fake-upstream-align),
//     which builds its Recorder, replay and private evidence on the same rules
//     and re-exports the shared types;
//   - the independent scan in the privacyscan subpackage, which re-reads what
//     was written with separately written rules.
//
// The package imports only the standard library and no application package, so
// production code may link it; code under internal/testutil may not be linked
// by shipping binaries (TestProductionBinariesDoNotLinkAnyTestutilPackage).
package wirecapture

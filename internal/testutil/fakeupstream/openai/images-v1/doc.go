// Package openaiimagesv1 is the fake of this upstream API version (issue #96,
// phase 3).
//
// The committed samples record the dev-test OpenAI-compatible image gateway's
// wire; the fake keeps that structure for the success and 400/401 answers. The
// fault answers the samples do not cover are constructed from the recorded
// gateway refusals and the official documents, and are named in the endpoint
// table: quota-exceeded 429 (error.code/type quota_exceeded with error.quota),
// forbidden 403 (eca_access_denied) and service-unavailable 503
// (service_unavailable_error/server_is_overloaded; error-code guide checked
// 2026-11-08). model is optional on POST /images/edits, as the official Create
// image edit reference says, and required on POST /images/generations.
package openaiimagesv1

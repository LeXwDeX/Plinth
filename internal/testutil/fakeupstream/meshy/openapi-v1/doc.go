// Package meshyopenapiv1 is the fake of the Meshy OpenAPI v1 routes the Catalog
// uses (https://api.meshy.ai/openapi/v1): balance, text-to-image,
// image-to-image, image-to-3d, multi-image-to-3d, remesh, convert, retexture,
// rigging, animations, uv-unwrap, resize, print/analyze, print/multi-color and
// text-to-motion. It is mounted at /meshy/openapi/v1; a Plinth stack points
// meshy_base_url at <fake>/meshy because the adapter appends openapi/<version>.
// This version is frozen once released: a new Meshy route version gets its own
// sibling package and never edits this one.
//
// Answers the committed samples cannot hold are constructed from the official
// documentation (checked 2026-11-08) and named in the endpoint notes:
//
//   - ScenarioForbidden answers the documented 403 {message} envelope
//     (https://docs.meshy.ai/en/api/errors).
//   - ScenarioRigNonHumanoid answers the rigging page's 422 pose-estimation
//     refusal (https://docs.meshy.ai/en/api/rigging).
//   - ScenarioTaskCanceled ends a task CANCELED (documented status enum).
//   - X-Fake-Task-Error selects a documented task_error type (invalid_input,
//     timeout, service_unavailable, server_error).
//   - PENDING, CANCELED and non-rigging FAILED poll bodies follow the field
//     documentation of each task object; texture_urls and PBR maps follow the
//     request's should_texture/enable_pbr options.
//
// print/repair (POST /openapi/v1/print/repair and its poll/delete routes) is
// deliberately not served: the Catalog offering meshy/processing/
// repair-printability is disabled, so no egress is expected. Enabling it needs
// a captured sample, a Kind and endpoint rows.
package meshyopenapiv1

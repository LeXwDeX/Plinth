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
//   - The rigging request is checked against the page read on 2026-11-12
//     (https://docs.meshy.ai/en/api/rigging-and-animation, #277): skeleton_template
//     is one of the nine documented templates, include_fingers a boolean that
//     cannot go with texture_image_url, height_meters not negative; a violation
//     answers 400 {message}. The page also requires a biped rig for the two
//     options and answers 422 for a model that can only be rigged with the default
//     skeleton or without fingers; the fake judges no rig type and models neither
//     422. None of it has been requested from the real API.
//   - The remesh options of an image-to-3d request (and of the v2 text-to-3d
//     preview) are checked against the pages read on 2026-11-12 (#276,
//     core.GenerationRequestError): target_polycount is 100-300000 for a
//     remeshed standard model, 100-15000 for meshy-t2 and 100-30000 for
//     meshy-t2.5, quad topology is refused for the Smart Topology models,
//     decimation_mode is 1-4; a violation answers 400 {message}. The pages say
//     nothing about how a quad counts toward target_polycount or whether the
//     texture of a remeshed image-to-3d task is generated on the remeshed mesh;
//     neither is modelled. None of it has been requested from the real API.
//   - PENDING, CANCELED and non-rigging FAILED poll bodies follow the field
//     documentation of each task object; texture_urls and PBR maps follow the
//     request's should_texture/enable_pbr options.
//
// print/repair (POST /openapi/v1/print/repair and its poll/delete routes) is
// deliberately not served: the Catalog offering meshy/processing/
// repair-printability is disabled, so no egress is expected. Enabling it needs
// a captured sample, a Kind and endpoint rows.
package meshyopenapiv1

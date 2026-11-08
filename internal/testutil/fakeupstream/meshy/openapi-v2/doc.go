// Package meshyopenapiv2 is the fake of the Meshy OpenAPI v2 routes the Catalog
// uses (https://api.meshy.ai/openapi/v2): text-to-3d with its preview and
// refine modes. It is mounted at /meshy/openapi/v2 and shares the task store of
// the v1 fake, because Meshy lets a v1 task (remesh, retexture, rigging) take a
// v2 task as its input_task_id. This version is frozen once released.
//
// Answers the committed samples cannot hold are constructed from the official
// text-to-3d documentation (checked 2026-11-08) and named in the endpoint
// notes: ScenarioForbidden answers the documented 403; ScenarioTaskCanceled
// ends a task CANCELED; X-Fake-Task-Error selects a documented task_error type;
// PENDING carries started_at/finished_at 0 and preceding_tasks; a refine with
// enable_pbr=true carries metallic/normal/roughness; a preview OBJ has no MTL.
// geometry_resolution and the deprecated ultra_mode are not echoed: the Adapter
// does not read them.
package meshyopenapiv2

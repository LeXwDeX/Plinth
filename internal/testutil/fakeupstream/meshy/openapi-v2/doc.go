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
// does not read them. The two 429s follow https://docs.meshy.ai/en/api/rate-limits
// (read 2026-11-24): ScenarioRateLimit answers "Rate limit exceeded." with
// Retry-After, ScenarioConcurrencyLimit answers a create with a message
// starting "NoMorePendingTasks" and no Retry-After (#504).
//
// The create is checked as the page read on 2026-11-24 documents it (#542,
// meshy/core/validate.go): a preview needs a prompt of at most 800 characters,
// a refine a preview_task_id that names a succeeded preview of the account
// (anything else is 400), geometry_resolution 2k needs meshy-7.1, latest or
// meshy-7 and 4k needs meshy-7.1 or latest, and the model, mode and format enums
// are enforced. consumed_credits follows the Pricing page (meshy/core/
// credits.go); a Standard task carries no geometry_resolution. The refusal texts
// are constructed.
package meshyopenapiv2

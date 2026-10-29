// Package meshyopenapiv2 is the fake of the Meshy OpenAPI v2 routes the Catalog
// uses (https://api.meshy.ai/openapi/v2): text-to-3d with its preview and
// refine modes. It is mounted at /meshy/openapi/v2 and shares the task store of
// the v1 fake, because Meshy lets a v1 task (remesh, retexture, rigging) take a
// v2 task as its input_task_id. This version is frozen once released.
package meshyopenapiv2

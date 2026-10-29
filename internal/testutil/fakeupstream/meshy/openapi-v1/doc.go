// Package meshyopenapiv1 is the fake of the Meshy OpenAPI v1 routes the Catalog
// uses (https://api.meshy.ai/openapi/v1): balance, text-to-image,
// image-to-image, image-to-3d, multi-image-to-3d, remesh, convert, retexture,
// rigging, animations, uv-unwrap, resize, print/analyze, print/multi-color and
// text-to-motion. It is mounted at /meshy/openapi/v1; a Plinth stack points
// meshy_base_url at <fake>/meshy because the adapter appends openapi/<version>.
// This version is frozen once released: a new Meshy route version gets its own
// sibling package and never edits this one.
package meshyopenapiv1

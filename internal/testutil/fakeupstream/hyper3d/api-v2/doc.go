// Package hyper3dapiv2 is the fake of the Hyper3D Rodin API version the
// Catalog's hyper3d_base_url points at (https://api.hyper3d.com/api/v2): rodin,
// bang, rodin_texture_only, status, download and check_balance. It is mounted at
// /hyper3d/api/v2. A rejected submission is HTTP 201 with a documented error
// code in the body; the error bodies without a committed wire sample are
// constructed from https://docs.hyper3d.ai/en/api-specification/errors (checked
// 2026-11-08). A Gen-2.5 submission's receipt follows the page's price table and
// the Extreme-High tier lists its extra mesh; the one paid game-character run of
// 2026-11-13 (#294 follow-up) and what the fixed files do not reproduce of it
// are in the /rodin endpoint note. This version is frozen once released: a new
// Rodin API version gets its own sibling package and never edits this one.
package hyper3dapiv2

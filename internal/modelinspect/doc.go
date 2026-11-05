// Package modelinspect parses binary glTF (GLB) assets, reports geometric and
// material facts about them, and renders views without a GPU.
//
// It is meant for read-only Agent tools that need to see and reason about 3D
// models: Inspect returns a JSON-ready Report, Render produces an image.NRGBA.
// Parse is the only entry point that reads the container; it validates the
// declared structure eagerly and decodes accessor data lazily, so a large model
// does not need a second full copy of the file in memory.
//
// It decodes EXT_meshopt_compression views in pure Go and accepts
// KHR_mesh_quantization and KHR_texture_transform. It deliberately does not
// read external buffers or images, does not implement Draco (reported as a
// CompressionError), KHR_meshopt_compression, basisu, sparse accessors or
// animation playback, and does not modify the model. Skinned meshes are
// posed by their joints in the stored scene pose, as glTF requires. It also
// defines no production limits: callers pass a Limits value and are
// responsible for the caps they choose; Model.RetainedBytes reports what a
// parsed model holds so a cache can charge it.
package modelinspect

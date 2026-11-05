// Package modelinspect parses binary glTF (GLB) assets, reports geometric and
// material facts about them, and renders views without a GPU.
//
// It is meant for read-only Agent tools that need to see and reason about 3D
// models: Inspect returns a JSON-ready Report, Render produces an image.NRGBA.
// Parse is the only entry point that reads the container; it validates the
// declared structure eagerly and decodes accessor data lazily, so a large model
// does not need a second full copy of the file in memory.
//
// The package deliberately does not read external buffers or images, does not
// implement Draco, meshopt, basisu, sparse accessors or animation playback, and
// does not modify the model. It also defines no production limits: callers pass
// a Limits value and are responsible for the caps they choose.
package modelinspect

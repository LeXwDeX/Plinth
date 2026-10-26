package tripo

import "github.com/LeXwDeX/Plinth/internal/generation"

// These identify protocol families, not a table of product versions, fees or
// parameter choices. Those remain in each frozen Offering declaration.
const (
	nativeMeshSegmentation generation.Capability = "mesh-segmentation"
	nativeMeshCompletion   generation.Capability = "mesh-completion"
)

func nativeGeometryCapability(capability generation.Capability) bool {
	return capability == generation.Remesh || capability == nativeMeshSegmentation || capability == nativeMeshCompletion
}

// Complete is a native continuation of Segment, never an arbitrary model edit.
// The exact permitted source Offerings remain declared in TaskOfferings; the
// kernel resolves their immutable successful public Tasks before this adapter.
func validateNativeGeometrySource(offering generation.Offering, source generation.Parameter) error {
	if offering.Capability == nativeMeshCompletion {
		if len(source.TaskCapabilities) != 1 || source.TaskCapabilities[0] != nativeMeshSegmentation {
			return nativeProcessingProtocolError()
		}
	}
	return nil
}

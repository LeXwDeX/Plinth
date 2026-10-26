package schema

// Gaussian Splat is an application-owned, versioned byte profile. These are
// application resource/numeric limits, not claims about every hosted API output.
const (
	ImageToSplat                     Capability = "image-to-splat"
	DeliveryProfileGaussianSplat32V1            = "gaussian-splat-32-v1"
	GaussianSplatRole                           = "gaussian-splat"
	GaussianSplatVariant                        = "splat"
	GaussianSplatMediaType                      = "application/vnd.art-ai.gaussian-splat"
	GaussianSplatMaxBytes            int64      = 32 << 20
	GaussianSplatRecordBytes                    = 32
	GaussianSplatMaxPosition                    = 1024.0
	GaussianSplatMinScale                       = 1.0 / 65536.0
	GaussianSplatMaxScale                       = 1024.0
)

// DeliveryProfileRole adds a role constraint only for representations whose
// identity depends on it. Historical container-only profiles retain semantics.
func DeliveryProfileRole(profile string) string {
	if profile == DeliveryProfileGaussianSplat32V1 {
		return GaussianSplatRole
	}
	return ""
}

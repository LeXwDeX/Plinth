// Package registry lists, explicitly, every fake upstream version. Adding an
// upstream API version means appending its entry here; older entries are
// never edited or removed (issue #96).
package registry

import (
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// Profiles are the alignment scripts, keyed "provider/api".
func Profiles() map[string]align.Profile {
	profiles := map[string]align.Profile{}
	for _, profile := range []align.Profile{
		tripov3.Profile(),
		openaiv1.Profile(),
		meshyopenapiv1.Profile(),
		meshyopenapiv2.Profile(),
		hyper3dapiv2.Profile(),
	} {
		profiles[profile.Provider+"/"+profile.API] = profile
	}
	return profiles
}

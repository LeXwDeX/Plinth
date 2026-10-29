// Package registry lists, explicitly, every fake upstream version. Adding an
// upstream API version means appending its entry here; older entries are
// never edited or removed (issue #96).
package registry

import (
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
	geminiimagesv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/gemini/images-v1"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
	jimengapiv3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/jimeng/api-v3"
	mattingapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/matting/api-v1"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
	openaiimagesv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/images-v1"
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
		openaiimagesv1.Profile(),
		geminiimagesv1.Profile(),
		jimengapiv3.Profile(),
		mattingapiv1.Profile(),
	} {
		profiles[profile.Provider+"/"+profile.API] = profile
	}
	return profiles
}

package registry

import (
	"fmt"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
	meshyopenapiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v1"
	meshyopenapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/meshy/openapi-v2"
	openaiv1 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/openai/v1"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// Versions is the explicit list of every fake version. Append a new upstream
// API version here; never edit or remove an existing entry (issue #96).
func Versions() []fakeupstream.Version {
	return []fakeupstream.Version{
		tripov3.Version(),
		openaiv1.Version(),
		meshyopenapiv1.Version(),
		meshyopenapiv2.Version(),
		hyper3dapiv2.Version(),
	}
}

// New builds a fake serving every registered version.
func New(config fakeupstream.Config) (*fakeupstream.Server, error) {
	return fakeupstream.New(config, Versions()...)
}

// Handler builds a fake serving only one "provider/api" version and returns
// its route prefix.
func Handler(version string) (http.Handler, string, error) {
	for _, candidate := range Versions() {
		if candidate.Provider+"/"+candidate.API == version {
			server, err := fakeupstream.New(fakeupstream.Config{QueuedPolls: 1, RunningPolls: 1}, candidate)
			return server, candidate.Prefix, err
		}
	}
	return nil, "", fmt.Errorf("registry: unknown version %q", version)
}

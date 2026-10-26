package app

import "regexp"

// sourceRevision is linked in at build time from the same GO_SOURCE_COMMIT that
// sets the image's org.opencontainers.image.revision label:
//
//	-ldflags "-X github.com/LeXwDeX/Plinth/internal/app.sourceRevision=<sha>"
var sourceRevision string

const unknownRevision = "unknown"

var fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

// runtimeRevision is the exact commit this binary was built from, or
// "unknown" for local and unstamped builds. It never falls back to a file that
// an ordinary release would leave stale.
func runtimeRevision() string {
	if fullRevision.MatchString(sourceRevision) {
		return sourceRevision
	}
	return unknownRevision
}

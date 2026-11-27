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

// releaseVersion is the release string scripts/release-env.py hands the build
// through the same build-argument channel as sourceRevision (#676): the name
// passed with --release-version, or `git describe --tags --always` of the
// released revision.
//
//	-ldflags "-X github.com/LeXwDeX/Plinth/internal/app.releaseVersion=<release>"
var releaseVersion string

var releaseName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// runtimeRelease is the linked release string, or empty for local and
// unstamped builds, which then show no release at all.
func runtimeRelease() string {
	if releaseName.MatchString(releaseVersion) {
		return releaseVersion
	}
	return ""
}

//go:build race

package local

// raceDetectorEnabled lets single-goroutine tests over hundreds of MiB skip
// the race build, where they add minutes and no concurrency coverage. The
// plain go test gate still runs them.
const raceDetectorEnabled = true

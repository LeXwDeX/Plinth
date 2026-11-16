package wirefixture

import (
	"fmt"
	"strings"
)

// Test-tooling switches for the upstream recorder (#220). The application has
// its own, runtime-configured recorder (UPSTREAM_CAPTURE_DIR, #233); these
// two-key switches stay for tools such as fake-upstream-align that record real
// calls into a directory outside the worktree.
const (
	EnvUpstreamRecordDir         = "ART_AI_UPSTREAM_RECORD_DIR"
	EnvUpstreamRecordAck         = "ART_AI_UPSTREAM_RECORD_ACK"
	EnvUpstreamRecordEnvironment = "ART_AI_UPSTREAM_RECORD_ENVIRONMENT"
)

// UpstreamRecorderFromEnv returns nil when recording is off (the default).
// It refuses (error) when only one of the directory and ACK switches is set,
// when the environment label is missing or production-like, or when the
// directory is unsafe.
func UpstreamRecorderFromEnv(getenv func(string) string, worktree string, defaults Metadata) (*UpstreamRecorder, error) {
	dir := strings.TrimSpace(getenv(EnvUpstreamRecordDir))
	ack := strings.TrimSpace(getenv(EnvUpstreamRecordAck))
	if dir == "" && ack == "" {
		return nil, nil
	}
	if dir == "" || ack != "1" {
		return nil, fmt.Errorf("%w: set both %s and %s=1", ErrUpstreamRecordRefused, EnvUpstreamRecordDir, EnvUpstreamRecordAck)
	}
	if label := strings.TrimSpace(getenv(EnvUpstreamRecordEnvironment)); label != "" {
		defaults.Environment = label
	}
	return NewUpstreamRecorder(UpstreamRecordConfig{Dir: dir, Worktree: worktree, Defaults: defaults})
}

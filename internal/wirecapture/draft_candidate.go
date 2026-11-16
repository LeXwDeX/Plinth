package wirecapture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DraftCandidate is the result of WriteDraftCandidate.
type DraftCandidate struct {
	Candidate string
	Policy    string
	Meta      string
	Exchanges int
}

// WriteDraftCandidate renumbers exchanges, derives the replay policy and
// writes <name>.candidate.json, <name>.policy.json and <name>.meta.json into
// out (an existing 0700 directory outside the worktree) with the same
// layout as committed samples. The result is a draft: review it before
// committing and before using it to align a fake.
func WriteDraftCandidate(worktree, out, name, offering string, exchanges []Exchange) (DraftCandidate, error) {
	if !identifierRE.MatchString(name) || strings.Contains(name, "..") {
		return DraftCandidate{}, fmt.Errorf("%w: draft name", ErrUnsafePublicValue)
	}
	if len(exchanges) == 0 {
		return DraftCandidate{}, fmt.Errorf("%w: no recorded exchanges", ErrInvalidExchange)
	}
	draft := SortedExchanges(exchanges)
	for i := range draft {
		draft[i].Sequence = i + 1
	}
	policy, err := DraftReplayPolicy(draft)
	if err != nil {
		return DraftCandidate{}, err
	}
	result := DraftCandidate{
		Candidate: filepath.Join(out, name+".candidate.json"),
		Policy:    filepath.Join(out, name+".policy.json"),
		Meta:      filepath.Join(out, name+".meta.json"),
		Exchanges: len(draft),
	}
	for _, path := range []string{result.Policy, result.Meta} {
		if _, err := os.Lstat(path); err == nil {
			return DraftCandidate{}, fmt.Errorf("%w: %s exists", ErrExistingCandidate, filepath.Base(path))
		}
	}
	if err := WriteCandidate(worktree, result.Candidate, draft, policy); err != nil {
		return DraftCandidate{}, err
	}
	written, err := os.ReadFile(result.Candidate)
	if err != nil {
		return DraftCandidate{}, err
	}
	sum := sha256.Sum256(written)
	policyJSON, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return DraftCandidate{}, err
	}
	meta := map[string]any{
		"version":          1,
		"scenario":         draft[0].Metadata.Scenario,
		"captured_at_utc":  draft[0].Metadata.CapturedAt.UTC().Format("2006-01-02"),
		"source_revision":  "unknown",
		"api_version":      draft[0].Metadata.APIVersion,
		"provenance":       string(draft[0].Metadata.Provenance),
		"transformation":   "Draft from the opt-in upstream recorder: prompts, long text, URLs and IDs are placeholders; query values, headers outside the replay set and media bytes are not stored.",
		"exchanges":        len(draft),
		"candidate_sha256": hex.EncodeToString(sum[:]),
	}
	if offering != "" {
		if !identifierRE.MatchString(offering) {
			return DraftCandidate{}, fmt.Errorf("%w: offering", ErrUnsafePublicValue)
		}
		meta["offering"] = offering
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return DraftCandidate{}, err
	}
	for path, data := range map[string][]byte{result.Policy: policyJSON, result.Meta: metaJSON} {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
		if err != nil {
			return DraftCandidate{}, err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			_ = file.Close()
			return DraftCandidate{}, err
		}
		if err := file.Close(); err != nil {
			return DraftCandidate{}, err
		}
	}
	return result, nil
}

package diagbundle

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/LeXwDeX/Plinth/internal/wirecapture"
)

// The capture records (#233) are the one category the export never rewrites.
// They were reduced to structure when the service recorded them, and a record
// is only evidence while it is what the recorder wrote: a changed record is not
// a sample any more. So the export reads each file as the recorder's own reader
// does (a 0600 regular file, the current format, no unknown field), ships the
// bytes unchanged, and leaves the judgement to the final privacy scan, which
// refuses the whole bundle on any finding (see verifyMembers).

// recordStampLayout is the UTC time the recorder starts every file name with.
const recordStampLayout = "20060102T150405.000000000Z"

type captureResult struct {
	status    string
	reason    string
	members   []Member
	entries   int
	available int
	truncated bool
	bytes     int64
	dropped   map[string]int
}

// collectCaptures reads the records of the time range, newest first, until the
// budget is full. The members come back oldest first.
func collectCaptures(ctx context.Context, sources Sources, since, until time.Time, budget int64) (captureResult, error) {
	result := captureResult{dropped: map[string]int{}}
	if sources.CaptureDirectory == "" {
		result.status, result.reason = StatusUnavailable, ReasonNotConfigured
		return result, nil
	}
	names, err := wirecapture.ListUpstreamRecords(sources.CaptureDirectory)
	if err != nil {
		result.status, result.reason = StatusUnavailable, directoryReason(err)
		return result, nil
	}
	var inRange []string
	for _, name := range names {
		if len(name) < len(recordStampLayout) {
			result.dropped["unreadable"]++
			continue
		}
		at, err := time.Parse(recordStampLayout, name[:len(recordStampLayout)])
		if err != nil {
			result.dropped["unreadable"]++
			continue
		}
		if !at.Before(since) && !at.After(until) {
			inRange = append(inRange, name)
		}
	}
	result.available = len(inRange)
	var kept []Member
	for i := len(inRange) - 1; i >= 0; i-- {
		if i%64 == 0 && ctx.Err() != nil {
			return result, ctx.Err()
		}
		raw, reason := readCaptureFile(filepath.Join(sources.CaptureDirectory, inRange[i]), inRange[i])
		if reason != "" {
			result.dropped[reason]++
			continue
		}
		if result.bytes+int64(len(raw)) > budget {
			result.truncated = true
			break
		}
		kept = append(kept, Member{Path: "captures/" + inRange[i], Category: CategoryCaptures, Data: raw})
		result.bytes += int64(len(raw))
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	result.members, result.entries = kept, len(kept)
	result.status = StatusIncluded
	if result.entries == 0 {
		result.status = StatusEmpty
	}
	if len(result.dropped) == 0 {
		result.dropped = nil
	}
	return result, nil
}

// readCaptureFile returns the bytes of a record the recorder's reader accepts,
// or the reason it is left out.
func readCaptureFile(path, name string) ([]byte, string) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, "unreadable"
	}
	if info.Size() > maxCaptureFileBytes {
		return nil, "oversize"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "unreadable"
	}
	// One JSON document and nothing after it, so what is shipped is what the
	// reader (and later the scan) parsed.
	if _, err := wirecapture.ParseUpstreamRecord(name, raw); err != nil || !json.Valid(bytes.TrimSpace(raw)) {
		return nil, "unreadable"
	}
	return raw, ""
}

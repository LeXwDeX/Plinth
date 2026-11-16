package diagbundle

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// Log lines are the free-text category: the service has already redacted
// every attribute it logs (internal/logging), and the export scrubs each line a
// second time before the independent scan judges it. A line that is not a JSON
// object with a time and a level, that is longer than the service ever writes,
// or that the scan still objects to after scrubbing is dropped and counted by
// reason in the manifest; nothing of it is shipped.

var levelRE = regexp.MustCompile(`^(?:DEBUG|INFO|WARN|ERROR)(?:[+-]\d+)?$`)

type logResult struct {
	status    string
	reason    string
	data      []byte
	entries   int
	available int
	truncated bool
	dropped   map[string]int
}

// collectLogs reads the lines of the time range from the log directory and
// returns the newest ones that fit the budget, scrubbed.
func collectLogs(ctx context.Context, sources Sources, since, until time.Time, budget int64, scrubber *Scrubber) (logResult, error) {
	result := logResult{dropped: map[string]int{}}
	if sources.LogDirectory == "" {
		result.status, result.reason = StatusUnavailable, ReasonNotConfigured
		return result, nil
	}
	fileName := sources.LogFileName
	if fileName == "" {
		fileName = logging.FileName
	}
	files, err := logging.ListLogFiles(sources.LogDirectory, fileName)
	if err != nil {
		result.status, result.reason = StatusUnavailable, directoryReason(err)
		return result, nil
	}
	// Raw lines are kept only up to twice the budget: scrubbing shrinks most
	// lines and drops a few, and the newest are the ones worth having.
	ring := &lineRing{limit: max(2*budget, 1<<20)}
	for _, file := range files {
		if !file.Rotated.IsZero() && file.Rotated.Before(since) {
			continue // closed before the range began
		}
		if err := readLogFile(ctx, filepath.Join(sources.LogDirectory, file.Name), since, until, ring, &result); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.dropped["unreadable_file"]++
		}
	}
	retained := ring.retained()
	// Identities first: a user ID that shows up in free text before it shows up
	// under its key is still replaced.
	for _, raw := range retained {
		var object map[string]any
		if decodeLine(raw, &object) {
			scrubber.Collect("", object)
		}
	}
	var kept [][]byte
	var used int64
	for i := len(retained) - 1; i >= 0; i-- {
		if i%512 == 0 && ctx.Err() != nil {
			return result, ctx.Err()
		}
		line, reason := scrubLogLine(scrubber, retained[i])
		if reason != "" {
			result.dropped[reason]++
			continue
		}
		if used+int64(len(line))+1 > budget {
			result.truncated = true
			break
		}
		kept = append(kept, line)
		used += int64(len(line)) + 1
	}
	if ring.evicted {
		result.truncated = true
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	var data bytes.Buffer
	for _, line := range kept {
		data.Write(line)
		data.WriteByte('\n')
	}
	result.data, result.entries = data.Bytes(), len(kept)
	result.status = StatusIncluded
	if result.entries == 0 {
		result.status = StatusEmpty
	}
	if len(result.dropped) == 0 {
		result.dropped = nil
	}
	return result, nil
}

// readLogFile adds the lines of one file that fall in the range to the ring.
func readLogFile(ctx context.Context, path string, since, until time.Time, ring *lineRing, result *logResult) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, maxLogLineBytes)
	for count := 0; ; count++ {
		if count%1024 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Longer than any line the service writes: skip to the next one.
			result.dropped["too_long"]++
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = reader.ReadSlice('\n')
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if at, ok := lineTime(line); !ok {
				result.dropped["unparsable"]++
			} else if !at.Before(since) && !at.After(until) {
				result.available++
				ring.push(bytes.Clone(line))
			}
		}
		if err != nil {
			return nil // EOF
		}
	}
}

var timePrefix = []byte(`{"time":"`)

// lineTime reads the time of a log line: the service writes it first, so the
// common case is a prefix check; anything else is decoded.
func lineTime(line []byte) (time.Time, bool) {
	if rest, found := bytes.CutPrefix(line, timePrefix); found {
		if end := bytes.IndexByte(rest, '"'); end > 0 && end < 64 {
			if at, err := time.Parse(time.RFC3339Nano, string(rest[:end])); err == nil {
				return at.UTC(), true
			}
		}
	}
	var probe struct {
		Time string `json:"time"`
	}
	if json.Unmarshal(line, &probe) != nil || probe.Time == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, probe.Time)
	return at.UTC(), err == nil
}

// decodeLine decodes one JSON object with nothing after it.
func decodeLine(raw []byte, object *map[string]any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(object) == nil && *object != nil && !decoder.More()
}

// scrubLogLine rewrites one raw log line, or says why it cannot be shipped.
func scrubLogLine(scrubber *Scrubber, raw []byte) ([]byte, string) {
	var object map[string]any
	if !decodeLine(raw, &object) {
		return nil, "unparsable"
	}
	timeText, _ := object["time"].(string)
	at, err := time.Parse(time.RFC3339Nano, timeText)
	level, _ := object["level"].(string)
	if err != nil || !levelRE.MatchString(level) {
		return nil, "unparsable"
	}
	message, hasMessage := object["msg"].(string)
	delete(object, "time")
	delete(object, "level")
	if hasMessage {
		delete(object, "msg")
		message = scrubber.Text(message)
	}
	scrubbed, _ := scrubber.Value("", object).(map[string]any)
	var line bytes.Buffer
	line.WriteString(`{"time":`)
	line.Write(marshalPlain(formatTimestamp(at)))
	line.WriteString(`,"level":`)
	line.Write(marshalPlain(level))
	if hasMessage {
		line.WriteString(`,"msg":`)
		line.Write(marshalPlain(message))
	}
	keys := make([]string, 0, len(scrubbed))
	for key := range scrubbed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		line.WriteByte(',')
		line.Write(marshalPlain(key))
		line.WriteByte(':')
		line.Write(marshalPlain(scrubbed[key]))
	}
	line.WriteByte('}')
	if findings := privacyscan.ScanJSON("log", line.Bytes()); len(findings) > 0 {
		return nil, firstRule(findings)
	}
	return line.Bytes(), ""
}

// marshalPlain encodes a value as JSON without escaping <, > and &, so the text
// of a message stays readable.
func marshalPlain(value any) []byte {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return []byte(`null`)
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// firstRule names the finding to count a dropped item under: the first rule in
// alphabetical order, so the count does not depend on the order of the scan.
func firstRule(findings []privacyscan.Finding) string {
	first := findings[0].Rule
	for _, finding := range findings[1:] {
		if finding.Rule < first {
			first = finding.Rule
		}
	}
	return first
}

// lineRing keeps the newest raw lines within a byte limit.
type lineRing struct {
	lines   [][]byte
	head    int
	bytes   int64
	limit   int64
	evicted bool
}

func (r *lineRing) push(line []byte) {
	r.lines = append(r.lines, line)
	r.bytes += int64(len(line))
	for r.bytes > r.limit && r.head < len(r.lines)-1 {
		r.bytes -= int64(len(r.lines[r.head]))
		r.lines[r.head] = nil
		r.head++
		r.evicted = true
	}
	if r.head > 4096 && r.head > len(r.lines)/2 {
		r.lines = append([][]byte(nil), r.lines[r.head:]...)
		r.head = 0
	}
}

func (r *lineRing) retained() [][]byte { return r.lines[r.head:] }

// directoryReason maps a directory error to the fixed reason of the manifest.
func directoryReason(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return ReasonNotFound
	}
	return ReasonUnreadable
}

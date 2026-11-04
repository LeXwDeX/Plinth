package openaicompatible

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// streamLines reads the SSE body line by line like bufio.Scanner, but a line
// longer than max does not end the stream: its bytes are discarded and the
// line is reported as oversized. Only its head is kept, enough to tell a
// picture the model drew itself (delta.images) from anything else.
type streamLines struct {
	reader    *bufio.Reader
	max       int
	line      []byte
	head      []byte
	oversized bool
	err       error
}

// oversizedHeadBytes is how much of an oversized line is kept to classify it.
const oversizedHeadBytes = 512

func newStreamLines(body io.Reader, max int) *streamLines {
	return &streamLines{reader: bufio.NewReaderSize(body, 64*1024), max: max}
}

// Scan advances to the next line; false at the end of the body or on a read error.
func (lines *streamLines) Scan() bool {
	lines.line, lines.head, lines.oversized = lines.line[:0], lines.head[:0], false
	for {
		fragment, err := lines.reader.ReadSlice('\n')
		if !lines.oversized {
			if len(lines.line)+len(fragment) > lines.max {
				lines.oversized = true
				lines.head = append(lines.head, lines.line[:min(len(lines.line), oversizedHeadBytes)]...)
				if room := oversizedHeadBytes - len(lines.head); room > 0 {
					lines.head = append(lines.head, fragment[:min(len(fragment), room)]...)
				}
				lines.line = lines.line[:0]
			} else {
				lines.line = append(lines.line, fragment...)
			}
		}
		switch {
		case err == nil:
			return true
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return len(lines.line) > 0 || lines.oversized
		default:
			lines.err = err
			return false
		}
	}
}

// Text is the current line without its line ending; empty for an oversized line.
func (lines *streamLines) Text() string {
	return string(bytes.TrimRight(lines.line, "\r\n"))
}

// Oversized reports whether the current line was longer than max.
func (lines *streamLines) Oversized() bool { return lines.oversized }

// OversizedImage reports whether the oversized line is a stream chunk that
// carries a picture the model drew itself.
func (lines *streamLines) OversizedImage() bool {
	return lines.oversized && bytes.HasPrefix(bytes.TrimSpace(lines.head), []byte("data:")) && bytes.Contains(lines.head, []byte(`"images"`))
}

// Err is the read error that ended the stream, if any.
func (lines *streamLines) Err() error { return lines.err }

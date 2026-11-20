// Package upstreamhttp holds the small HTTP helpers the chat model Adapters
// share (#401): reading a server-sent-event body line by line, and telling the
// class of a refused request from its status and body. The Adapters own their
// request and response shapes; nothing here knows a wire protocol.
package upstreamhttp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// Lines reads the SSE body line by line like bufio.Scanner, but a line longer
// than max does not end the stream: its bytes are discarded and the line is
// reported as oversized. Only its head is kept, enough for the caller to tell
// what the line was (a picture the model drew itself, delta.images, on Chat
// Completions) without holding it.
type Lines struct {
	reader    *bufio.Reader
	max       int
	line      []byte
	head      []byte
	oversized bool
	err       error
}

// oversizedHeadBytes is how much of an oversized line is kept to classify it.
const oversizedHeadBytes = 512

// NewLines reads body line by line; a line over max bytes is oversized.
func NewLines(body io.Reader, max int) *Lines {
	return &Lines{reader: bufio.NewReaderSize(body, 64*1024), max: max}
}

// Scan advances to the next line; false at the end of the body or on a read error.
func (lines *Lines) Scan() bool {
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
func (lines *Lines) Text() string {
	return string(bytes.TrimRight(lines.line, "\r\n"))
}

// Oversized reports whether the current line was longer than max.
func (lines *Lines) Oversized() bool { return lines.oversized }

// Head is the first bytes of an oversized line; nil for any other line. The
// slice is valid until the next Scan.
func (lines *Lines) Head() []byte {
	if !lines.oversized {
		return nil
	}
	return lines.head
}

// Err is the read error that ended the stream, if any.
func (lines *Lines) Err() error { return lines.err }

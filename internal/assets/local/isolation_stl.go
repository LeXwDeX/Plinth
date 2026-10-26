package local

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	binarySTLHeaderBytes  = 84
	binarySTLRecordBytes  = 50
	binarySTLBatchRecords = 1024
	// An ASCII STL line holds one keyword and at most three numbers, or a
	// solid name. Longer lines are not STL text.
	maxASCIISTLLineBytes = 64 << 10
)

var errNotSTL = errors.New("内容不是 STL 网格")

func isSTLFormat(variant, mediaType string) bool {
	variant = strings.ToLower(strings.TrimSpace(variant))
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	return variant == "stl" || strings.HasSuffix(variant, ".stl") ||
		mediaType == "model/stl" || mediaType == "application/sla" || mediaType == "application/vnd.ms-pki.stl"
}

func binarySTLSizeMatches(header []byte, size int64) bool {
	return len(header) >= binarySTLHeaderBytes && size >= binarySTLHeaderBytes &&
		uint64(size) == binarySTLHeaderBytes+uint64(binary.LittleEndian.Uint32(header[80:84]))*binarySTLRecordBytes
}

func inspectSTLAddresses(reader *bufio.Reader, size int64) error {
	header, err := reader.Peek(binarySTLHeaderBytes)
	if err != nil && err != io.EOF {
		return err
	}
	if !binarySTLSizeMatches(header, size) {
		// ASCII STL and incomplete or extended binary layouts retain the full
		// address check. A filename alone never exempts arbitrary bytes.
		return scanExternalAddresses(reader)
	}
	return inspectBinarySTL(reader, size)
}

// inspectSTL establishes that a delivered STL slot holds an STL mesh, not
// merely bytes without addresses: either the exact binary layout (header,
// triangle count and length agree) or ASCII solid/facet/endsolid structure
// with finite numbers. Both forms keep the external-address checks. Archive
// members named *.stl keep the address-only contract of inspectSTLAddresses.
func inspectSTL(reader io.Reader, size int64) error {
	if size <= 0 {
		return errNotSTL
	}
	buffered := bufio.NewReader(reader)
	header, err := buffered.Peek(binarySTLHeaderBytes)
	if err != nil && err != io.EOF {
		return err
	}
	if binarySTLSizeMatches(header, size) {
		return inspectBinarySTL(buffered, size)
	}
	addresses := &addressSink{scanner: addressScanner{initial: externalAddress, continuation: continuedExternalAddress}}
	// The structure pass reads every byte to EOF before it can succeed, so
	// the address scan over the same bytes is complete whenever it matters.
	if err := inspectASCIISTL(io.TeeReader(buffered, addresses)); err != nil {
		return err
	}
	if addresses.found {
		return errors.New("模型内容含外部地址")
	}
	return nil
}

type addressSink struct {
	scanner addressScanner
	found   bool
}

func (sink *addressSink) Write(data []byte) (int, error) {
	if !sink.found && len(data) > 0 && sink.scanner.contains(data) {
		sink.found = true
	}
	return len(data), nil
}

// asciiSTLTokens yields whitespace-separated tokens across lines; a solid
// name is the remainder of its line and is skipped as a whole.
type asciiSTLTokens struct {
	reader *bufio.Reader
	fields [][]byte
	eof    bool
}

func (t *asciiSTLTokens) next() ([]byte, error) {
	for len(t.fields) == 0 {
		if t.eof {
			return nil, io.EOF
		}
		line, err := t.reader.ReadSlice('\n')
		switch {
		case err == io.EOF:
			t.eof = true
		case errors.Is(err, bufio.ErrBufferFull):
			return nil, errors.New("ASCII STL 行过长")
		case err != nil:
			return nil, err
		}
		if bytes.IndexByte(line, 0) >= 0 {
			return nil, errNotSTL
		}
		t.fields = bytes.Fields(line)
	}
	token := t.fields[0]
	t.fields = t.fields[1:]
	return token, nil
}

func (t *asciiSTLTokens) skipLine() { t.fields = nil }

func (t *asciiSTLTokens) expect(keyword string) error {
	token, err := t.next()
	if err == io.EOF {
		return errNotSTL
	}
	if err != nil {
		return err
	}
	if !bytes.EqualFold(token, []byte(keyword)) {
		return errNotSTL
	}
	return nil
}

func (t *asciiSTLTokens) numbers(count int) error {
	for range count {
		token, err := t.next()
		if err == io.EOF {
			return errNotSTL
		}
		if err != nil {
			return err
		}
		// Binary STL stores float32; the same finite range applies to text.
		value, err := strconv.ParseFloat(string(token), 32)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("STL 几何数据含非有限数值")
		}
	}
	return nil
}

// inspectASCIISTL accepts one or more solids, each a sequence of facets with
// a normal and a three-vertex outer loop. Keywords are case-insensitive; the
// file must contain at least one facet and nothing after the last endsolid.
func inspectASCIISTL(reader io.Reader) error {
	tokens := &asciiSTLTokens{reader: bufio.NewReaderSize(reader, maxASCIISTLLineBytes)}
	solids, facets := 0, 0
	for {
		token, err := tokens.next()
		if err == io.EOF {
			if solids == 0 || facets == 0 {
				return errNotSTL
			}
			return nil
		}
		if err != nil {
			return err
		}
		if !bytes.EqualFold(token, []byte("solid")) {
			return errNotSTL
		}
		tokens.skipLine()
		solids++
		for {
			token, err = tokens.next()
			if err == io.EOF {
				return errNotSTL
			}
			if err != nil {
				return err
			}
			if bytes.EqualFold(token, []byte("endsolid")) {
				tokens.skipLine()
				break
			}
			if !bytes.EqualFold(token, []byte("facet")) {
				return errNotSTL
			}
			if err = tokens.expect("normal"); err != nil {
				return err
			}
			if err = tokens.numbers(3); err != nil {
				return err
			}
			if err = tokens.expect("outer"); err != nil {
				return err
			}
			if err = tokens.expect("loop"); err != nil {
				return err
			}
			for range 3 {
				if err = tokens.expect("vertex"); err != nil {
					return err
				}
				if err = tokens.numbers(3); err != nil {
					return err
				}
			}
			if err = tokens.expect("endloop"); err != nil {
				return err
			}
			if err = tokens.expect("endfacet"); err != nil {
				return err
			}
			facets++
		}
	}
}

func inspectBinarySTL(reader io.Reader, size int64) error {
	var header [binarySTLHeaderBytes]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	if !binarySTLSizeMatches(header[:], size) {
		return errors.New("STL 三角形计数与文件大小不一致")
	}
	if containsExternalAddress(header[:80]) {
		return errors.New("STL 头部含外部地址")
	}
	// Binary STL has an 80-byte text header, a count, and fixed numeric
	// records. Numeric bits cannot name a network resource. Absolute HTTP(S)
	// addresses still cannot pass anywhere, including encoded numeric bytes.
	scanner := addressScanner{initial: absoluteAddress, continuation: absoluteAddress}
	if scanner.contains(header[:]) {
		return errors.New("STL 内容含外部地址")
	}
	remaining := binary.LittleEndian.Uint32(header[80:84])
	var batch [binarySTLBatchRecords * binarySTLRecordBytes]byte
	for remaining > 0 {
		records := min(remaining, binarySTLBatchRecords)
		body := batch[:int(records)*binarySTLRecordBytes]
		if _, err := io.ReadFull(reader, body); err != nil {
			return err
		}
		if scanner.contains(body) {
			return errors.New("STL 内容含外部地址")
		}
		for offset := 0; offset < len(body); offset += binarySTLRecordBytes {
			for coordinate := 0; coordinate < 48; coordinate += 4 {
				value := float64(math.Float32frombits(binary.LittleEndian.Uint32(body[offset+coordinate : offset+coordinate+4])))
				if math.IsNaN(value) || math.IsInf(value, 0) {
					return errors.New("STL 几何数据含非有限数值")
				}
			}
		}
		remaining -= records
	}
	var remainder [1]byte
	if count, err := reader.Read(remainder[:]); count != 0 || err != io.EOF {
		if err != nil && err != io.EOF {
			return err
		}
		return errors.New("STL 三角形记录之外含附加内容")
	}
	return nil
}

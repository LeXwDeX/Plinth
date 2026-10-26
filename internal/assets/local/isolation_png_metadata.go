package local

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// PNG keywords and palette names share the printable Latin-1 rules in
// https://www.w3.org/TR/png-3/#11keywords. The separator is not part of the name.
func validPNGKeyword(name []byte) bool {
	if len(name) < 1 || len(name) > 79 || name[0] == ' ' || name[len(name)-1] == ' ' {
		return false
	}
	previousSpace := false
	for _, value := range name {
		if value < 32 || (value >= 127 && value <= 160) || (value == ' ' && previousSpace) {
			return false
		}
		previousSpace = value == ' '
	}
	return true
}

// Upper bound on language tag subtags, applied before Split.
const maxPNGLanguageSubtags = 10000

// Verify RFC 5646 section 2.1 syntax, without an online language registry.
// Each subtag has at most eight ASCII characters. maxPNGLanguageSubtags
// limits subtag storage before Split or duplicate detection.
func validPNGLanguageTag(tag []byte) bool {
	if len(tag) == 0 {
		return true
	}
	if len(tag) > maxPNGLanguageSubtags*9 || bytes.Count(tag, []byte{'-'}) >= maxPNGLanguageSubtags {
		return false
	}
	for _, value := range tag {
		if (value < 'A' || value > 'Z') && (value < 'a' || value > 'z') && (value < '0' || value > '9') && value != '-' {
			return false
		}
	}
	lower := strings.ToLower(string(tag))
	parts := strings.Split(lower, "-")
	for _, part := range parts {
		if len(part) == 0 || len(part) > 8 || !pngLanguageCharacters(part, false, false) {
			return false
		}
	}
	// RFC 5646's fixed grandfathered forms that do not match langtag.
	switch lower {
	case "en-gb-oed", "i-ami", "i-bnn", "i-default", "i-enochian", "i-hak", "i-klingon", "i-lux", "i-mingo",
		"i-navajo", "i-pwn", "i-tao", "i-tay", "i-tsu", "sgn-be-fr", "sgn-be-nl", "sgn-ch-de":
		return true
	}
	if parts[0] == "x" {
		return len(parts) > 1
	}
	if len(parts[0]) < 2 || !pngLanguageCharacters(parts[0], true, false) {
		return false
	}
	index := 1
	if len(parts[0]) <= 3 {
		for count := 0; count < 3 && index < len(parts) && len(parts[index]) == 3 && pngLanguageCharacters(parts[index], true, false); count++ {
			index++
		}
	}
	if index < len(parts) && len(parts[index]) == 4 && pngLanguageCharacters(parts[index], true, false) {
		index++
	}
	if index < len(parts) && ((len(parts[index]) == 2 && pngLanguageCharacters(parts[index], true, false)) ||
		(len(parts[index]) == 3 && pngLanguageCharacters(parts[index], false, true))) {
		index++
	}
	variants := make(map[string]bool)
	for index < len(parts) && (len(parts[index]) >= 5 || (len(parts[index]) == 4 && parts[index][0] >= '0' && parts[index][0] <= '9')) {
		if variants[parts[index]] {
			return false
		}
		variants[parts[index]] = true
		index++
	}
	var singletons [128]bool
	for index < len(parts) && len(parts[index]) == 1 && parts[index] != "x" {
		singleton := parts[index][0]
		if singletons[singleton] {
			return false
		}
		singletons[singleton] = true
		index++
		first := index
		for index < len(parts) && len(parts[index]) >= 2 {
			index++
		}
		if first == index {
			return false
		}
	}
	if index < len(parts) && parts[index] == "x" {
		return index+1 < len(parts)
	}
	return index == len(parts)
}

func pngLanguageCharacters(value string, lettersOnly, digitsOnly bool) bool {
	for index := 0; index < len(value); index++ {
		letter := value[index] >= 'a' && value[index] <= 'z'
		digit := value[index] >= '0' && value[index] <= '9'
		if (!letter && !digit) || (lettersOnly && !letter) || (digitsOnly && !digit) {
			return false
		}
	}
	return true
}

// sPLT entries are RGBA plus a big-endian frequency: 6 bytes at depth 8,
// 10 bytes at depth 16. Only this complete numeric array receives a waiver;
// the palette name is inspected from its own body origin.
func inspectPNGSuggestedPalette(payload []byte, frame rasterPNGFrame, names map[string]bool) (inspectionPlan, error) {
	nameEnd := bytes.IndexByte(payload, 0)
	if frame.seenPixels || nameEnd < 0 || !validPNGKeyword(payload[:nameEnd]) || len(payload)-nameEnd < 3 {
		return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 建议调色板名称或顺序无效"))
	}
	name := string(payload[:nameEnd])
	if names[name] {
		return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 建议调色板名称重复"))
	}
	width := 0
	switch payload[nameEnd+1] {
	case 8:
		width = 6
	case 16:
		width = 10
	}
	start := nameEnd + 2
	if width == 0 || len(payload)-start == 0 || (len(payload)-start)%width != 0 {
		return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 建议调色板条目无效"))
	}
	previous := uint16(65535)
	for offset := start; offset < len(payload); offset += width {
		frequency := binary.BigEndian.Uint16(payload[offset+width-2 : offset+width])
		if frequency > previous {
			return inspectionPlan{}, rejectInspection("png_invalid_structure", errors.New("PNG 建议调色板频率顺序无效"))
		}
		previous = frequency
	}
	names[name] = true
	return inspectionPlan{numeric: []inspectionSpan{{int64(start), int64(len(payload))}}}, nil
}

// Check UTF-8 while streaming decoded iTXt. At most three incomplete bytes
// survive a Read; no second metadata-sized allocation is needed.
type pngUTF8Reader struct {
	source  io.Reader
	partial [utf8.UTFMax]byte
	count   int
}

func (reader *pngUTF8Reader) Read(target []byte) (int, error) {
	count, err := reader.source.Read(target)
	data := target[:count]
	for len(data) > 0 {
		if reader.count > 0 {
			reader.partial[reader.count] = data[0]
			reader.count++
			data = data[1:]
			if !utf8.FullRune(reader.partial[:reader.count]) {
				continue
			}
			value, width := utf8.DecodeRune(reader.partial[:reader.count])
			if (value == utf8.RuneError && width == 1) || width != reader.count {
				return count, errors.New("invalid PNG international text encoding")
			}
			reader.count = 0
			continue
		}
		if data[0] < utf8.RuneSelf {
			data = data[1:]
			continue
		}
		if !utf8.FullRune(data) {
			reader.count = copy(reader.partial[:], data)
			break
		}
		value, width := utf8.DecodeRune(data)
		if value == utf8.RuneError && width == 1 {
			return count, errors.New("invalid PNG international text encoding")
		}
		data = data[width:]
	}
	if err == io.EOF && reader.count != 0 {
		return count, errors.New("incomplete PNG international text encoding")
	}
	return count, err
}

package schema

import (
	"strconv"
	"strings"
)

// SizeText is the one way a byte count is written (#327): decimal MB
// (1 MB = 1,000,000 bytes; KB of 1,000 bytes below 1 MB) with at most two
// decimals and no trailing zeros — in Schema limits shown on the page, in size
// refusals and in the Agent's tool results, which carry this text so the
// Agent quotes it instead of converting bytes itself. A size is rounded up and
// a limit down, so a file over its limit never reads as equal to it
// ("10.49 MB" over "10.48 MB"). src/lib/size-text.ts mirrors it.
func SizeText(bytes int64, roundUp bool) string {
	unit, scale := " MB", int64(1_000_000)
	if bytes < scale {
		unit, scale = " KB", 1_000
	}
	if bytes < 0 {
		bytes = 0
	}
	hundredths := bytes * 100 / scale
	if roundUp && bytes*100%scale != 0 {
		hundredths++
	}
	text := strconv.FormatInt(hundredths/100, 10)
	if fraction := hundredths % 100; fraction != 0 {
		text += "." + strings.TrimRight(strconv.FormatInt(100+fraction, 10)[1:], "0")
	}
	return text + unit
}

// Base64EncodedBytes is the length of bytes after standard padded base64
// encoding, the form inline image requests carry.
func Base64EncodedBytes(bytes int64) int64 {
	return (bytes + 2) / 3 * 4
}

// FillSizeRefusal fills a Schema size-refusal template: {size} with the
// refused size, {limit} with the limit and {label} with the input's label.
func FillSizeRefusal(template string, label string, size int64, limit int64) string {
	return strings.NewReplacer(
		"{label}", label,
		"{size}", SizeText(size, true),
		"{limit}", SizeText(limit, false),
	).Replace(template)
}

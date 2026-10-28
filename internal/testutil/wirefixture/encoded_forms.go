package wirefixture

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Encoded forms of values checkText already rejects in plain form (#90). They
// are decoded only to be checked; the decoded text is never stored or returned.

const (
	maxPercentRounds = 2  // %2531 -> %31 -> 1; deeper nesting is not decoded
	mixedBlobMinRun  = 80 // shorter than embeddedBlobRE, but only with base64-like character mix
	percentMaxBytes  = 64 << 10
)

var (
	percentEscapeRE = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)
	mixedBlobRE     = regexp.MustCompile(`[A-Za-z0-9+/_-]{80,}={0,2}`)
	// 0xC0A80009 (one 32-bit hex number) or dotted components in hex/octal such as 0xC0.0xA8.0.9 / 0300.0250.0.9.
	hexIntIPRE = regexp.MustCompile(`(?i)\b0x[0-9a-f]{8}\b`)
	dottedIPRE = regexp.MustCompile(`(?i)\b(?:0x[0-9a-f]{1,8}|\d{1,10})(?:\.(?:0x[0-9a-f]{1,8}|\d{1,10})){1,3}\b`)
	upperRE    = regexp.MustCompile(`[A-Z]`)
	lowerRE    = regexp.MustCompile(`[a-z]`)
	digitRE    = regexp.MustCompile(`[0-9]`)
)

// percentDecodedVariants returns the distinct texts obtained by decoding valid
// %XX escapes once and twice. Invalid escapes are left as they are, so "100%"
// and "50% off" yield no variant.
func percentDecodedVariants(text string) []string {
	if len(text) > percentMaxBytes || !percentEscapeRE.MatchString(text) {
		return nil
	}
	var out []string
	current := text
	for round := 0; round < maxPercentRounds; round++ {
		decoded := percentEscapeRE.ReplaceAllStringFunc(current, func(escape string) string {
			value, _ := strconv.ParseUint(escape[1:], 16, 8)
			return string([]byte{byte(value)})
		})
		if decoded == current {
			break
		}
		out = append(out, decoded)
		current = decoded
	}
	return out
}

// mixedBlob reports a long run that mixes upper case, lower case and digits,
// the shape of base64 or an opaque token. Long single-class runs (slugs, hex
// digests, repeated filler) stay below embeddedBlobRE's hard 200 limit.
func mixedBlob(text string) bool {
	for _, run := range mixedBlobRE.FindAllString(text, -1) {
		if upperRE.MatchString(run) && lowerRE.MatchString(run) && digitRE.MatchString(run) {
			return true
		}
	}
	return false
}

// legacyIPv4NonPublic reports IPv4 literals written as one hex number or with
// hex/octal components, which netip.ParseAddr refuses but resolvers accept.
// Plain integers are not handled here: outside a URL host they cannot be told
// from timestamps and identifiers, and a non-example.test URL is rejected anyway.
func legacyIPv4NonPublic(text string) bool {
	for _, raw := range hexIntIPRE.FindAllString(text, -1) {
		if value, err := strconv.ParseUint(raw[2:], 16, 32); err == nil && nonPublicAddr(uint32Addr(uint32(value))) {
			return true
		}
	}
	for _, raw := range dottedIPRE.FindAllString(text, -1) {
		parts := strings.Split(raw, ".")
		hasLegacy := false
		for _, part := range parts {
			if len(part) > 1 && (part[0] == '0') {
				hasLegacy = true
			}
		}
		if !hasLegacy {
			continue // plain dotted decimal is covered by ipv4RE
		}
		for _, base := range []int{0, 10} { // inet_aton (octal/hex) reading, then decimal with leading zeros
			if addr, ok := parseInetAton(parts, base); ok && nonPublicAddr(addr) {
				return true
			}
		}
	}
	return false
}

func parseInetAton(parts []string, base int) (netip.Addr, bool) {
	values := make([]uint64, len(parts))
	for i, part := range parts {
		value, err := strconv.ParseUint(part, base, 32)
		if err != nil {
			return netip.Addr{}, false
		}
		values[i] = value
	}
	var number uint64
	last := len(values) - 1
	for i := 0; i < last; i++ {
		if values[i] > 255 {
			return netip.Addr{}, false
		}
		number |= values[i] << (8 * (3 - uint(i)))
	}
	if values[last] >= 1<<(8*(4-uint(last))) {
		return netip.Addr{}, false
	}
	number |= values[last]
	return uint32Addr(uint32(number)), true
}

func uint32Addr(value uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

package local

import (
	"bytes"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

// Frozen independent pre-118 scanner oracle. This is deliberately not
// implemented through the production matcher or its decoding helpers.
// Source: isolation.go SHA256 cc5e58e814a0027e0971ce03deb20fe1982a3b30a15deb977e3d34a370ca1033.
const addressScanOldOverlap = 4096

// Network authorities include IP addresses and local hostnames. Require a URI
// boundary before // so an OBJ face such as 1//1 remains valid geometry.
const addressScanOldBoundary = `[^\p{L}\p{N}_./\\:+-]`
const addressScanOldRelative = `//(?:[\p{L}\p{N}._~!$&'()*+,;=:%-]+@)?(?:[\p{L}\p{N}][\p{L}\p{N}._-]*|\[[a-z0-9:.%_-]+\])(?::[0-9]+)?(?:[/?:#\s]|$)`

var addressScanOldExternal = regexp.MustCompile(`(?i)(https?://|(?:^|` + addressScanOldBoundary + `)` + addressScanOldRelative + `)`)
var addressScanOldContinued = regexp.MustCompile(`(?i)(https?://|` + addressScanOldBoundary + addressScanOldRelative + `)`)
var addressScanOldAbsolute = regexp.MustCompile(`(?i)https?://`)
var addressScanOldPercent = regexp.MustCompile(`(?i)%([0-9a-f]{2})`)
var addressScanOldEscape = regexp.MustCompile(`(?i)\\u([0-9a-f]{4})`)

type addressScanOldScanner struct {
	initial, continuation *regexp.Regexp
	previous              []byte
	seen                  int64
}

func (scanner *addressScanOldScanner) contains(data []byte) bool {
	window := make([]byte, 0, len(scanner.previous)+len(data))
	window = append(window, scanner.previous...)
	window = append(window, data...)
	pattern := scanner.initial
	// The overlap has already been checked with its preceding context. Its
	// truncated beginning is not a new URI boundary, including after decoding.
	if scanner.seen > int64(len(scanner.previous)) {
		pattern = scanner.continuation
	}
	scanner.seen += int64(len(data))
	scanner.previous = append(scanner.previous[:0], window[max(0, len(window)-addressScanOldOverlap):]...)
	return addressScanOldContains(window, pattern)
}

func addressScanOldContains(data []byte, pattern *regexp.Regexp) bool {
	if pattern.Match(data) {
		return true
	}
	if bytes.IndexByte(data, 0) >= 0 &&
		(addressScanOldEncoded(addressScanOldUTF16(data, true, pattern), pattern) ||
			addressScanOldEncoded(addressScanOldUTF16(data, false, pattern), pattern)) {
		return true
	}
	if bytes.IndexAny(data, `%\&`) < 0 {
		return false
	}
	return addressScanOldEncoded(string(data), pattern)
}

func addressScanOldEncoded(text string, pattern *regexp.Regexp) bool {
	for pass := 0; pass < 3; pass++ {
		if pattern.MatchString(text) {
			return true
		}
		decoded := html.UnescapeString(text)
		decoded = strings.ReplaceAll(decoded, `\/`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002f`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002F`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u003a`, `:`)
		decoded = strings.ReplaceAll(decoded, `\u003A`, `:`)
		decoded = addressScanOldEscape.ReplaceAllStringFunc(decoded, func(token string) string {
			code, err := strconv.ParseUint(token[2:], 16, 16)
			if err != nil || code < 0x20 || code > 0x7e {
				return token
			}
			return string(rune(code))
		})
		decoded = addressScanOldPercent.ReplaceAllStringFunc(decoded, func(token string) string {
			value, err := url.QueryUnescape(token)
			if err != nil {
				return token
			}
			return value
		})
		if decoded == text {
			return false
		}
		text = decoded
	}
	return pattern.MatchString(text)
}

func addressScanOldUTF16(data []byte, littleEndian bool, pattern *regexp.Regexp) string {
	var out strings.Builder
	for start := 0; start < 2; start++ {
		out.Reset()
		for offset := start; offset+1 < len(data); offset += 2 {
			first, second := data[offset], data[offset+1]
			if !littleEndian {
				first, second = second, first
			}
			if second == 0 && first >= 0x20 && first <= 0x7e {
				out.WriteByte(first)
			} else {
				out.WriteByte(' ')
			}
		}
		if addressScanOldEncoded(out.String(), pattern) {
			return out.String()
		}
	}
	return out.String()
}

type addressScanCase struct {
	name, text                    string
	external, continued, absolute bool
}

func addressScanCases() []addressScanCase {
	percent := "https%3A%2F%2Fprivate.example.test%2Fsynth"
	percent2 := strings.ReplaceAll(percent, "%", "%25")
	percent3 := strings.ReplaceAll(percent2, "%", "%25")
	return []addressScanCase{
		{"empty", "", false, false, false},
		{"plain", "finite model data with no address", false, false, false},
		{"literal_http", "http://private.example.test/synth", true, true, true},
		{"literal_https", "HTTPS://private.example.test/synth", true, true, true},
		{"relative_origin", "//localhost/synth", true, false, false},
		{"relative_ip", "//127.0.0.1:8080/synth", true, false, false},
		{"relative_ipv6", "//[::1]/synth", true, false, false},
		{"relative_boundary", " //localhost/synth", true, true, false},
		{"relative_userinfo", " //user@private.example.test:8080/synth", true, true, false},
		{"relative_utf8_host", "//模型.example/synth", true, false, false},
		{"unicode_letter_boundary", "模型//localhost/synth", false, false, false},
		{"unicode_punctuation_boundary", "。//localhost/synth", true, true, false},
		{"obj_face", "f 1//1 2//2 3//3", false, false, false},
		{"numeric_boundary", "1//localhost/synth", false, false, false},
		{"dot_boundary", "name.//localhost/synth", false, false, false},
		{"colon_boundary", "name://localhost/synth", false, false, false},
		{"ftp", "ftp://localhost/synth", false, false, false},
		{"percent_one", percent, true, true, true},
		{"percent_two", percent2, true, true, true},
		{"percent_three", percent3, true, true, true},
		{"percent_four", strings.ReplaceAll(percent3, "%", "%25"), false, false, false},
		{"relative_percent", "%2F%2Flocalhost%2Fsynth", true, false, false},
		{"json_slashes", `https:\/\/private.example.test\/synth`, true, true, true},
		{"json_unicode", `\u0068\u0074\u0074\u0070\u0073\u003a\u002f\u002fprivate.example.test\u002fsynth`, true, true, true},
		{"relative_unicode", `\u002f\u002flocalhost\u002fsynth`, true, false, false},
		{"html_decimal", "https:&#47;&#47;private.example.test/synth", true, true, true},
		{"html_hex", "https:&#x2f;&#X2F;private.example.test/synth", true, true, true},
		{"html_named", "https:&sol;&sol;private.example.test/synth", true, true, true},
		{"html_two", "https:&amp;#47;&amp;#47;private.example.test/synth", true, true, true},
		{"mixed", `https%3A%26%2347%3B\u002fprivate.example.test/synth`, true, true, true},
		{"invalid_percent", "http%ZZ:%2x%2ylocalhost", false, false, false},
		{"truncated_escape", `http\u002`, false, false, false},
	}
}

func addressScanPatterns() []struct {
	name            string
	current, oracle *regexp.Regexp
	expected        func(addressScanCase) bool
} {
	return []struct {
		name            string
		current, oracle *regexp.Regexp
		expected        func(addressScanCase) bool
	}{
		{"external", externalAddress, addressScanOldExternal, func(c addressScanCase) bool { return c.external }},
		{"continued", continuedExternalAddress, addressScanOldContinued, func(c addressScanCase) bool { return c.continued }},
		{"absolute", absoluteAddress, addressScanOldAbsolute, func(c addressScanCase) bool { return c.absolute }},
	}
}

func addressScanUTF16(text string, little bool, parity int) []byte {
	units := utf16.Encode([]rune(text))
	result := make([]byte, parity, parity+2*len(units))
	for _, value := range units {
		if little {
			result = append(result, byte(value), byte(value>>8))
		} else {
			result = append(result, byte(value>>8), byte(value))
		}
	}
	return result
}

func addressScanRepresentations(text string) []struct {
	name string
	data []byte
} {
	return []struct {
		name string
		data []byte
	}{
		{"raw", []byte(text)},
		{"binary_neighbors", append(append([]byte{0xff, 0, 1}, []byte(text)...), 0xa5, 0)},
		{"utf16_le_even", addressScanUTF16(text, true, 0)},
		{"utf16_le_odd", addressScanUTF16(text, true, 1)},
		{"utf16_be_even", addressScanUTF16(text, false, 0)},
		{"utf16_be_odd", addressScanUTF16(text, false, 1)},
	}
}

func TestAddressScanLiteralAndEncodedContracts(t *testing.T) {
	for _, c := range addressScanCases() {
		for _, pattern := range addressScanPatterns() {
			t.Run(c.name+"/"+pattern.name, func(t *testing.T) {
				expected := pattern.expected(c)
				if addressScanOldContains([]byte(c.text), pattern.oracle) != expected {
					t.Fatal("independent old spelling control disagrees with its declared contract")
				}
				if containsAddress([]byte(c.text), pattern.current) != expected {
					t.Fatal("current spelling check changed the declared contract")
				}
			})
		}
	}
}

func TestAddressScanFrozenProjectionOracle(t *testing.T) {
	for _, c := range addressScanCases() {
		for _, representation := range addressScanRepresentations(c.text) {
			for _, pattern := range addressScanPatterns() {
				t.Run(c.name+"/"+representation.name+"/"+pattern.name, func(t *testing.T) {
					expected := addressScanOldContains(representation.data, pattern.oracle)
					if containsAddress(representation.data, pattern.current) != expected {
						t.Fatal("address projection differs from independent pre-change oracle")
					}
				})
			}
		}
	}
}

func addressScanCompareStream(t *testing.T, data []byte, cuts []int, absolute bool) {
	t.Helper()
	current := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
	old := addressScanOldScanner{initial: addressScanOldExternal, continuation: addressScanOldContinued}
	if absolute {
		current.initial, current.continuation = absoluteAddress, absoluteAddress
		old.initial, old.continuation = addressScanOldAbsolute, addressScanOldAbsolute
	}
	start := 0
	for _, stop := range append(append([]int(nil), cuts...), len(data)) {
		if stop < start || stop > len(data) {
			t.Fatal("invalid independent stream fixture")
		}
		chunk := data[start:stop]
		expected := old.contains(chunk)
		if current.contains(chunk) != expected {
			t.Fatal("stream result differs from frozen oracle")
		}
		if current.seen != old.seen || !bytes.Equal(current.previous, old.previous) {
			t.Fatal("stream carry or origin state differs from frozen oracle")
		}
		start = stop
	}
}

func TestAddressScanFrozenStreamOracle(t *testing.T) {
	for _, c := range addressScanCases() {
		for _, representation := range addressScanRepresentations(c.text) {
			for _, absolute := range []bool{false, true} {
				name := "external"
				if absolute {
					name = "absolute"
				}
				t.Run(c.name+"/"+representation.name+"/"+name, func(t *testing.T) {
					// One-byte reads cover every spelling boundary. Also test
					// complete windows and each possible single split.
					cuts := make([]int, len(representation.data))
					for i := range cuts {
						cuts[i] = i
					}
					addressScanCompareStream(t, representation.data, cuts, absolute)
					for split := 0; split <= len(representation.data); split++ {
						addressScanCompareStream(t, representation.data, []int{split}, absolute)
					}
				})
			}
		}
	}
}

func TestAddressScanCarryAndContinuationOracle(t *testing.T) {
	for _, text := range []string{
		"https://private.example.test/synth",
		"https%253A%252F%252Fprivate.example.test%252Fsynth",
		`https:\u002f\u002fprivate.example.test/synth`,
		"https:&amp;#47;&amp;#47;private.example.test/synth",
	} {
		for _, representation := range addressScanRepresentations(text) {
			for _, absolute := range []bool{false, true} {
				name := "external"
				if absolute {
					name = "absolute"
				}
				t.Run(inlineTestHash([]byte(text))[:8]+"/"+representation.name+"/"+name, func(t *testing.T) {
					prefix := bytes.Repeat([]byte{'z'}, isolationChunkBytes-3)
					data := append(prefix, representation.data...)
					addressScanCompareStream(t, data, []int{isolationChunkBytes}, absolute)
				})
			}
		}
	}
	for _, little := range []bool{false, true} {
		t.Run(map[bool]string{false: "be", true: "le"}[little], func(t *testing.T) {
			text := "seed1//localhost" + strings.Repeat("x", addressScanOldOverlap/2-len("//localhost"))
			first := addressScanUTF16(text, little, 0)
			current := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
			old := addressScanOldScanner{initial: addressScanOldExternal, continuation: addressScanOldContinued}
			for _, chunk := range [][]byte{first, {0, 0}, {0, 0}} {
				expected := old.contains(chunk)
				if expected {
					t.Fatal("truncated origin fixture must remain a non-URI")
				}
				if current.contains(chunk) != expected || current.seen != old.seen || !bytes.Equal(current.previous, old.previous) {
					t.Fatal("truncated origin was incorrectly reset by the syntax predicate")
				}
			}
		})
	}
	t.Run("marker_free_prefix_keeps_context", func(t *testing.T) {
		current := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
		for index, chunk := range [][]byte{[]byte("https:"), []byte("/"), []byte("/private.example.test/synth")} {
			if current.contains(chunk) != (index == 2) {
				t.Fatal("marker-free window discarded address prefix")
			}
		}
	})
}

func addressScanNoMarkerInputs() []struct {
	name string
	data []byte
} {
	return []struct {
		name string
		data []byte
	}{
		{"zero", make([]byte, 64<<10)},
		{"nonzero_binary", bytes.Repeat([]byte{0, 1, 0x7f, 0xff, 0xe2, 0x98, 0x83, 0}, (64<<10)/8)},
		{"utf16_plain_text", bytes.Repeat([]byte{'a', 0}, (64<<10)/2)},
	}
}

func TestAddressScanNoMarkerAllocations(t *testing.T) {
	for _, input := range addressScanNoMarkerInputs() {
		t.Run(input.name, func(t *testing.T) {
			for _, pattern := range []*regexp.Regexp{externalAddress, continuedExternalAddress, absoluteAddress} {
				allocations := testing.AllocsPerRun(5, func() {
					if containsAddress(input.data, pattern) {
						t.Fatal("marker-free bytes formed an address")
					}
				})
				if allocations != 0 {
					t.Fatalf("marker-free content allocated projections: %.0f", allocations)
				}
			}
		})
	}
}

func TestAddressScanNoMarkerStreamAllocations(t *testing.T) {
	for _, input := range addressScanNoMarkerInputs() {
		t.Run(input.name, func(t *testing.T) {
			scanner := addressScanner{initial: externalAddress, continuation: continuedExternalAddress}
			allocations := testing.AllocsPerRun(5, func() {
				before := scanner.seen
				if scanner.contains(input.data) || scanner.seen != before+int64(len(input.data)) || len(scanner.previous) != addressScanOldOverlap {
					t.Fatal("marker-free stream changed its result or carry accounting")
				}
			})
			// One mandatory overlap+input window remains. UTF16 projection
			// buffers and transformed strings must not be allocated.
			if allocations != 1 {
				t.Fatalf("marker-free stream allocated beyond its window: %.0f", allocations)
			}
		})
	}
}

package diagbundle

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// Free text is the one place a diagnostics bundle cannot be structure only: a
// log line or an upstream error says what happened in words. The Scrubber
// turns such text into something the independent privacy scan accepts, by
// replacing, never by judging:
//
//   - values under credential or prompt keys, and every credential shape
//     (bearer tokens, provider keys, signed-URL parameters), become [redacted];
//   - URLs, addresses, private host names and e-mail addresses become [url],
//     [ip], [host], [email];
//   - values under identity keys (owner_id, username, ...), every identity the
//     bundle has seen and every 32-digit hexadecimal identifier (the shape of a
//     user ID) become stable placeholders id-1, id-2, ...: the bundle can still
//     say "the same user" without saying who;
//   - the free-text inputs of the failed tasks (prompts) become [input].
//
// The key rules are not copied from the scan: a probe asks the scan itself
// which key names it treats as credentials or identities, so the two cannot
// drift. The text rules mirror the scan's content rules, and the scan stays the
// judge: whatever still trips it after scrubbing is dropped by the caller, never
// shipped (see logs.go and bundle.go).

const (
	redacted         = "[redacted]"
	placeholderURL   = "[url]"
	placeholderEmail = "[email]"
	placeholderIP    = "[ip]"
	placeholderHost  = "[host]"
	placeholderBlob  = "[blob]"
	placeholderInput = "[input]"

	// maxValueBytes bounds one string value. A prompt that slipped through is
	// cut short, and no line is dominated by one long error chain.
	maxValueBytes = 1024
	// minIdentityBytes is the shortest identity replaced inside free text. A
	// shorter user name is only handled under its key: as a word it cannot be
	// told from the vocabulary of the log, and replacing it would damage the
	// text that tells what happened.
	minIdentityBytes = 8
	// minPrivateRunes is the shortest prompt echo removed from free text (the
	// kernel's own echo rule starts at 5; a shorter word is indistinguishable
	// from the vocabulary of the log).
	minPrivateRunes = 8
)

var (
	scrubCredentialRE = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}|\bsk-[A-Za-z0-9_-]{8,}|\bAKIA[0-9A-Z]{16}\b|-----BEGIN [A-Z ]*PRIVATE KEY-----|\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|\bgh[pousr]_[A-Za-z0-9]{16,}|\bxox[abprs]-[A-Za-z0-9-]{8,}`)
	scrubURLRE        = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>\\]+`)
	scrubSignedRE     = regexp.MustCompile(`(?i)(?:x-amz-[a-z-]+|x-goog-[a-z-]+|\bsignature|\bexpires|\btoken|\bsig|\bsas|\bgoogleaccessid|\baccess_token|\bapikey|\bapi_key)=[^\s&"';,]*`)
	scrubEmailRE      = regexp.MustCompile(`[A-Za-z0-9._%+'-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)
	scrubIPv6RE       = regexp.MustCompile(`(?i)\[[0-9a-f:.]+\]|\b(?:[0-9a-f]{0,4}:){2,}[0-9a-f:.]*`)
	scrubIPv4RE       = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	scrubHexIPRE      = regexp.MustCompile(`(?i)\b0x[0-9a-f]{8}\b`)
	scrubDottedRE     = regexp.MustCompile(`(?i)\b(?:0x[0-9a-f]{1,8}|\d{1,10})(?:\.(?:0x[0-9a-f]{1,8}|\d{1,10})){1,3}\b`)
	scrubHostRE       = regexp.MustCompile(`(?i)\b(?:[a-z0-9-]+\.)+(?:internal|local|lan|corp|home|intranet|private|localdomain|svc)\b|\blocalhost\b`)
	scrubHostSuffixRE = regexp.MustCompile(`(?i)\.(?:internal|local|lan|corp|home|intranet|private|localdomain|svc|home\.arpa|cluster\.local)\b`)
	scrubUUIDRE       = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	scrubHex32RE      = regexp.MustCompile(`\b[0-9a-f]{32}\b`)
	scrubBlobRE       = regexp.MustCompile(`[A-Za-z0-9+/_-]{80,}={0,2}`)
	scrubDataURIRE    = regexp.MustCompile(`(?i)\bdata:[a-z0-9.+/-]*[;,][^\s"']*`)
	// placeholderRE is what the scan accepts under an identity key.
	placeholderRE = regexp.MustCompile(`^(?:id-\d+|\[redacted\]|ok)?$`)
	promptKeyRE   = regexp.MustCompile(`(?i)prompt|instruction`)
)

// promptKeys are exact (compacted) key names that carry user or model text, and
// extraIdentityKeys are person-related names the scan's own list does not hold.
var (
	promptKeys        = map[string]bool{"system": true, "input": true, "query": true, "content": true, "text": true, "description": true}
	extraIdentityKeys = map[string]bool{
		"actorid": true, "openid": true, "nickname": true, "displayname": true, "ownername": true,
		"approverid": true, "requesterid": true, "creatorid": true,
	}
)

type keyKind int

const (
	keyPlain keyKind = iota
	keyCredential
	keyIdentity
	keyPrompt
)

// Scrubber holds what one export learns about identities and prompts, so every
// category of the bundle replaces the same value with the same placeholder. It
// is not safe for concurrent use.
type Scrubber struct {
	pseudonyms map[string]string
	kinds      map[string]keyKind
	identities map[string]struct{}
	private    map[string]struct{}
	// Derived from identities and private; rebuilt when they change.
	dirty       bool
	longReplace *strings.Replacer
	privateList []string
	privateForm []string
}

// NewScrubber returns a Scrubber that knows nothing yet.
func NewScrubber() *Scrubber {
	return &Scrubber{
		pseudonyms: map[string]string{}, kinds: map[string]keyKind{},
		identities: map[string]struct{}{}, private: map[string]struct{}{},
	}
}

// Pseudonym returns the placeholder of an identity value, id-1, id-2, ... in
// the order the values were first asked for.
func (s *Scrubber) Pseudonym(value string) string {
	if placeholder, known := s.pseudonyms[value]; known {
		return placeholder
	}
	placeholder := "id-" + strconv.Itoa(len(s.pseudonyms)+1)
	s.pseudonyms[value] = placeholder
	return placeholder
}

// AddIdentity registers a person's identifier (user ID, user name, address) so
// free text that repeats it is rewritten to its placeholder. An identity shorter
// than minIdentityBytes is only given a placeholder for its key.
func (s *Scrubber) AddIdentity(value string) {
	value = strings.TrimSpace(value)
	if len(value) < minIdentityBytes || placeholderRE.MatchString(value) {
		return
	}
	if _, known := s.identities[value]; !known {
		s.identities[value] = struct{}{}
		s.dirty = true
	}
}

// AddPrivate registers a user-written text (a prompt) that must not appear in
// anything the bundle contains. Short words are ignored.
func (s *Scrubber) AddPrivate(text string) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) < minPrivateRunes {
		return
	}
	if _, known := s.private[text]; !known {
		s.private[text] = struct{}{}
		s.dirty = true
	}
}

// prepare derives the replacers from the registered values.
func (s *Scrubber) prepare() {
	if !s.dirty {
		return
	}
	s.dirty = false
	identities := make([]string, 0, len(s.identities))
	for identity := range s.identities {
		identities = append(identities, identity)
	}
	// Longest first so an identity that contains another wins; sorted so the
	// placeholders of a given database do not depend on map order.
	sortLongestFirst(identities)
	pairs := make([]string, 0, len(identities)*2)
	for _, identity := range identities {
		pairs = append(pairs, identity, s.Pseudonym(identity))
	}
	s.longReplace = nil
	if len(pairs) > 0 {
		s.longReplace = strings.NewReplacer(pairs...)
	}
	s.privateList = s.privateList[:0]
	s.privateForm = s.privateForm[:0]
	for text := range s.private {
		s.privateList = append(s.privateList, text)
	}
	sortLongestFirst(s.privateList)
	for _, text := range s.privateList {
		// The form the text takes inside a JSON string.
		encoded, _ := json.Marshal(text)
		s.privateForm = append(s.privateForm, string(encoded[1:len(encoded)-1]))
	}
}

func sortLongestFirst(values []string) {
	sort.Slice(values, func(i, j int) bool {
		if len(values[i]) != len(values[j]) {
			return len(values[i]) > len(values[j])
		}
		return values[i] < values[j]
	})
}

// kind asks the scan which rule a key name falls under.
func (s *Scrubber) kind(key string) keyKind {
	if kind, known := s.kinds[key]; known {
		return kind
	}
	kind := keyPlain
	compact := compactKey(key)
	probe := privacyscan.ScanText("probe", "$", key, "value")
	switch {
	case privacyscan.HasRule(probe, "credential-field"):
		kind = keyCredential
	case privacyscan.HasRule(probe, "identity-field") || extraIdentityKeys[compact]:
		kind = keyIdentity
	case promptKeyRE.MatchString(key) || promptKeys[compact]:
		kind = keyPrompt
	}
	s.kinds[key] = kind
	return kind
}

func compactKey(key string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '.' || r == '-' || r == '_' || r == ' ':
			return -1
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return r
	}, key)
}

// Collect registers the identity values found under identity keys anywhere in a
// decoded JSON value. Run it over everything before scrubbing, so an identity
// that appears in free text before it appears under its key is still caught.
func (s *Scrubber) Collect(key string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			s.Collect(childKey, child)
		}
	case []any:
		for _, child := range typed {
			s.Collect(key, child)
		}
	case string:
		if s.kind(key) == keyIdentity {
			s.AddIdentity(typed)
		}
	case json.Number:
		if s.kind(key) == keyIdentity {
			s.AddIdentity(typed.String())
		}
	}
}

// Value scrubs one decoded JSON value that sits under key.
func (s *Scrubber) Value(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			scrubbed := s.Text(childKey)
			if _, taken := out[scrubbed]; taken {
				continue
			}
			out[scrubbed] = s.Value(childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = s.Value(key, child)
		}
		return out
	case string:
		return s.Leaf(key, typed)
	case json.Number:
		if s.kind(key) == keyIdentity {
			return s.Pseudonym(typed.String())
		}
	}
	return value
}

// Leaf scrubs one string that sits under key.
func (s *Scrubber) Leaf(key, text string) string {
	if text == "" {
		return text
	}
	switch s.kind(key) {
	case keyCredential, keyPrompt:
		return redacted
	case keyIdentity:
		if placeholderRE.MatchString(text) {
			return text
		}
		return s.Pseudonym(text)
	}
	return s.Text(text)
}

// Text scrubs free text. It is idempotent.
func (s *Scrubber) Text(text string) string {
	if text == "" {
		return text
	}
	s.prepare()
	text = strings.ToValidUTF8(text, " ")
	for _, private := range s.privateList {
		text = strings.ReplaceAll(text, private, placeholderInput)
	}
	if s.longReplace != nil {
		text = s.longReplace.Replace(text)
	}
	text = logging.Redact(text)
	// Each pass runs only when the text could hold what it looks for: most
	// values of a log line are short words and identifiers, and the passes are
	// regular expressions.
	if hasFold(text, credentialHints) {
		text = scrubCredentialRE.ReplaceAllString(text, redacted)
	}
	if strings.Contains(text, "://") {
		text = scrubURLRE.ReplaceAllString(text, placeholderURL)
	}
	if strings.Contains(text, "=") {
		text = scrubSignedRE.ReplaceAllString(text, redacted)
	}
	if strings.Contains(text, "@") {
		text = scrubEmailRE.ReplaceAllString(text, placeholderEmail)
	}
	if strings.Count(text, ":") >= 2 {
		text = scrubIPv6RE.ReplaceAllStringFunc(text, func(match string) string {
			if _, err := netip.ParseAddr(strings.Trim(match, "[]")); err == nil {
				return placeholderIP
			}
			return match
		})
	}
	if hasFold(text, []string{"0x"}) {
		text = scrubHexIPRE.ReplaceAllStringFunc(text, func(match string) string {
			if value, err := strconv.ParseUint(match[2:], 16, 32); err == nil && nonPublicAddress(uint32Address(value)) {
				return placeholderIP
			}
			return match
		})
	}
	if strings.Contains(text, ".") {
		text = scrubIPv4RE.ReplaceAllString(text, placeholderIP)
		text = scrubDottedRE.ReplaceAllStringFunc(text, func(match string) string {
			if legacyAddress(match) {
				return placeholderIP
			}
			return match
		})
		text = scrubHostRE.ReplaceAllString(text, placeholderHost)
		text = scrubHostSuffixRE.ReplaceAllString(text, "."+placeholderHost)
	} else if hasFold(text, []string{"localhost"}) {
		text = scrubHostRE.ReplaceAllString(text, placeholderHost)
	}
	if len(text) >= 36 && strings.Contains(text, "-") {
		text = scrubUUIDRE.ReplaceAllStringFunc(text, s.Pseudonym)
	}
	if len(text) >= 32 {
		text = scrubHex32RE.ReplaceAllStringFunc(text, s.Pseudonym)
	}
	if hasFold(text, []string{"data:"}) {
		text = scrubDataURIRE.ReplaceAllString(text, placeholderBlob)
	}
	if len(text) >= 80 {
		text = scrubBlobRE.ReplaceAllStringFunc(text, func(run string) string {
			if len(run) >= 200 || mixedBlob(run) {
				return placeholderBlob
			}
			return run
		})
	}
	return truncateText(text, maxValueBytes)
}

// credentialHints are the lower-case words a credential shape of
// scrubCredentialRE contains.
var credentialHints = []string{"bearer", "basic", "sk-", "akia", "begin", "eyj", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "xox"}

// hasFold reports whether text contains any of the lower-case words, ignoring
// case.
func hasFold(text string, words []string) bool {
	lower := strings.ToLower(text)
	for _, word := range words {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// nonPublicAddress is the scan's notion of an address that must not be shipped.
func nonPublicAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	return !addr.IsValid() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || !addr.IsGlobalUnicast() ||
		(addr.Is6() && !netip.MustParsePrefix("2000::/3").Contains(addr))
}

func uint32Address(value uint64) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

// legacyAddress reports whether a dotted group of numbers reads, with octal or
// hexadecimal components (010.0.0.1, 0x7f.1, 10.01), as a non-public IPv4
// address, which some resolvers accept and the scan flags. Ordinary decimals and
// the fractions of a time stamp (00.123) do not.
func legacyAddress(match string) bool {
	parts := strings.Split(match, ".")
	legacy := false
	for _, part := range parts {
		legacy = legacy || (len(part) > 1 && part[0] == '0')
	}
	if !legacy {
		return false
	}
	for _, base := range []int{0, 10} {
		var number uint64
		valid := true
		for i, part := range parts {
			value, err := strconv.ParseUint(part, base, 32)
			if err != nil {
				valid = false
				break
			}
			if i < len(parts)-1 {
				valid = valid && value <= 255
				number |= value << (8 * uint(3-i))
			} else {
				valid = valid && value < 1<<(8*uint(4-i))
				number |= value
			}
		}
		if valid && nonPublicAddress(uint32Address(number)) {
			return true
		}
	}
	return false
}

// mixedBlob is the scan's test for a base64-like run: upper case, lower case and
// a digit together.
func mixedBlob(run string) bool {
	return strings.IndexFunc(run, unicode.IsUpper) >= 0 && strings.IndexFunc(run, unicode.IsLower) >= 0 &&
		strings.IndexFunc(run, unicode.IsDigit) >= 0
}

// truncateText cuts text to at most limit bytes without splitting a character.
func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// Leaks reports the rule a piece of output breaks when it still holds an
// identity or a prompt the Scrubber knows, or "" when it holds none. It is the
// check behind the scrubbing: whatever Text replaced must be gone.
func (s *Scrubber) Leaks(data []byte) string {
	s.prepare()
	text := string(data)
	for i, private := range s.privateList {
		if strings.Contains(text, private) || strings.Contains(text, s.privateForm[i]) {
			return "known-private-text"
		}
	}
	for identity := range s.identities {
		if strings.Contains(text, identity) {
			return "known-identity"
		}
	}
	return ""
}

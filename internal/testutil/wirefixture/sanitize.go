package wirefixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var identifierRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var modelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+ -]{0,127}$`)
var segmentRE = regexp.MustCompile(`^[A-Za-z0-9_{}.-]{1,128}$`)
var schemeURLRE = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>\\]+`)
var protocolRelativeURLRE = regexp.MustCompile(`(?i)(?:^|[\s"'(\[{=])(//[^\s"'<>\\/][^\s"'<>\\]*)`)
var pathURLRefRE = regexp.MustCompile(`(?i)(?:^|[\s"'(\[{=])(/[^\s"'<>\\]*[?#][^\s"'<>\\]*)`)
var pathUserinfoURLRE = regexp.MustCompile(`(?i)(?:^|[\s"'(\[{=])(/[^\s"'<>\\]*[^/\s"'<>\\]:[^/\s"'<>\\@]+@[^\s"'<>\\]*)`)
var credentialRE = regexp.MustCompile(`(?i)(\bBearer\s+[A-Za-z0-9._~+/-]{8,}=*|\bBasic\s+[A-Za-z0-9+/=]{8,}|\bsk-[A-Za-z0-9_-]{12,}|\bAKIA[0-9A-Z]{16}\b|-----BEGIN [A-Z ]*PRIVATE KEY-----|\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}|\bgh[pousr]_[A-Za-z0-9]{20,}|\b(?:token|api[._ -]*key|api[._ -]*token|auth[._ -]*token|password|passwd|secret(?:[._ -]*access)?[._ -]*key|secret|subscription[._ -]*key|signed[._ -]*url|x[._ -]*amz[._ -]*signature|x[._ -]*amz[._ -]*credential|x[._ -]*amz[._ -]*security[._ -]*token)\s*[:=]\s*(?:\\?["'][^"'\\]{1,}\\?["']|[A-Za-z0-9._~+/-]{3,}))`)
var privateHostRE = regexp.MustCompile(`(?i)(\blocalhost\b|\b127\.0\.0\.1\b|\b0\.0\.0\.0\b|\b169\.254\.\d{1,3}\.\d{1,3}\b|\b10\.\d{1,3}\.\d{1,3}\.\d{1,3}\b|\b192\.168\.\d{1,3}\.\d{1,3}\b|\b172\.(1[6-9]|2[0-9]|3[01])\.\d{1,3}\.\d{1,3}\b|\.internal\b|\.local\b|\.svc\b|\.corp\b|\.lan\b|\.home\b|\.home\.arpa\b|\.intranet\b|\.private\b|\.localdomain\b|\[::1\])`)
var dataURIRE = regexp.MustCompile(`(?i)\bdata:[^\s"'<>]*[,;]`)
var quotedKeyRE = regexp.MustCompile(`"(?:[^"\\]|\\.)*"\s*:`)
var credentialAssignmentRE = regexp.MustCompile(`(?i)\b([A-Za-z0-9][A-Za-z0-9._-]{0,63})\s*[:=]\s*(?:\\?["'][^"'\\]{1,}\\?["']|[^\s"'<>]{1,})`)
var embeddedJSONKeyRE = regexp.MustCompile(`(?i)\\?"([A-Za-z0-9][A-Za-z0-9._ -]{0,63})\\?"\s*:`)
var ipv4RE = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
var ipv6RE = regexp.MustCompile(`(?i)\[[0-9a-f:]+\]|(?:[0-9a-f]{0,4}:){2,}[0-9a-f:.]*`)
var globalIPv6Prefix = netip.MustParsePrefix("2000::/3")

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var methods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true,
	http.MethodDelete: true, http.MethodHead: true, http.MethodOptions: true,
}

var requestHeaders = map[string]bool{"content-type": true, "accept": true, "x-api-version": true}
var responseHeaders = map[string]bool{"content-type": true, "retry-after": true}

func baseMedia(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
}

func payloadKind(contentType string) (string, error) {
	switch media := baseMedia(contentType); {
	case media == "":
		return "", nil
	case media == "application/json" || strings.HasSuffix(media, "+json"):
		return PayloadJSON, nil
	case media == "text/event-stream":
		return PayloadSSE, nil
	case media == "multipart/form-data":
		return PayloadMultipart, nil
	case supportedMediaTypes[media]:
		return PayloadMedia, nil
	default:
		return "", ErrUnsupportedBody
	}
}

func sanitizeHost(host string, policy Policy) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "example.test" {
		return host, nil
	}
	if mapped, ok := policy.Aliases[host]; ok && mapped == "example.test" {
		if _, chained := policy.Aliases[mapped]; chained {
			return "", fmt.Errorf("%w: request host", ErrUnsafePublicValue)
		}
		return mapped, nil
	}
	if policy.PublicHosts[host] {
		return host, nil
	}
	return "", fmt.Errorf("%w: request host", ErrUnsafePublicValue)
}

func sanitizePath(raw string, policy Policy) (string, error) {
	if raw == "" {
		raw = "/"
	}
	if !strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "?#") || strings.Contains(raw, "://") {
		return "", fmt.Errorf("%w: path", ErrUnsafePublicValue)
	}
	parts := strings.Split(raw, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if policy.projector != nil {
			if public, exists := policy.projector.ids[part]; exists {
				parts[i] = public
				continue
			}
		}
		mapped, ok := policy.Aliases[part]
		if !ok {
			if !policy.Allowed[part] {
				return "", fmt.Errorf("%w: path segment %q", ErrUnknownKey, part)
			}
			mapped = part
		} else {
			if _, chained := policy.Aliases[mapped]; chained {
				return "", fmt.Errorf("%w: path alias", ErrUnsafePublicValue)
			}
			if !policy.Allowed[mapped] {
				return "", fmt.Errorf("%w: path segment %q", ErrUnknownKey, mapped)
			}
		}
		if mapped == "." || mapped == ".." || !segmentRE.MatchString(mapped) {
			return "", fmt.Errorf("%w: path segment %q", ErrUnsafePublicValue, mapped)
		}
		if err := checkText(mapped, false); err != nil {
			return "", err
		}
		parts[i] = mapped
	}
	return strings.Join(parts, "/"), nil
}

func sanitizeHeaders(headers http.Header, request bool, policy Policy) (map[string]string, error) {
	allow := responseHeaders
	if request {
		allow = requestHeaders
	}
	out := map[string]string{}
	for name, values := range headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if !allow[lower] {
			continue
		}
		value := strings.Join(values, ", ")
		switch lower {
		case "content-type":
			if _, err := contentBase(value); err != nil {
				return nil, err
			}
			if err := checkText(value, false); err != nil {
				return nil, err
			}
			continue
		case "accept", "x-api-version":
			resolved, err := policy.resolve(value, 0)
			if err != nil {
				return nil, fmt.Errorf("header %q: %w", lower, err)
			}
			value = resolved
		case "retry-after":
			if _, err := strconv.Atoi(value); err != nil {
				resolved, err := policy.resolve(value, 0)
				if err != nil {
					return nil, fmt.Errorf("header %q: %w", lower, err)
				}
				value = resolved
			}
		}
		if err := checkText(value, false); err != nil {
			return nil, err
		}
		out[lower] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (p Policy) resolve(raw string, depth int) (string, error) {
	if depth > 12 {
		return "", fmt.Errorf("%w: nesting", ErrUnsafePublicValue)
	}
	if mapped, ok := p.Aliases[raw]; ok {
		if mapped != "[redacted]" && wholeURL(raw) && !canonicalExampleURL(mapped) {
			return "", fmt.Errorf("%w: URL alias", ErrUnsafePublicValue)
		}
		return p.resolveAlias(mapped, depth)
	}
	if raw == "[redacted]" || canonicalExampleURL(raw) {
		return raw, nil
	}
	if p.Allowed[raw] {
		if publicURLAllowed(raw, p) {
			return raw, nil
		}
		if schemeURLRE.MatchString(raw) || privateHostRE.MatchString(raw) || credentialRE.MatchString(raw) || dataURIRE.MatchString(raw) {
			return "", fmt.Errorf("%w: alias required", ErrUnsafePublicValue)
		}
		if err := checkText(raw, true); err != nil {
			return "", err
		}
		return raw, nil
	}
	return "", fmt.Errorf("%w: string not approved", ErrUnsafePublicValue)
}

func (p Policy) resolveAlias(mapped string, depth int) (string, error) {
	if depth > 12 {
		return "", fmt.Errorf("%w: nesting", ErrUnsafePublicValue)
	}
	if _, chained := p.Aliases[mapped]; chained {
		return "", fmt.Errorf("%w: alias chain", ErrUnsafePublicValue)
	}
	if mapped == "[redacted]" {
		return mapped, nil
	}
	if canonicalExampleURL(mapped) {
		return mapped, nil
	}
	nested, ok, err := nestedString(mapped, p, depth+1)
	if err != nil {
		return "", err
	}
	if ok {
		return nested, nil
	}
	if p.Allowed[mapped] {
		if publicURLAllowed(mapped, p) {
			return mapped, nil
		}
		if err := checkText(mapped, true); err != nil {
			return "", err
		}
		return mapped, nil
	}
	return "", fmt.Errorf("%w: alias target", ErrUnsafePublicValue)
}

func wholeURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != "" && parsed.Host != "" && schemeURLRE.FindString(value) == value
}

func canonicalExampleURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "example.test" {
		return false
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return false
	}
	return checkText(value, true) == nil
}

func approvedSignedURL(value string, policy Policy) (string, bool) {
	mapped, err := policy.resolve(value, 0)
	return mapped, err == nil && (mapped == "[redacted]" || canonicalExampleURL(mapped))
}

func checkText(text string, allowWhitespace bool) error {
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: invalid UTF-8", ErrUnsafePublicValue)
	}
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' {
			if allowWhitespace {
				continue
			}
			return fmt.Errorf("%w: control character", ErrUnsafePublicValue)
		}
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: control character", ErrUnsafePublicValue)
		}
	}
	if credentialRE.MatchString(text) || credentialAssignment(text) || embeddedCredentialField(text) ||
		privateHostRE.MatchString(text) || dataURIRE.MatchString(text) || nonPublicIPsInText(text) ||
		protocolRelativeURLRE.MatchString(text) || pathURLRefRE.MatchString(text) || pathUserinfoURLRE.MatchString(text) {
		return fmt.Errorf("%w: credential, private host or data URI", ErrUnsafePublicValue)
	}
	for _, raw := range schemeURLRE.FindAllString(text, -1) {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "example.test" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%w: URL", ErrUnsafePublicValue)
		}
	}
	return nil
}

func nonPublicIPsInText(text string) bool {
	for _, raw := range ipv4RE.FindAllString(text, -1) {
		if addr, err := netip.ParseAddr(raw); err == nil && nonPublicAddr(addr) {
			return true
		}
	}
	for _, raw := range ipv6RE.FindAllString(text, -1) {
		raw = strings.Trim(raw, "[]")
		if addr, err := netip.ParseAddr(raw); err == nil && nonPublicAddr(addr) {
			return true
		}
	}
	return false
}

func nonPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || !addr.IsGlobalUnicast() {
		return true
	}
	if addr.Is6() && !globalIPv6Prefix.Contains(addr) {
		return true
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func compactKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(key))
}

var sensitiveKeySuffixes = []string{
	"apikey", "apisecret", "accesskey", "authtoken", "accesstoken", "refreshtoken", "idtoken",
	"password", "passwd", "secret", "secretkey", "secretaccesskey", "clientsecret",
	"privatekey", "credential", "credentials", "signature", "subscriptionkey",
	"sessionid", "sessiontoken", "signedurl", "token", "auth",
}

func sensitiveKey(key string) bool {
	compact := compactKey(key)
	switch compact {
	case "authorization", "cookie", "setcookie", "subscriptionkey", "apikey", "xapikey", "accesskey", "apisecret",
		"token", "accesstoken", "refreshtoken", "idtoken", "xauthtoken", "authtoken", "apitoken", "password",
		"passwd", "secret", "clientsecret", "secretkey", "secretaccesskey", "signature", "xsignature",
		"xamzsignature", "xamzcredential", "xamzsecuritytoken", "signedurl",
		"credential", "credentials", "sessionid", "sessiontoken", "privatekey", "auth":
		return true
	}
	for _, suffix := range sensitiveKeySuffixes {
		if strings.HasSuffix(compact, suffix) {
			return true
		}
	}
	return false
}

func credentialAssignment(text string) bool {
	for _, match := range credentialAssignmentRE.FindAllStringSubmatch(text, -1) {
		if sensitiveKey(match[1]) {
			return true
		}
	}
	return false
}

func embeddedCredentialField(text string) bool {
	for _, match := range embeddedJSONKeyRE.FindAllStringSubmatch(text, -1) {
		if sensitiveKey(match[1]) {
			return true
		}
	}
	return false
}

func sanitizeJSON(raw []byte, policy Policy) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: JSON", ErrUnsafePublicValue)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON", ErrUnsafePublicValue)
	}
	clean, err := sanitizeValue(value, policy, 0)
	if err != nil {
		return nil, err
	}
	out, err := marshalJSON(clean)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func sanitizeValue(value any, policy Policy, depth int) (any, error) {
	if depth > 12 {
		return nil, fmt.Errorf("%w: nesting", ErrUnsafePublicValue)
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var unknown []string
		for _, key := range keys {
			if err := checkText(key, false); err != nil {
				return nil, err
			}
			if !policy.Keys[key] {
				unknown = append(unknown, key)
			}
		}
		if len(unknown) > 0 {
			return nil, fmt.Errorf("%w: keys %q", ErrUnknownKey, unknown)
		}
		for _, key := range keys {
			child := typed[key]
			if sensitiveKey(key) {
				if compactKey(key) == "signedurl" {
					if text, ok := child.(string); ok {
						if mapped, approved := approvedSignedURL(text, policy); approved {
							typed[key] = mapped
							continue
						}
					}
				}
				typed[key] = "[redacted]"
				continue
			}
			if kind, projected := policy.Projection[key]; projected {
				clean, err := projectValue(kind, child, policy, depth)
				if err != nil {
					return nil, fmt.Errorf("key %q: %w", key, err)
				}
				typed[key] = clean
				continue
			}
			clean, err := sanitizeValue(child, policy, depth+1)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
			typed[key] = clean
		}
		return typed, nil
	case []any:
		for i, child := range typed {
			clean, err := sanitizeValue(child, policy, depth+1)
			if err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
			typed[i] = clean
		}
		return typed, nil
	case string:
		if _, aliased := policy.Aliases[typed]; aliased {
			return policy.resolve(typed, depth)
		}
		if typed == "" || typed == "[redacted]" {
			return typed, nil
		}
		if policy.projector != nil && typed == "ok" {
			return typed, nil
		}
		if wholeURL(typed) {
			return policy.resolve(typed, depth)
		}
		if nested, ok, err := nestedString(typed, policy, depth); err != nil {
			return nil, err
		} else if ok {
			return nested, nil
		}
		if credentialRE.MatchString(typed) {
			return "[redacted]", nil
		}
		if mapped, err := policy.resolve(typed, depth); err == nil {
			return mapped, nil
		} else if policy.projector != nil && identifierRE.MatchString(typed) {
			return policy.projector.id(typed, policy)
		} else {
			return "", err
		}
	case json.Number:
		raw := typed.String()
		if mapped, ok := policy.Aliases[raw]; ok {
			if _, chained := policy.Aliases[mapped]; chained {
				return nil, fmt.Errorf("%w: numeric alias", ErrUnsafePublicValue)
			}
			number, ok := parseJSONNumber(mapped)
			if !ok || mapped != number.String() || !policy.Allowed[mapped] {
				return nil, fmt.Errorf("%w: numeric alias", ErrUnsafePublicValue)
			}
			return number, nil
		}
		if policy.Allowed[raw] {
			return typed, nil
		}
		return nil, fmt.Errorf("%w: number not approved", ErrUnsafePublicValue)
	case bool, nil:
		return value, nil
	default:
		return value, nil
	}
}

func parseJSONNumber(value string) (json.Number, bool) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return "", false
	}
	number, ok := parsed.(json.Number)
	if !ok {
		return "", false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", false
	}
	return number, true
}

func nestedString(text string, policy Policy, depth int) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 2 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", false, nil
	}
	if depth > 12 {
		return "", true, fmt.Errorf("%w: nesting", ErrUnsafePublicValue)
	}
	if !json.Valid([]byte(trimmed)) {
		// Malformed object/array text still fails closed; other text needs exact approval.
		if quotedKeyRE.MatchString(trimmed) {
			return "", true, fmt.Errorf("%w: nested JSON", ErrUnsafePublicValue)
		}
		return "", false, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", true, fmt.Errorf("%w: nested JSON", ErrUnsafePublicValue)
	}
	clean, err := sanitizeValue(value, policy, depth+1)
	if err != nil {
		return "", true, err
	}
	out, err := marshalJSON(clean)
	return string(out), true, err
}

func sanitizeBody(raw []byte, contentType string, policy Policy) (Payload, error) {
	kind, err := payloadKind(contentType)
	if err != nil {
		return Payload{}, err
	}
	if kind == PayloadSSE && len(raw) == 0 {
		return Payload{}, ErrPartialSSE
	}
	if len(raw) == 0 {
		return Payload{Encoding: PayloadNone}, nil
	}
	switch kind {
	case PayloadJSON:
		clean, err := sanitizeJSON(raw, policy)
		if err != nil {
			return Payload{}, err
		}
		return Payload{Encoding: PayloadJSON, JSON: clean}, nil
	case PayloadSSE:
		clean, err := sanitizeSSE(raw, policy)
		if err != nil {
			return Payload{}, err
		}
		return Payload{Encoding: PayloadSSE, Transform: TransformNormalizedSSE, SSE: clean}, nil
	default:
		return Payload{}, fmt.Errorf("%w: body type is not supported", ErrUnsupportedBody)
	}
}

func marshalJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

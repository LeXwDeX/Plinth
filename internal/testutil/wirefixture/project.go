package wirefixture

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var publicExtensionRE = regexp.MustCompile(`^\.[a-z0-9]{1,8}$`)

// projector is capture-scoped. It maps each raw dynamic value to one fixed
// public placeholder so repeated IDs and signed URLs stay consistent across an
// entire scenario. It never receives free text: only registered "id"/"url"
// fields reach it.
type projector struct {
	ids     map[string]string
	urls    map[string]string
	nextID  int
	nextURL int
}

func newProjector() *projector {
	return &projector{ids: map[string]string{}, urls: map[string]string{}}
}

func (p *projector) id(raw string, policy Policy) (string, error) {
	if token, ok := p.ids[raw]; ok {
		return token, nil
	}
	if strings.HasPrefix(raw, "id-") && identifierRE.MatchString(raw) {
		return raw, nil
	}
	if !identifierRE.MatchString(raw) {
		return "", fmt.Errorf("%w: projected identifier", ErrUnsafePublicValue)
	}
	p.nextID++
	token := "id-" + strconv.Itoa(p.nextID)
	p.ids[raw] = token
	if policy.Allowed != nil {
		policy.Allowed[token] = true
	}
	return token, nil
}

func (p *projector) publicURL(raw string, policy Policy) (string, error) {
	if canonicalExampleURL(raw) {
		return raw, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: projected URL", ErrUnsafePublicValue)
	}
	host := strings.ToLower(parsed.Host)
	publicHost := host
	if mapped, ok := policy.Aliases[host]; ok && mapped == "example.test" {
		publicHost = mapped
	} else if !policy.PublicHosts[host] {
		return "", fmt.Errorf("%w: projected URL host", ErrUnsafePublicValue)
	}
	if parsed.RawQuery != "" {
		values, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			return "", fmt.Errorf("%w: projected URL query", ErrUnsafePublicValue)
		}
		for key := range values {
			if !policy.QueryKeys[key] {
				return "", fmt.Errorf("%w: projected URL query %q", ErrUnsafePublicValue, key)
			}
		}
	}
	key := canonicalURL(parsed)
	if existing, ok := p.urls[key]; ok {
		return existing, nil
	}
	p.nextURL++
	name := "fixture-" + strconv.Itoa(p.nextURL) + publicExtension(parsed.Path)
	target := "https://" + publicHost + "/" + name
	p.urls[key] = target
	if policy.Allowed != nil {
		policy.Allowed[target] = true
		policy.Allowed[name] = true
	}
	return target, nil
}

func canonicalURL(parsed *url.URL) string {
	host := strings.ToLower(parsed.Host)
	query := parsed.Query().Encode()
	if query != "" {
		query = "?" + query
	}
	return "https://" + host + parsed.EscapedPath() + query
}

func publicExtension(rawPath string) string {
	ext := strings.ToLower(path.Ext(rawPath))
	if publicExtensionRE.MatchString(ext) {
		return ext
	}
	return ""
}

func queryKeysAllowed(rawQuery string, registered map[string]bool) bool {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return false
	}
	for key := range values {
		if !registered[key] || len(key) > 128 || strings.ContainsAny(key, "\r\n") {
			return false
		}
	}
	return true
}

// publicQueryKeys returns the registered query keys without their values.
// Values are never copied into an Exchange.
func publicQueryKeys(parsed *url.URL, policy Policy) (keys []string, valid bool) {
	if parsed == nil || parsed.User != nil {
		return nil, false
	}
	if parsed.RawQuery == "" && !parsed.ForceQuery {
		return nil, true
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return nil, false
	}
	for key := range values {
		if !policy.QueryKeys[key] || len(key) > 128 || strings.ContainsAny(key, "\r\n") {
			return nil, false
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, true
}

// publicRequestURL returns the public host and path recorded for a raw request
// URL. Signed query values are used only for the lookup and are never returned.
func publicRequestURL(policy Policy, scheme, host, rawPath, rawQuery string) (string, string, bool) {
	if policy.projector == nil {
		return "", "", false
	}
	parsed, err := url.Parse(scheme + "://" + host + rawPath)
	if err != nil || parsed.Host == "" {
		return "", "", false
	}
	if rawQuery != "" || strings.HasSuffix(rawPath, "?") {
		parsed.RawQuery = rawQuery
	}
	target, ok := policy.projector.urls[canonicalURL(parsed)]
	if !ok {
		return "", "", false
	}
	public, err := url.Parse(target)
	if err != nil {
		return "", "", false
	}
	return public.Host, public.EscapedPath(), true
}

func projectValue(kind string, value any, policy Policy, depth int) (any, error) {
	if policy.projector == nil {
		return nil, fmt.Errorf("%w: projection requires deferred capture", ErrUnsafePublicValue)
	}
	switch kind {
	case "id", "ids":
		return projectIDDeep(value, policy, depth)
	case "url":
		return projectURLDeep(value, policy, depth)
	case "timestamp":
		switch typed := value.(type) {
		case nil:
			return nil, nil
		case json.Number:
			token := json.Number("1700000000000")
			if policy.Allowed != nil {
				policy.Allowed[token.String()] = true
			}
			return token, nil
		case string:
			if _, err := time.Parse(time.RFC3339, typed); err != nil {
				return nil, fmt.Errorf("%w: projected timestamp type", ErrUnsafePublicValue)
			}
			token := "2026-01-01T00:00:00Z"
			if policy.Allowed != nil {
				policy.Allowed[token] = true
			}
			return token, nil
		default:
			return nil, fmt.Errorf("%w: projected timestamp type", ErrUnsafePublicValue)
		}
	case "number":
		switch value.(type) {
		case nil:
			return nil, nil
		case json.Number:
			token := json.Number("0")
			if policy.Allowed != nil {
				policy.Allowed[token.String()] = true
			}
			return token, nil
		default:
			return nil, fmt.Errorf("%w: projected number type", ErrUnsafePublicValue)
		}
	case "text":
		switch typed := value.(type) {
		case nil:
			return nil, nil
		case string:
			if typed == "" {
				return "", nil
			}
			token := "ok"
			if policy.Allowed != nil {
				policy.Allowed[token] = true
			}
			return token, nil
		default:
			return nil, fmt.Errorf("%w: projected text type", ErrUnsafePublicValue)
		}
	case "opaque":
		if typed, ok := value.(string); ok && typed == "" {
			return "", nil
		}
		token := "ok"
		if policy.Allowed != nil {
			policy.Allowed[token] = true
		}
		return token, nil
	default:
		return nil, fmt.Errorf("%w: projection kind", ErrUnsafePublicValue)
	}
}

func projectIDDeep(value any, policy Policy, depth int) (any, error) {
	if depth > 12 {
		return nil, fmt.Errorf("%w: nesting", ErrUnsafePublicValue)
	}
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		if typed == "" {
			return typed, nil
		}
		return policy.projector.id(typed, policy)
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			clean, err := projectIDDeep(child, policy, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = clean
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: projected identifier type", ErrUnsafePublicValue)
	}
}

func projectURLDeep(value any, policy Policy, depth int) (any, error) {
	if depth > 12 {
		return nil, fmt.Errorf("%w: nesting", ErrUnsafePublicValue)
	}
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case string:
		if typed == "" {
			return "", nil
		}
		return policy.projector.publicURL(typed, policy)
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			if !policy.Keys[key] || sensitiveKey(key) || checkText(key, false) != nil {
				return nil, fmt.Errorf("%w: projected URL key %q", ErrUnknownKey, key)
			}
			clean, err := projectURLDeep(child, policy, depth+1)
			if err != nil {
				return nil, err
			}
			typed[key] = clean
		}
		return typed, nil
	case []any:
		for i, child := range typed {
			clean, err := projectURLDeep(child, policy, depth+1)
			if err != nil {
				return nil, err
			}
			typed[i] = clean
		}
		return typed, nil
	default:
		return nil, fmt.Errorf("%w: projected URL type", ErrUnsafePublicValue)
	}
}

func publicURLAllowed(raw string, policy Policy) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return false
	}
	host := strings.ToLower(parsed.Host)
	if host != "example.test" && !policy.PublicHosts[host] {
		return false
	}
	for _, segment := range strings.Split(parsed.EscapedPath(), "/") {
		if segment != "" && !segmentRE.MatchString(segment) {
			return false
		}
	}
	return true
}

// ValidatePolicy rejects ambiguous or unsafe policy registrations before any
// request starts. It does not approve values; the sanitizer still does that.
func ValidatePolicy(policy Policy) error {
	if policy.MaxBodyBytes < 0 || policy.MaxBodyBytes > 8<<20 {
		return fmt.Errorf("%w: body limit", ErrUnsafePublicValue)
	}
	for key, enabled := range policy.Keys {
		if !enabled || key == "" || checkText(key, false) != nil || !identifierRE.MatchString(key) {
			return fmt.Errorf("%w: policy key", ErrUnsafePublicValue)
		}
	}
	for value, enabled := range policy.Allowed {
		if !enabled || value == "" || len(value) > 4096 {
			return fmt.Errorf("%w: policy allowed value", ErrUnsafePublicValue)
		}
	}
	for raw, target := range policy.Aliases {
		if raw == "" || target == "" || len(raw) > 4096 || len(target) > 4096 {
			return fmt.Errorf("%w: policy alias", ErrUnsafePublicValue)
		}
		if _, chained := policy.Aliases[target]; chained {
			return fmt.Errorf("%w: policy alias chain", ErrUnsafePublicValue)
		}
	}
	for host, enabled := range policy.PublicHosts {
		if !enabled || !validPublicHost(host) {
			return fmt.Errorf("%w: public host", ErrUnsafePublicValue)
		}
	}
	for key, kind := range policy.Projection {
		if !policy.Keys[key] || sensitiveKey(key) || checkText(key, false) != nil ||
			(kind != "id" && kind != "ids" && kind != "url" && kind != "timestamp" && kind != "number" && kind != "text" && kind != "opaque") {
			return fmt.Errorf("%w: projection", ErrUnsafePublicValue)
		}
	}
	for key, enabled := range policy.QueryKeys {
		if !enabled || key == "" || len(key) > 128 || strings.ContainsAny(key, "\r\n") {
			return fmt.Errorf("%w: query key", ErrUnsafePublicValue)
		}
	}
	return nil
}

func validPublicHost(host string) bool {
	if host == "" || host != strings.ToLower(host) || strings.TrimSpace(host) != host ||
		host == "example.test" || strings.ContainsAny(host, "/?#@ \\") || privateHostRE.MatchString(host) {
		return false
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if _, err := netip.ParseAddr(parsed.Hostname()); err == nil || legacyNumericHostRE.MatchString(parsed.Hostname()) {
		return false
	}
	return true
}

// legacyNumericHostRE matches hosts that resolvers read as an IPv4 address in
// integer, hex or octal form (3232235529, 0xC0A80009, 0300.0250.0.9).
var legacyNumericHostRE = regexp.MustCompile(`(?i)^(?:0x[0-9a-f]+|\d+)(?:\.(?:0x[0-9a-f]+|\d+))*$`)

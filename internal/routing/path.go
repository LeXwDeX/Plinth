package routing

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var pathParameterPattern = regexp.MustCompile(
	`\{([A-Za-z][A-Za-z0-9_]*)\}`,
)

// One 是经启动期校验、仅含一个 Schema 参数的 HTTP 路径模板。
type One struct {
	prefix string
	suffix string
}

// Two 是经启动期校验、按声明顺序含两个 Schema 参数的 HTTP 路径模板。
type Two struct {
	prefix          string
	middle          string
	suffix          string
	firstThenSecond bool
}

func ParseOne(path string, parameter string) (One, error) {
	segments, _, err := parse(path, []string{parameter})
	if err != nil {
		return One{}, err
	}
	return One{prefix: segments[0], suffix: segments[1]}, nil
}

func ParseTwo(path string, first string, second string) (Two, error) {
	segments, actual, err := parse(path, []string{first, second})
	if err != nil {
		return Two{}, err
	}
	return Two{
		prefix:          segments[0],
		middle:          segments[1],
		suffix:          segments[2],
		firstThenSecond: actual[0] == first,
	}, nil
}

func (route One) Render(value string) string {
	return route.prefix + url.PathEscape(value) + route.suffix
}

// Match 仅接受参数占据一个非空路径段的完整路径，并返回解码后的参数值。
func (route One) Match(value string) (string, bool) {
	if !strings.HasPrefix(value, route.prefix) ||
		!strings.HasSuffix(value, route.suffix) {
		return "", false
	}
	parameterEnd := len(value) - len(route.suffix)
	if parameterEnd <= len(route.prefix) {
		return "", false
	}
	parameter := value[len(route.prefix):parameterEnd]
	if strings.Contains(parameter, "/") {
		return "", false
	}
	decoded, err := url.PathUnescape(parameter)
	if err != nil || decoded == "" || strings.Contains(decoded, "/") {
		return "", false
	}
	return decoded, true
}

func (route Two) Render(first string, second string) string {
	if !route.firstThenSecond {
		first, second = second, first
	}
	return route.prefix +
		url.PathEscape(first) +
		route.middle +
		url.PathEscape(second) +
		route.suffix
}

// Redact 在 value 完整匹配该路径模板时，仅替换参数段；普通路径原样返回。
func (route One) Redact(value string, label string) (string, bool) {
	if !strings.HasPrefix(value, route.prefix) ||
		!strings.HasSuffix(value, route.suffix) {
		return value, false
	}
	parameterEnd := len(value) - len(route.suffix)
	if parameterEnd <= len(route.prefix) {
		return value, false
	}
	return route.prefix + "{" + label + "}" + route.suffix, true
}

func parse(
	path string,
	parameters []string,
) ([]string, []string, error) {
	if !strings.HasPrefix(path, "/") ||
		strings.ContainsAny(path, "?#") {
		return nil, nil, errors.New("Schema HTTP 路径模板无效")
	}
	matches := pathParameterPattern.FindAllStringSubmatchIndex(path, -1)
	if len(matches) != len(parameters) {
		return nil, nil, errors.New("Schema HTTP 路径模板参数数量不匹配")
	}
	expected := make(map[string]struct{}, len(parameters))
	for _, parameter := range parameters {
		expected[parameter] = struct{}{}
	}
	actual := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	segments := make([]string, 0, len(parameters)+1)
	cursor := 0
	for _, match := range matches {
		parameter := path[match[2]:match[3]]
		if _, exists := expected[parameter]; !exists {
			return nil, nil, errors.New("Schema HTTP 路径模板参数不匹配")
		}
		if _, exists := seen[parameter]; exists {
			return nil, nil, errors.New("Schema HTTP 路径模板参数重复")
		}
		seen[parameter] = struct{}{}
		actual = append(actual, parameter)
		segments = append(segments, path[cursor:match[0]])
		cursor = match[1]
	}
	segments = append(segments, path[cursor:])
	for _, segment := range segments {
		if strings.ContainsAny(segment, "{}") {
			return nil, nil, errors.New("Schema HTTP 路径模板包含非法参数")
		}
	}
	return segments, actual, nil
}

package catalog

// Shared schema value, route and presentation validation helpers.
import (
	"fmt"
	"regexp"
	"strings"
)

func validateUniqueSchemaValues(
	scope string,
	values []string,
	allowed map[string]struct{},
) error {
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return fmt.Errorf("%s 含空值", scope)
		}
		if allowed != nil {
			if _, exists := allowed[value]; !exists {
				return fmt.Errorf("%s 含未知值: %q", scope, value)
			}
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s 值重复: %q", scope, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateUniqueWireNames(
	scope string,
	values map[string]string,
) error {
	seen := make(map[string]string, len(values))
	for name, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, " ?&=/#{}") {
			return fmt.Errorf("%s.%s 无效", scope, name)
		}
		if previous, exists := seen[value]; exists {
			return fmt.Errorf(
				"%s.%s 与 %s 重复为 %q",
				scope,
				previous,
				name,
				value,
			)
		}
		seen[value] = name
	}
	return nil
}

var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func isHexColor(value string) bool {
	return hexColorPattern.MatchString(strings.TrimSpace(value))
}

func configuredRouteParameters(route string) []string {
	var result []string
	for {
		start := strings.IndexByte(route, '{')
		if start < 0 {
			return result
		}
		route = route[start+1:]
		end := strings.IndexByte(route, '}')
		if end < 0 {
			return result
		}
		result = append(result, route[:end])
		route = route[end+1:]
	}
}

func configuredRouteParametersMatch(
	route string,
	expected []string,
) bool {
	route = strings.ReplaceAll(route, "{$}", "")
	actual := configuredRouteParameters(route)
	if len(actual) != len(expected) {
		return false
	}
	remaining := route
	required := make(map[string]struct{}, len(expected))
	for _, parameter := range expected {
		required[parameter] = struct{}{}
	}
	seen := make(map[string]struct{}, len(actual))
	for _, parameter := range actual {
		if _, exists := required[parameter]; !exists {
			return false
		}
		if _, exists := seen[parameter]; exists {
			return false
		}
		seen[parameter] = struct{}{}
		remaining = strings.ReplaceAll(
			remaining,
			"{"+parameter+"}",
			"",
		)
	}
	return !strings.ContainsAny(remaining, "{}")
}

func validateValueLabels(name string, values []ValueLabel) error {
	if len(values) == 0 {
		return fmt.Errorf("%s 不能为空", name)
	}
	seen := map[string]struct{}{}
	for index, value := range values {
		value.Value = strings.TrimSpace(value.Value)
		if value.Value == "" || strings.TrimSpace(value.Label) == "" {
			return fmt.Errorf("%s[%d] 的 value 和 label 不能为空", name, index)
		}
		if _, exists := seen[value.Value]; exists {
			return fmt.Errorf("%s value 重复: %q", name, value.Value)
		}
		seen[value.Value] = struct{}{}
	}
	return nil
}

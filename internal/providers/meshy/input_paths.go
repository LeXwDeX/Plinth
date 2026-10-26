package meshy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

var inputPathSegment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// input_paths belongs to the frozen private Offering. Public parameters remain
// flat; only this adapter translates them into the upstream JSON structure.
func validateInputPaths(offering generation.Offering, options wireOptions) error {
	if len(options.InputPaths) == 0 {
		return nil
	}
	declared := make(map[string]bool, len(offering.Parameters))
	paths := make(map[string]string, len(offering.Parameters))
	wireNames := make(map[string]string, len(offering.Parameters))
	for _, parameter := range offering.Parameters {
		declared[parameter.Name] = true
	}
	for _, parameter := range offering.Parameters {
		wireName := strings.TrimSpace(parameter.WireName)
		if wireName == "" {
			wireName = parameter.Name
		}
		if other, exists := wireNames[wireName]; exists || (wireName != parameter.Name && declared[wireName]) {
			return fmt.Errorf("input_paths 参数 %q 的扁平投影与 %q 冲突", parameter.Name, other)
		}
		wireNames[wireName] = parameter.Name
		parts := []string{wireName}
		if nested, exists := options.InputPaths[parameter.Name]; exists {
			if len(nested) < 2 || len(nested) > 8 || parameter.Type == "asset-array" {
				return fmt.Errorf("input_paths[%q] 必须有 2–8 层且不能映射 asset-array", parameter.Name)
			}
			parts = nested
		}
		for _, segment := range parts {
			if !inputPathSegment.MatchString(segment) {
				return fmt.Errorf("input_paths[%q] 包含无效路径段", parameter.Name)
			}
		}
		path := strings.Join(parts, ".")
		for existing, name := range paths {
			if path == existing || strings.HasPrefix(path, existing+".") || strings.HasPrefix(existing, path+".") {
				return fmt.Errorf("input_paths 参数 %q 与 %q 的目标路径冲突", parameter.Name, name)
			}
		}
		paths[path] = parameter.Name
	}
	for name := range options.InputPaths {
		if !declared[name] {
			return fmt.Errorf("input_paths[%q] 不是该 Offering 的参数", name)
		}
	}
	return nil
}

// Move already resolved task IDs, asset values and wire scalars together. A
// separate output prevents traversal order from silently overwriting a field.
func projectInputPaths(offering generation.Offering, options wireOptions, body map[string]any) error {
	if len(options.InputPaths) == 0 {
		return nil
	}
	type entry struct {
		path  []string
		value any
	}
	entries := make([]entry, 0, len(options.InputPaths))
	output := make(map[string]any, len(body))
	for name, value := range body {
		output[name] = value
	}
	for _, parameter := range offering.Parameters {
		parts, mapped := options.InputPaths[parameter.Name]
		if !mapped {
			continue
		}
		wireName := strings.TrimSpace(parameter.WireName)
		if wireName == "" {
			wireName = parameter.Name
		}
		value, exists := body[wireName]
		if !exists {
			continue
		}
		delete(output, wireName)
		entries = append(entries, entry{parts, value})
	}
	// Raw containers are not accepted as a second input path beside the declared
	// public parameters, even if their values happen to match.
	for _, parts := range options.InputPaths {
		if _, exists := output[parts[0]]; exists {
			return fmt.Errorf("input_paths 根路径 %q 与已有输入冲突", parts[0])
		}
	}
	for _, entry := range entries {
		current := output
		for _, segment := range entry.path[:len(entry.path)-1] {
			if current[segment] == nil {
				current[segment] = map[string]any{}
			}
			current = current[segment].(map[string]any)
		}
		current[entry.path[len(entry.path)-1]] = entry.value
	}
	clear(body)
	for name, value := range output {
		body[name] = value
	}
	return nil
}

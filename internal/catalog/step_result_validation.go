package catalog

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

// validateStepPolicyResult validates application.step_policy.result (#330 step
// 3B): the object a generation tool result carries beside its budget object.
// The code writes the fixed StepResult* property names, so the Schema must
// declare each of them with its type and a description, and must require the
// two keys every mode writes. cap and remaining are optional on purpose: only
// the cap mode writes them, because the other modes have no limit to report.
func validateStepPolicyResult(policy *StepPolicySchema) error {
	parsed, err := toolschema.Parse(policy.Result.Schema, toolschema.Options{RootObject: true})
	if err != nil {
		return fmt.Errorf("step_policy.result.schema 无效: %w", err)
	}
	if strings.TrimSpace(parsed.Description) == "" {
		return errors.New("step_policy.result.schema 缺少说明")
	}
	types := map[string]string{
		StepResultMode:      "string",
		StepResultUsed:      "integer",
		StepResultCap:       "integer",
		StepResultRemaining: "integer",
	}
	for _, name := range []string{StepResultMode, StepResultUsed, StepResultCap, StepResultRemaining} {
		property, declared := parsed.Properties[name]
		if !declared {
			return fmt.Errorf("step_policy.result.schema 缺少属性 %q（服务端会写出它）", name)
		}
		if len(property.Types) != 1 || property.Types[0] != types[name] {
			return fmt.Errorf("step_policy.result.schema 的 %s 类型必须是 %s", name, types[name])
		}
		if strings.TrimSpace(property.Description) == "" {
			return fmt.Errorf("step_policy.result.schema 的 %s 缺少说明", name)
		}
	}
	required := make(map[string]bool, len(parsed.Required))
	for _, name := range parsed.Required {
		switch name {
		case StepResultMode, StepResultUsed:
		case StepResultCap, StepResultRemaining:
			return fmt.Errorf("step_policy.result.schema 不能把 %s 列为必填：只有硬上限模式写出它", name)
		default:
			return fmt.Errorf("step_policy.result.schema 的 required 只能含 %s 与 %s，不能含 %q",
				StepResultMode, StepResultUsed, name)
		}
		required[name] = true
	}
	for _, name := range []string{StepResultMode, StepResultUsed} {
		if !required[name] {
			return fmt.Errorf("step_policy.result.schema 必须把 %s 列为必填（每种模式都写出它）", name)
		}
	}
	return nil
}

// validateStepResultField checks the steps object a generation tool result
// carries (#330 step 3B). The key is its own: a second field under the same
// key would overwrite the object. Whether a result really carries it is decided
// per call from application.step_policy.counted_capabilities and the session's
// approval mode, so a tool that declares it may still return a result without
// it.
func validateStepResultField(schema ChatSchema) error {
	for index, tool := range schema.Tools {
		key := tool.ResultFields.Steps
		if key == "" {
			continue
		}
		for _, reserved := range reservedResultKeys {
			if key == reserved {
				return fmt.Errorf("tools[%d] result_fields.steps 不能使用保留键 %q", index, key)
			}
		}
		fields := reflect.ValueOf(tool.ResultFields)
		for position := 0; position < fields.NumField(); position++ {
			name := fields.Type().Field(position).Name
			if name == "Steps" || fields.Field(position).Kind() != reflect.String {
				continue
			}
			if fields.Field(position).String() == key {
				return fmt.Errorf("tools[%d] result_fields.steps 与 %s 使用了同一个键 %q", index, name, key)
			}
		}
	}
	return nil
}

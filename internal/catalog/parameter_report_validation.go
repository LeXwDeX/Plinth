package catalog

import (
	"fmt"
	"reflect"
)

// reservedResultKeys are the tool result keys the executor writes outside
// result_fields; a declared key must not take one of them over.
var reservedResultKeys = []string{"native_quote"}

// validateTaskNoticeField checks the notices list a generation tool result
// carries (#284). Like the parameters list it is written only where the
// executor builds the result of a submitted Task, so the processing plan tool
// may not declare it, and its key must be its own, or the list would overwrite
// another field of the result.
func validateTaskNoticeField(schema ChatSchema) error {
	for index, tool := range schema.Tools {
		key := tool.ResultFields.Notices
		if key == "" {
			continue
		}
		if tool.Processing != nil && tool.Processing.Plan != nil {
			return fmt.Errorf("tools[%d] result_fields.notices 不适用于加工计划工具：计划结果不是提交任务的结果", index)
		}
		for _, reserved := range reservedResultKeys {
			if key == reserved {
				return fmt.Errorf("tools[%d] result_fields.notices 不能使用保留键 %q", index, key)
			}
		}
		fields := reflect.ValueOf(tool.ResultFields)
		for position := 0; position < fields.NumField(); position++ {
			name := fields.Type().Field(position).Name
			if name == "Notices" || fields.Field(position).Kind() != reflect.String {
				continue
			}
			if fields.Field(position).String() == key {
				return fmt.Errorf("tools[%d] result_fields.notices 与 %s 使用了同一个键 %q", index, name, key)
			}
		}
	}
	return nil
}

// validateParameterReport checks the parameters list a generation tool result
// carries (#296). A tool may declare result_fields.parameters only where the
// executor writes it: every tool that submits a Task, never the processing
// plan tool, whose result is built from the plan. The key must be its own: a
// second field under the same key would overwrite the list. The list holds
// only values that differ from the Offering's default, and the Schema bounds
// one value, so a Catalog that declares the key needs a positive bound.
func validateParameterReport(schema ChatSchema) error {
	maximum := schema.Agent.ParameterReport.MaximumValueCharacters
	if maximum < 0 {
		return fmt.Errorf("chat.agent.parameter_report.maximum_value_characters 不能为负数")
	}
	for index, tool := range schema.Tools {
		key := tool.ResultFields.Parameters
		if key == "" {
			continue
		}
		if tool.Processing != nil && tool.Processing.Plan != nil {
			return fmt.Errorf("tools[%d] result_fields.parameters 不适用于加工计划工具：计划结果不是提交任务的结果", index)
		}
		if maximum == 0 {
			return fmt.Errorf("tools[%d] 声明了 result_fields.parameters，chat.agent.parameter_report.maximum_value_characters 必须大于 0", index)
		}
		for _, reserved := range reservedResultKeys {
			if key == reserved {
				return fmt.Errorf("tools[%d] result_fields.parameters 不能使用保留键 %q", index, key)
			}
		}
		fields := reflect.ValueOf(tool.ResultFields)
		for position := 0; position < fields.NumField(); position++ {
			name := fields.Type().Field(position).Name
			if name == "Parameters" || fields.Field(position).Kind() != reflect.String {
				continue
			}
			if fields.Field(position).String() == key {
				return fmt.Errorf("tools[%d] result_fields.parameters 与 %s 使用了同一个键 %q", index, name, key)
			}
		}
	}
	return nil
}

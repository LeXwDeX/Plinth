package tripo

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// An Offering whose selected formats are exported through private models/convert
// jobs declares how those requests are written under
// provider_options.native_conversion. The generation Offerings (#278) and the
// processing Offerings (#292) share the declaration:
//
//   - parameters are constant request fields every export carries;
//   - bindings forward one public Offering parameter of the frozen input as one
//     request field, for every export or only for the listed formats (the
//     texture format, the texture size, the pivot and the FBX preset a purpose
//     chooses).
//
// A bound parameter belongs to the exports only: it is never sent with the
// generation or processing request. A binding that names the upstream default of
// its field leaves the field out while the value equals it, so a generation
// export that asks for nothing special is exactly the {input, format} request it
// has always been, and the upstream bills it as a basic conversion (its price
// page bills only non-default values as advanced). An Offering without the
// declaration, including every Task frozen before it existed, keeps that request
// too. Both fields are declarations of the frozen Offering, so a Task keeps the
// export options it was accepted with.
type nativeConversionBinding struct {
	Parameter string   `json:"parameter"`
	Wire      string   `json:"wire"`
	Formats   []string `json:"formats,omitempty"`
	// UpstreamDefault is the scalar the upstream applies when the field is
	// absent; empty means the field is always sent when the input has a value.
	UpstreamDefault json.RawMessage `json:"upstream_default,omitempty"`
}

// reservedConversionFields are written by the adapter itself.
var reservedConversionFields = []string{"input", "format", "model"}

func conversionDeclarationError(detail string) error {
	return failure(generation.FailureProtocol, false, "Tripo 生成转换声明无效: "+detail)
}

// generationConversion returns the export request options a generation Offering
// declares, or nil when it declares none. A declaration that is malformed, or
// that has the shape of a processing Offering's, is refused before anything is
// sent so a paid generation never ends in an export that cannot be written.
func generationConversion(offering generation.Offering, options providerOptions) (*nativeConversionOptions, error) {
	c := options.NativeConversion
	if c == nil {
		return nil, nil
	}
	if c.SourceAlias != "" || c.MaxSourceBytes != 0 || len(c.Formats) != 0 {
		return nil, conversionDeclarationError("native_conversion 带有原生处理的来源或格式声明")
	}
	if len(c.Parameters) == 0 && len(c.Bindings) == 0 {
		return nil, conversionDeclarationError("native_conversion 没有声明任何请求字段")
	}
	for name, raw := range c.Parameters {
		if slices.Contains(reservedConversionFields, name) {
			return nil, conversionDeclarationError("native_conversion.parameters 含保留字段 " + name)
		}
		if _, err := conversionScalar(raw); err != nil {
			return nil, conversionDeclarationError("native_conversion.parameters." + name + " 不是标量")
		}
	}
	// The formats a binding may be limited to are the options of the selection.
	selection := offering.PriceSelectionParameter()
	var formats []string
	for _, parameter := range offering.Parameters {
		if parameter.Name == selection {
			for _, option := range parameter.Options {
				formats = append(formats, option.Value)
			}
		}
	}
	if detail := conversionBindingsDetail(offering, c, formats); detail != "" {
		return nil, conversionDeclarationError(detail)
	}
	return c, nil
}

// conversionBindingsDetail says what is wrong with the bindings of a
// declaration, or returns "" when they can all be written: every bound request
// field is its own (not a reserved one, not a constant of the declaration, not
// another binding's), every bound parameter is a scalar of the Offering that is
// neither the format selection nor the consent, every format a binding is
// limited to is one the exports produce, and a select option the wire maps has a
// scalar upstream value. formats are the export formats the declaration produces.
func conversionBindingsDetail(offering generation.Offering, c *nativeConversionOptions, formats []string) string {
	wires := map[string]bool{}
	for name := range c.Parameters {
		wires[name] = true
	}
	selection := offering.PriceSelectionParameter()
	bound := map[string]bool{}
	for _, binding := range c.Bindings {
		if binding.Wire == "" || slices.Contains(reservedConversionFields, binding.Wire) || wires[binding.Wire] {
			return "绑定的请求字段为空、保留或重复: " + binding.Wire
		}
		wires[binding.Wire] = true
		if bound[binding.Parameter] {
			return "参数被绑定了不止一次: " + binding.Parameter
		}
		bound[binding.Parameter] = true
		var parameter *generation.Parameter
		for index := range offering.Parameters {
			if offering.Parameters[index].Name == binding.Parameter {
				parameter = &offering.Parameters[index]
			}
		}
		if parameter == nil || parameter.Name == selection || parameter.Name == offering.ExtraConsentParameter() {
			return "绑定的参数不存在或是格式选择、同意参数: " + binding.Parameter
		}
		switch parameter.Type {
		case "boolean", "number", "select", "text":
		default:
			return "绑定的参数类型不是标量: " + binding.Parameter
		}
		for _, format := range binding.Formats {
			if !slices.Contains(formats, format) {
				return "绑定的格式不在可选范围内: " + format
			}
		}
		if len(binding.UpstreamDefault) > 0 {
			if _, err := conversionScalar(binding.UpstreamDefault); err != nil {
				return "绑定的上游默认值不是标量: " + binding.Parameter
			}
		}
		for _, option := range parameter.Options {
			if len(parameter.WireValues) == 0 {
				break
			}
			mapped, declared := parameter.WireValues[option.Value]
			if !declared {
				return "选项没有上游取值: " + binding.Parameter + "=" + option.Value
			}
			if _, err := conversionScalar(mapped); err != nil {
				return "选项的上游取值不是标量: " + binding.Parameter + "=" + option.Value
			}
		}
	}
	return ""
}

// conversionScalar decodes a declared request value: a string, number or
// boolean.
func conversionScalar(raw json.RawMessage) (any, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	switch value.(type) {
	case string, bool, float64:
		return value, nil
	}
	return nil, fmt.Errorf("not a scalar")
}

// conversionBoundParameter reports whether the Offering sends a parameter with
// its exports instead of with the generation or processing request.
func conversionBoundParameter(options providerOptions, name string) bool {
	if options.NativeConversion == nil {
		return false
	}
	for _, binding := range options.NativeConversion.Bindings {
		if binding.Parameter == name {
			return true
		}
	}
	return false
}

// conversionExtras are the request fields, besides input and format, of the
// export of one format of a generation Offering: the declared constants and the
// frozen values of the parameters bound to that format. Nil means the Offering
// declares none; an empty map, that this export asks for nothing beyond input
// and format.
func conversionExtras(
	offering generation.Offering,
	options providerOptions,
	input json.RawMessage,
	format string,
) (map[string]any, error) {
	c, err := generationConversion(offering, options)
	if c == nil || err != nil {
		return nil, err
	}
	extras := make(map[string]any, len(c.Parameters))
	for name, raw := range c.Parameters {
		extras[name], _ = conversionScalar(raw)
	}
	bound, err := conversionBoundValues(offering, c, input, format)
	if err != nil {
		return nil, err
	}
	for name, value := range bound {
		extras[name] = value
	}
	return extras, nil
}

// conversionBoundValues are the request fields the bindings write for the
// export of one format, read from the frozen input. A bound option with a
// wire_values mapping is sent as the mapped value, and a value equal to its
// binding's upstream default is left out.
func conversionBoundValues(
	offering generation.Offering,
	c *nativeConversionOptions,
	input json.RawMessage,
	format string,
) (map[string]any, error) {
	var values map[string]any
	if json.Unmarshal(input, &values) != nil {
		return nil, failure(generation.FailureInvalidInput, false, "规范化输入不是 JSON 对象")
	}
	bound := map[string]any{}
	for _, binding := range c.Bindings {
		if len(binding.Formats) > 0 && !slices.Contains(binding.Formats, format) {
			continue
		}
		value, present := values[binding.Parameter]
		if !present {
			continue
		}
		for _, parameter := range offering.Parameters {
			if parameter.Name != binding.Parameter || len(parameter.WireValues) == 0 {
				continue
			}
			selected, isText := value.(string)
			mapped, declared := parameter.WireValues[selected]
			if !isText || !declared {
				return nil, failure(generation.FailureInvalidInput, false, "转换参数的上游取值映射无效")
			}
			value, _ = conversionScalar(mapped)
		}
		switch value.(type) {
		case string, bool, float64:
		default:
			return nil, failure(generation.FailureInvalidInput, false, "转换参数取值不是标量")
		}
		// The upstream default is what an absent field means: say nothing.
		if len(binding.UpstreamDefault) > 0 {
			if upstream, err := conversionScalar(binding.UpstreamDefault); err == nil && upstream == value {
				continue
			}
		}
		bound[binding.Wire] = value
	}
	return bound, nil
}

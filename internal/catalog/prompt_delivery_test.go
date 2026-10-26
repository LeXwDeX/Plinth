package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func promptDeliveryOffering(promptMax int) schemacontract.Offering {
	return schemacontract.Offering{
		ID: "test/prompt-delivery",
		Parameters: []schemacontract.Parameter{
			{Name: "prompt", Type: "text", Semantic: schemacontract.SemanticPrompt, MaxLength: promptMax},
			{
				Name:    "size",
				Type:    "select",
				Default: json.RawMessage(`"1024x1024"`),
				Options: []schemacontract.ParameterOption{
					{Value: "1024x1024", Label: "方图"},
					{Value: "1536x1024", Label: "横图"},
				},
				PromptDelivery: &schemacontract.PromptDelivery{Template: "画面尺寸：{value}"},
				OmitWhen: []schemacontract.ParameterCondition{
					{Parameter: "skip", Operator: "truthy"},
				},
			},
			{Name: "skip", Type: "boolean"},
		},
	}
}

func normalizeToMap(t *testing.T, offering schemacontract.Offering, input string) (map[string]any, error) {
	t.Helper()
	raw, err := normalizeInput(offering, json.RawMessage(input))
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	return values, nil
}

func TestPromptDeliveryAppendsTemplateAndKeepsParameterOffWire(t *testing.T) {
	offering := promptDeliveryOffering(0)
	raw, err := normalizeInput(offering, json.RawMessage(`{"prompt":"一只猫","size":"1536x1024"}`))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	if values["prompt"] != "一只猫\n画面尺寸：1536x1024" {
		t.Errorf("prompt = %q", values["prompt"])
	}
	if values["size"] != "1536x1024" {
		t.Errorf("normalized input must keep the chosen size, got %v", values["size"])
	}
	// 准备输入与提交会再次规范化并比较结果：必须幂等。
	again, err := normalizeInput(offering, raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(raw) {
		t.Errorf("normalization not idempotent:\n first %s\nsecond %s", raw, again)
	}
	var wire map[string]any
	if err := json.Unmarshal(schemacontract.WireInput(offering, raw), &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["size"]; exists {
		t.Error("delivered parameter must not reach the provider")
	}
	if wire["prompt"] != values["prompt"] {
		t.Errorf("wire prompt = %q", wire["prompt"])
	}
}

func TestPromptDeliveryUsesDefaultWhenUnchosen(t *testing.T) {
	values, err := normalizeToMap(t, promptDeliveryOffering(0), `{"prompt":"一只猫"}`)
	if err != nil {
		t.Fatal(err)
	}
	if values["prompt"] != "一只猫\n画面尺寸：1024x1024" {
		t.Errorf("prompt = %q", values["prompt"])
	}
}

func TestPromptDeliverySkipsOmittedParameter(t *testing.T) {
	values, err := normalizeToMap(t, promptDeliveryOffering(0), `{"prompt":"一只猫","skip":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if values["prompt"] != "一只猫" {
		t.Errorf("prompt = %q, want unchanged", values["prompt"])
	}
}

func TestPromptDeliveryRevalidatesPromptLength(t *testing.T) {
	if _, err := normalizeToMap(t, promptDeliveryOffering(10), `{"prompt":"一只猫"}`); err == nil {
		t.Fatal("expected max_length error after append")
	}
	if _, err := normalizeToMap(t, promptDeliveryOffering(30), `{"prompt":"一只猫"}`); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePromptDeliveryDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*schemacontract.Offering)
		want   string
	}{
		{"valid", func(*schemacontract.Offering) {}, ""},
		{"missing {value}", func(o *schemacontract.Offering) {
			o.Parameters[1].PromptDelivery.Template = "画面尺寸"
		}, "{value}"},
		{"empty template", func(o *schemacontract.Offering) {
			o.Parameters[1].PromptDelivery.Template = ""
		}, "{value}"},
		{"no prompt parameter", func(o *schemacontract.Offering) {
			o.Parameters[0].Semantic = ""
		}, "恰有一个提示词参数"},
		{"delivered is prompt", func(o *schemacontract.Offering) {
			o.Parameters[0].PromptDelivery = &schemacontract.PromptDelivery{Template: "{value}"}
		}, "自身是提示词参数"},
		{"two delivered parameters", func(o *schemacontract.Offering) {
			o.Parameters[2].PromptDelivery = &schemacontract.PromptDelivery{Template: "跳过：{value}"}
		}, "最多只能有一个"},
		{"asset type", func(o *schemacontract.Offering) {
			o.Parameters[1].Type = "asset"
		}, "不能声明 prompt_delivery"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offering := promptDeliveryOffering(0)
			tc.mutate(&offering)
			var err error
			for _, parameter := range offering.Parameters {
				if err = validatePromptDelivery(offering, parameter); err != nil {
					break
				}
			}
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

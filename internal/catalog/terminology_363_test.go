package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestTerminologyValidateCopy(t *testing.T) {
	terminology := &TerminologySchema{UserFacing: []TerminologyTermSchema{
		{Concept: "offering", Preferred: "服务", Rejected: []string{"", "OfFeRiNg", "Package"}},
		{Concept: "service", Preferred: "Service", Rejected: []string{"供给项", "ÄLT"}},
	}}
	cases := []struct {
		name        string
		terminology *TerminologySchema
		text        string
		want        string
	}{
		{name: "nil terminology", text: "OFFERING"},
		{name: "nil terms", terminology: &TerminologySchema{}, text: "OFFERING"},
		{name: "empty terms", terminology: &TerminologySchema{UserFacing: []TerminologyTermSchema{}}, text: "OFFERING"},
		{name: "empty copy", terminology: terminology},
		{name: "allowed copy and empty alias", terminology: terminology, text: "模型服务已就绪"},
		{
			name:        "alias order before text position",
			terminology: terminology,
			text:        "PACKAGE before an OFFERING",
			want:        `test.copy 使用禁用术语 "OfFeRiNg"；offering 的规范术语是 "服务"`,
		},
		{
			name:        "term order before text position",
			terminology: terminology,
			text:        "供给项 before a pAcKaGe",
			want:        `test.copy 使用禁用术语 "Package"；offering 的规范术语是 "服务"`,
		},
		{
			name:        "Unicode case folding in substring",
			terminology: terminology,
			text:        "choose ält now",
			want:        `test.copy 使用禁用术语 "ÄLT"；service 的规范术语是 "Service"`,
		},
		{
			name:        "Chinese substring",
			terminology: terminology,
			text:        "请选择供给项。",
			want:        `test.copy 使用禁用术语 "供给项"；service 的规范术语是 "Service"`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.terminology.validateCopy(terminologyCopy{path: "test.copy", text: testCase.text})
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("allowed copy rejected: %v", err)
				}
			} else if err == nil || err.Error() != testCase.want {
				t.Fatalf("validateCopy() = %v, want %s", err, testCase.want)
			}
		})
	}
}

func BenchmarkTerminologyValidateCopy(b *testing.B) {
	product, err := LoadDefault()
	if err != nil {
		b.Fatal(err)
	}
	terminology := product.Application().Terminology
	copy := terminologyCopy{path: "test.copy", text: strings.Repeat("MODEL 服务已准备就绪，请选择模型。", 4)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := terminology.validateCopy(copy); err != nil {
			b.Fatal(err)
		}
	}
}

// #363: one user-facing term per concept. The default catalog passes; the
// guard rejects the aliases a person would otherwise read in UI copy.
func TestDefaultCatalogDeclaresUserFacingTerminology(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	terminology := product.Application().Terminology
	if terminology == nil {
		t.Fatal("default catalog does not declare application.terminology")
	}
	concepts := map[string]string{}
	for _, term := range terminology.UserFacing {
		concepts[term.Concept] = term.Preferred
	}
	for concept, preferred := range map[string]string{
		"offering":  "服务",
		"service":   "Service",
		"retexture": "换贴图",
		"rigging":   "绑骨",
		"remesh":    "重拓扑",
	} {
		if concepts[concept] != preferred {
			t.Fatalf("terminology %q = %q, want %q", concept, concepts[concept], preferred)
		}
	}
}

func TestTerminologyRejectsBannedUserFacingCopy(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(document map[string]any)
		want   string
	}{
		{
			name: "retexture alias in a message",
			mutate: func(document map[string]any) {
				applicationMessages(document)["ui.retexture.empty"] = "输入模型后重新贴图"
			},
			want: "重新贴图",
		},
		{
			name: "offering alias in a message",
			mutate: func(document map[string]any) {
				applicationMessages(document)["ui.workspace.processing.plan_submit_rejected"] = "处理步骤未能提交，请检查参数或供给项。"
			},
			want: "供给项",
		},
		{
			name: "rigging alias in a capability label",
			mutate: func(document map[string]any) {
				capabilityDocument(t, document, "rigging")["label_cn"] = "骨骼绑定"
			},
			want: "骨骼绑定",
		},
		{
			name: "remesh alias in a message",
			mutate: func(document map[string]any) {
				applicationMessages(document)["ui.remesh.title"] = "重新拓扑"
			},
			want: "重新拓扑",
		},
		{
			name: "offering in an English message",
			mutate: func(document map[string]any) {
				englishBundle(document)["messages"].(map[string]any)["ui.format_conversion.unavailable"] = "No format conversion offering is currently available"
			},
			want: "offering",
		},
		{
			name: "offering in an English field",
			mutate: func(document map[string]any) {
				englishBundle(document)["fields"].(map[string]any)["chat.harness.operations[id=mesh-segmentation].description"] = "Split a model into separate parts; executable only when a compatible Offering is enabled."
			},
			want: "offering",
		},
		{
			name: "offering alias in a pending approval message",
			mutate: func(document map[string]any) {
				chat := applicationDocument(document)["chat"].(map[string]any)
				pending := chat["pending_approvals"].(map[string]any)
				messages := pending["messages"].(map[string]any)
				messages["denied_native_extra"] = messages["denied_native_extra"].(string) + " 供给项"
			},
			want: "供给项",
		},
		{
			name: "offering in a format conversion reason",
			mutate: func(document map[string]any) {
				reasons := applicationDocument(document)["model_delivery"].(map[string]any)["reasons"].(map[string]any)
				reasons["offering_unavailable"] = "格式转换 offering 当前不可用"
			},
			want: "offering",
		},
		{
			name: "supply alias in the default harness operation",
			mutate: func(document map[string]any) {
				chat := applicationDocument(document)["chat"].(map[string]any)
				harness := chat["harness"].(map[string]any)
				for _, raw := range harness["operations"].([]any) {
					operation := raw.(map[string]any)
					if operation["id"] == "mesh-segmentation" {
						operation["description"] = "将模型拆分为独立部件；仅在有兼容且已启用的供给项时可执行。"
						return
					}
				}
				t.Fatal("mesh-segmentation operation missing")
			},
			want: "供给项",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, testCase.mutate)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("banned user-facing term accepted or not named: %v", err)
			}
		})
	}
}

// The architecture word "offering" may stay in prompts and tool descriptions:
// only the UI copy is part of the terminology contract.
func TestTerminologyLeavesInternalPromptWordingAlone(t *testing.T) {
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		chat := applicationDocument(document)["chat"].(map[string]any)
		harness := chat["harness"].(map[string]any)
		harness["context_template"] = harness["context_template"].(string) + " 当前选择的供给项或 offering。"
		tools := chat["tools"].([]any)
		tool := tools[0].(map[string]any)
		tool["description"] = tool["description"].(string) + " The offering keeps its internal identity."
		agent := chat["agent"].(map[string]any)
		agent["system_prompt"] = agent["system_prompt"].(string) + " 报告每一步供给项。"
	})
	if err != nil {
		t.Fatalf("prompt wording rejected by the UI terminology guard: %v", err)
	}
}

func TestTerminologyRejectsOfferingCopy(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(document map[string]any)
		want   string
	}{
		{
			name: "parameter description",
			mutate: func(document map[string]any) {
				offering := offeringDocumentByID(t, document, "openai/gpt-image-2/text-to-image")
				quality := parameterDocument(t, offering, "quality")
				quality["description"] = "Choose image quality; 重新贴图"
			},
			want: "重新贴图",
		},
		{
			name: "provider failure message",
			mutate: func(document map[string]any) {
				offering := offeringDocumentByID(t, document, "matting/default/background-removal")
				options := offering["provider_options"].(map[string]any)
				options["messages"].(map[string]any)["invalid_assets"] = "背景去除需要符合 Offering 数量约束"
			},
			want: "offering",
		},
		{
			name: "translated Offering copy",
			mutate: func(document map[string]any) {
				englishOfferingTexts(document)["纹理随机种子"] = "Texture seed used by this offering"
			},
			want: "offering",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := mutateDefaultCatalog(t, testCase.mutate)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("banned Offering copy accepted or not named: %v", err)
			}
		})
	}
}

// Technical product names stay valid: Meshy Remesh and Rigging are names, not
// the rejected Chinese aliases.
func TestTerminologyAllowsTechnicalProductNames(t *testing.T) {
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		applicationMessages(document)["error.legacy_remesh_invalid_extension"] = "不支持的模型格式 {extension}，Meshy Remesh 仅接受 {extensions}"
		applicationMessages(document)["ui.remesh.title"] = "Remesh"
		capabilityDocument(t, document, "rigging")["label_cn"] = "Rigging"
	})
	if err != nil {
		t.Fatalf("technical product name rejected: %v", err)
	}
}

func TestTerminologyRejectsInvalidDeclarations(t *testing.T) {
	cases := []struct {
		name        string
		terminology *TerminologySchema
		want        string
	}{
		{
			name:        "no user-facing terms",
			terminology: &TerminologySchema{},
			want:        "至少声明一个术语",
		},
		{
			name: "empty preferred",
			terminology: &TerminologySchema{UserFacing: []TerminologyTermSchema{{
				Concept:   "offering",
				Preferred: " ",
				Rejected:  []string{"供给项"},
			}}},
			want: "preferred 不能为空",
		},
		{
			name: "empty rejected",
			terminology: &TerminologySchema{UserFacing: []TerminologyTermSchema{{
				Concept:   "offering",
				Preferred: "服务",
			}}},
			want: "rejected 不能为空",
		},
		{
			name: "rejected equals preferred",
			terminology: &TerminologySchema{UserFacing: []TerminologyTermSchema{{
				Concept:   "service",
				Preferred: "Service",
				Rejected:  []string{"service"},
			}}},
			want: "不能与 preferred",
		},
		{
			name: "duplicate concept",
			terminology: &TerminologySchema{UserFacing: []TerminologyTermSchema{
				{Concept: "offering", Preferred: "服务", Rejected: []string{"供给项"}},
				{Concept: "offering", Preferred: "项目", Rejected: []string{"条目"}},
			}},
			want: "重复",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			application := defaultApplicationForLocalization(t)
			application.Terminology = testCase.terminology
			err := ValidateApplicationSchema(application)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("invalid terminology declaration accepted or not named: %v", err)
			}
		})
	}
}

func TestPublicProjectionOmitsTerminology(t *testing.T) {
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(product.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"terminology"`)) {
		t.Fatal("public application projection exposes the server-side terminology declaration")
	}
}

func applicationDocument(document map[string]any) map[string]any {
	application, _ := document["application"].(map[string]any)
	return application
}

func applicationMessages(document map[string]any) map[string]any {
	content, _ := applicationDocument(document)["content"].(map[string]any)
	messages, _ := content["messages"].(map[string]any)
	return messages
}

func englishBundle(document map[string]any) map[string]any {
	localization, _ := applicationDocument(document)["localization"].(map[string]any)
	bundles, _ := localization["bundles"].(map[string]any)
	bundle, _ := bundles["en"].(map[string]any)
	return bundle
}

func capabilityDocument(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	capabilities, _ := applicationDocument(document)["capabilities"].([]any)
	for _, raw := range capabilities {
		capability, ok := raw.(map[string]any)
		if ok && capability["id"] == id {
			return capability
		}
	}
	t.Fatalf("capability %q not found", id)
	return nil
}

// #363 review: every task cancellation string is user-facing, not only the
// unsupported case.
func TestTerminologyRejectsEveryCancellationString(t *testing.T) {
	for _, field := range []string{"local_only", "requesting", "confirmed", "failed", "unsupported", "unknown"} {
		t.Run(field, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				runtime := applicationDocument(document)["runtime"].(map[string]any)
				cancellation := runtime["task_cancellation"].(map[string]any)
				cancellation[field] = cancellation[field].(string) + " 供给项"
			})
			if err == nil || !strings.Contains(err.Error(), "供给项") {
				t.Fatalf("banned term in task_cancellation.%s accepted or not named: %v", field, err)
			}
		})
	}
}

// #363 review: runtime.generation_messages reaches users as failure and
// refusal text, so it is part of the same terminology guard.
func TestTerminologyRejectsGenerationMessageCopy(t *testing.T) {
	err := mutateDefaultCatalog(t, func(document map[string]any) {
		runtime := applicationDocument(document)["runtime"].(map[string]any)
		messages := runtime["generation_messages"].(map[string]any)
		messages["reference_limit"] = messages["reference_limit"].(string) + " 供给项"
	})
	if err == nil || !strings.Contains(err.Error(), "供给项") {
		t.Fatalf("banned term in runtime.generation_messages accepted or not named: %v", err)
	}
}

// #363 review: the localized copies of cancellation and generation messages
// are user-facing too.
func TestTerminologyRejectsLocalizedCancellationAndGenerationCopy(t *testing.T) {
	for _, path := range []string{
		"runtime.task_cancellation.requesting",
		"runtime.generation_messages.reference_limit",
	} {
		t.Run(path, func(t *testing.T) {
			err := mutateDefaultCatalog(t, func(document map[string]any) {
				fields := englishBundle(document)["fields"].(map[string]any)
				fields[path] = fields[path].(string) + " offering"
			})
			if err == nil || !strings.Contains(err.Error(), "offering") {
				t.Fatalf("banned term in localized %s accepted or not named: %v", path, err)
			}
		})
	}
}

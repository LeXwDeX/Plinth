package catalog

// User-facing terminology: one declared preferred word per concept and the
// aliases Catalog loading rejects. The declaration is server-side only; the
// browser reads the resolved copy, not the glossary.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// TerminologySchema is application.terminology.
type TerminologySchema struct {
	UserFacing []TerminologyTermSchema `json:"user_facing"`
}

// TerminologyTermSchema is one user-facing concept.
type TerminologyTermSchema struct {
	Concept   string   `json:"concept"`
	Preferred string   `json:"preferred"`
	Rejected  []string `json:"rejected"`
}

type terminologyCopy struct {
	path string
	text string
}

func validateTerminologySchema(terminology *TerminologySchema) error {
	if terminology == nil {
		return nil
	}
	if len(terminology.UserFacing) == 0 {
		return fmt.Errorf("user_facing 至少声明一个术语")
	}
	concepts := map[string]bool{}
	aliases := map[string]string{}
	for index, term := range terminology.UserFacing {
		at := fmt.Sprintf("user_facing[%d]", index)
		concept := strings.TrimSpace(term.Concept)
		if concept == "" {
			return fmt.Errorf("%s 的 concept 不能为空", at)
		}
		if concepts[concept] {
			return fmt.Errorf("%s 的 concept %q 重复", at, concept)
		}
		concepts[concept] = true
		preferred := strings.TrimSpace(term.Preferred)
		if preferred == "" {
			return fmt.Errorf("%s 的 preferred 不能为空", at)
		}
		if len(term.Rejected) == 0 {
			return fmt.Errorf("%s 的 rejected 不能为空；无需拒绝别名时请删除该术语", at)
		}
		for aliasIndex, alias := range term.Rejected {
			if alias == "" || strings.TrimSpace(alias) != alias {
				return fmt.Errorf("%s.rejected[%d] 不能为空或含首尾空白", at, aliasIndex)
			}
			if strings.EqualFold(alias, preferred) {
				return fmt.Errorf("%s.rejected[%d] 不能与 preferred %q 相同", at, aliasIndex, preferred)
			}
			key := strings.ToLower(alias)
			if previous, exists := aliases[key]; exists && previous != concept {
				return fmt.Errorf("%s.rejected[%d] 的别名 %q 已在术语 %q 中声明", at, aliasIndex, alias, previous)
			}
			aliases[key] = concept
		}
	}
	return nil
}

func (terminology *TerminologySchema) validateCopy(copy terminologyCopy) error {
	if terminology == nil || copy.text == "" {
		return nil
	}
	lowerText := strings.ToLower(copy.text)
	for _, term := range terminology.UserFacing {
		for _, alias := range term.Rejected {
			if alias == "" || !strings.Contains(lowerText, strings.ToLower(alias)) {
				continue
			}
			return fmt.Errorf(
				"%s 使用禁用术语 %q；%s 的规范术语是 %q",
				copy.path,
				alias,
				term.Concept,
				term.Preferred,
			)
		}
	}
	return nil
}

// validateApplicationTerminology checks only copy the person reads. System
// prompts, tool descriptions and chat.harness.context_template stay outside
// this guard: they may name the architecture concept "offering" on purpose.
// Scope is the fields collected by applicationTerminologyCopy and the
// localized fields matching isUserFacingTerminologyField; it does not try to
// classify every Schema string. Default-locale structured copy outside
// Content.Messages (navigation, brand, home) and other runtime presentation
// messages are a known coverage gap. Offering translations run separately
// after stale/missing detection in validateLocalizedOfferingTerminology.
func validateApplicationTerminology(schema ApplicationSchema) error {
	if schema.Terminology == nil {
		return nil
	}
	for _, copy := range applicationTerminologyCopy(schema) {
		if err := schema.Terminology.validateCopy(copy); err != nil {
			return err
		}
	}
	if schema.Localization == nil {
		return nil
	}
	locales := make([]string, 0, len(schema.Localization.Bundles))
	for locale := range schema.Localization.Bundles {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	for _, locale := range locales {
		bundle := schema.Localization.Bundles[locale]
		for _, key := range sortedStringMapKeys(bundle.Messages) {
			if err := schema.Terminology.validateCopy(terminologyCopy{
				path: fmt.Sprintf("localization.bundles.%s.messages[%q]", locale, key),
				text: bundle.Messages[key],
			}); err != nil {
				return err
			}
		}
		for _, path := range sortedStringMapKeys(bundle.Fields) {
			if !isUserFacingTerminologyField(path) {
				continue
			}
			if err := schema.Terminology.validateCopy(terminologyCopy{
				path: fmt.Sprintf("localization.bundles.%s.fields[%q]", locale, path),
				text: bundle.Fields[path],
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateLocalizedOfferingTerminology runs after the stale/missing
// offering_texts checks, so a translation that no Offering uses still reports
// as stale rather than as a terminology violation.
func validateLocalizedOfferingTerminology(
	terminology *TerminologySchema,
	localization *LocalizationSchema,
) error {
	if terminology == nil || localization == nil {
		return nil
	}
	locales := make([]string, 0, len(localization.Bundles))
	for locale := range localization.Bundles {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	for _, locale := range locales {
		texts := localization.Bundles[locale].OfferingTexts
		for _, source := range sortedStringMapKeys(texts) {
			for _, copy := range []terminologyCopy{
				{
					path: fmt.Sprintf("bundles.%s.offering_texts[%q]（原文）", locale, source),
					text: source,
				},
				{
					path: fmt.Sprintf("bundles.%s.offering_texts[%q]（译文）", locale, source),
					text: texts[source],
				},
			} {
				if err := terminology.validateCopy(copy); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func applicationTerminologyCopy(schema ApplicationSchema) []terminologyCopy {
	copies := make([]terminologyCopy, 0, len(schema.Content.Messages)+32)
	for _, key := range sortedStringMapKeys(schema.Content.Messages) {
		copies = append(copies, terminologyCopy{
			path: fmt.Sprintf("content.messages[%q]", key),
			text: schema.Content.Messages[key],
		})
	}
	for _, capability := range schema.Capabilities {
		copies = append(copies,
			terminologyCopy{
				path: fmt.Sprintf("capabilities[%s].label", capability.ID),
				text: capability.Label,
			},
			terminologyCopy{
				path: fmt.Sprintf("capabilities[%s].label_cn", capability.ID),
				text: capability.LabelCN,
			},
		)
	}
	copies = append(copies, jsonTerminologyCopy("model_delivery.reasons", schema.ModelDelivery.Reasons)...)
	cancellation := schema.Runtime.TaskCancellation
	copies = append(copies,
		terminologyCopy{"runtime.task_cancellation.local_only", cancellation.LocalOnly},
		terminologyCopy{"runtime.task_cancellation.requesting", cancellation.Requesting},
		terminologyCopy{"runtime.task_cancellation.confirmed", cancellation.Confirmed},
		terminologyCopy{"runtime.task_cancellation.failed", cancellation.Failed},
		terminologyCopy{"runtime.task_cancellation.unsupported", cancellation.Unsupported},
		terminologyCopy{"runtime.task_cancellation.unknown", cancellation.Unknown},
	)
	copies = append(copies, jsonTerminologyCopy("runtime.generation_messages", schema.Runtime.GenerationMessages)...)
	copies = append(copies, jsonTerminologyCopy("chat.presentation", schema.Chat.Presentation)...)
	copies = append(copies, jsonTerminologyCopy("chat.pending_approvals", schema.Chat.PendingApprovals)...)
	copies = append(copies, harnessTerminologyCopy(schema.Chat.Harness)...)
	return copies
}

// harnessTerminologyCopy collects the Harness fields the person reads. It
// deliberately skips context_template and conversation_input_description.
func harnessTerminologyCopy(harness ChatHarnessSchema) []terminologyCopy {
	copies := []terminologyCopy{
		{"chat.harness.source_clarification_message", harness.SourceClarificationMessage},
		{"chat.harness.rejection_message", harness.RejectionMessage},
		{"chat.harness.changed_message", harness.ChangedMessage},
		{"chat.harness.missing_input_message", harness.MissingInputMessage},
		{"chat.harness.unavailable_message", harness.UnavailableMessage},
		{"chat.harness.proposal_message", harness.ProposalMessage},
		{"chat.harness.missing_conversation_input_message", harness.MissingConversationInputMessage},
		{"chat.harness.missing_parameters_message", harness.MissingParametersMessage},
	}
	for _, kind := range harness.SourceKinds {
		copies = append(copies,
			terminologyCopy{fmt.Sprintf("chat.harness.source_kinds[%s].label", kind.ID), kind.Label},
			terminologyCopy{fmt.Sprintf("chat.harness.source_kinds[%s].description", kind.ID), kind.Description},
		)
	}
	for _, mode := range harness.TaskSourceModes {
		copies = append(copies,
			terminologyCopy{fmt.Sprintf("chat.harness.task_source_modes[%s].label", mode.ID), mode.Label},
			terminologyCopy{fmt.Sprintf("chat.harness.task_source_modes[%s].description", mode.ID), mode.Description},
		)
	}
	for _, group := range harness.Groups {
		copies = append(copies,
			terminologyCopy{fmt.Sprintf("chat.harness.groups[%s].label", group.ID), group.Label},
			terminologyCopy{fmt.Sprintf("chat.harness.groups[%s].empty_message", group.ID), group.EmptyMessage},
		)
	}
	for _, operation := range harness.Operations {
		copies = append(copies,
			terminologyCopy{fmt.Sprintf("chat.harness.operations[%s].label", operation.ID), operation.Label},
			terminologyCopy{fmt.Sprintf("chat.harness.operations[%s].description", operation.ID), operation.Description},
			terminologyCopy{fmt.Sprintf("chat.harness.operations[%s].unavailable_reason", operation.ID), operation.UnavailableReason},
			terminologyCopy{fmt.Sprintf("chat.harness.operations[%s].uploaded_source_unavailable_reason", operation.ID), operation.UploadedSourceUnavailableReason},
		)
	}
	return copies
}

// isUserFacingTerminologyField decides whether a localized field path holds
// copy the person reads. The few prompt roots are excluded by name; all other
// localized application paths are user-facing.
func isUserFacingTerminologyField(path string) bool {
	switch {
	case path == "chat.harness.context_template",
		path == "chat.harness.conversation_input_description",
		strings.HasPrefix(path, "chat.harness.context_template."),
		strings.HasPrefix(path, "chat.harness.conversation_input_description."):
		return false
	case strings.HasPrefix(path, "chat.tools"),
		strings.HasPrefix(path, "chat.agent"),
		strings.HasPrefix(path, "agent_configuration"),
		strings.HasPrefix(path, "provider_deprecations"):
		return false
	}
	return strings.HasPrefix(path, "content.") ||
		strings.HasPrefix(path, "capabilities[") ||
		strings.HasPrefix(path, "chat.presentation.") ||
		strings.HasPrefix(path, "chat.pending_approvals.messages.") ||
		strings.HasPrefix(path, "chat.harness.") ||
		strings.HasPrefix(path, "model_delivery.reasons.") ||
		strings.HasPrefix(path, "runtime.task_cancellation.") ||
		strings.HasPrefix(path, "runtime.generation_messages.")
}

// jsonTerminologyCopy walks a struct that contains only user-facing copy.
func jsonTerminologyCopy(prefix string, value any) []terminologyCopy {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var node any
	if json.Unmarshal(raw, &node) != nil {
		return nil
	}
	return collectTerminologyStrings(prefix, node)
}

func collectTerminologyStrings(path string, node any) []terminologyCopy {
	switch value := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		copies := make([]terminologyCopy, 0, len(keys))
		for _, key := range keys {
			copies = append(copies, collectTerminologyStrings(path+"."+key, value[key])...)
		}
		return copies
	case []any:
		copies := make([]terminologyCopy, 0, len(value))
		for index, item := range value {
			copies = append(copies, collectTerminologyStrings(fmt.Sprintf("%s[%d]", path, index), item)...)
		}
		return copies
	case string:
		return []terminologyCopy{{path: path, text: value}}
	default:
		return nil
	}
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// validateOfferingTerminology checks the Offering copy the person reads.
// provider_options.messages reaches the person as failure text, so it is part
// of the same scope as labels and descriptions.
func validateOfferingTerminology(
	terminology *TerminologySchema,
	offering schemacontract.Offering,
) error {
	if terminology == nil {
		return nil
	}
	for _, copy := range offeringUserFacingCopy(offering) {
		if err := terminology.validateCopy(terminologyCopy{
			path: fmt.Sprintf("Offering %q 的用户文案 %s", offering.ID, copy.path),
			text: copy.text,
		}); err != nil {
			return err
		}
	}
	return nil
}

package catalog_test

import (
	"regexp"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #632 second pass: the approval texts (consent_*), the prompt report and the
// get_task failure advice reach the Agent as tool results in the turns around
// a three-view. In the default locale they are Chinese with no English
// sentence the Agent could copy into its reply (field names such as
// status_text or result_recovery.available are identifiers, not words); the
// en bundle keeps the English copy and the coverage entries keep it complete.
func TestAgentToolResultCopyIsInTheDefaultLocale(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	agent := application.Chat.Agent
	report := agent.PromptReport
	texts := map[string]string{
		"chat.agent.consent_denied_content":                        agent.ConsentDeniedContent,
		"chat.agent.consent_waiting_content":                       agent.ConsentWaitingContent,
		"chat.agent.consent_duplicate_content":                     agent.ConsentDuplicateContent,
		"chat.agent.consent_recharge_content":                      agent.ConsentRechargeContent,
		"chat.agent.consent_limit_content":                         agent.ConsentLimitContent,
		"chat.agent.consent_reconcile_content":                     agent.ConsentReconcileContent,
		"chat.agent.prompt_report.unchanged_template":              report.UnchangedTemplate,
		"chat.agent.prompt_report.changed_template":                report.ChangedTemplate,
		"chat.agent.prompt_report.changed_without_prompt_template": report.ChangedWithoutPromptTemplate,
		"chat.agent.prompt_report.truncation_template":             report.TruncationTemplate,
		"chat.agent.prompt_report.too_long_template":               report.TooLongTemplate,
		"chat.agent.prompt_report.unit_text":                       report.UnitText,
	}
	for id, label := range report.Layers {
		texts["chat.agent.prompt_report.layers."+id] = label
	}
	for category, advice := range application.Chat.Processing.Failures.Advice {
		texts["chat.processing.failures.advice."+category] = advice
	}
	chinese := regexp.MustCompile(`\p{Han}`)
	machineToken := regexp.MustCompile(`[A-Za-z0-9_@#./{}=<>%-]*[_@#{}0-9=<>%.][A-Za-z0-9_@#./{}=<>%-]*`)
	english := regexp.MustCompile(`[A-Za-z][A-Za-z'’]*(?:[ ,;:()/-]+[A-Za-z][A-Za-z'’]*){5,}`)
	bundle := application.Localization.Bundles["en"].Fields
	for path, text := range texts {
		if !chinese.MatchString(text) {
			t.Errorf("%s is not default-locale copy: %q", path, text)
		}
		if match := english.FindString(machineToken.ReplaceAllString(text, "|")); match != "" {
			t.Errorf("%s carries the English sentence %q", path, match)
		}
		if translated := bundle[path]; translated == "" || chinese.MatchString(translated) {
			t.Errorf("the en bundle lacks an English %s: %q", path, translated)
		}
	}
	declared := map[string]bool{}
	for _, path := range application.Localization.Coverage {
		declared[path] = true
	}
	for _, path := range []string{"chat.agent.consent_denied_content", "chat.agent.consent_waiting_content",
		"chat.agent.consent_duplicate_content", "chat.agent.consent_recharge_content", "chat.agent.consent_limit_content",
		"chat.agent.consent_reconcile_content", "chat.agent.prompt_report", "chat.processing.failures.advice"} {
		if !declared[path] {
			t.Errorf("localization coverage does not declare %s", path)
		}
	}
}

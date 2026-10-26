package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestInsufficientBalanceIsDeclaredEndToEndInTheSchema(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	class := string(schema.FailureInsufficientBalance)
	if !schema.FailureInsufficientBalance.Valid() {
		t.Fatal("insufficient_balance must be a valid failure class")
	}
	if strings.TrimSpace(app.Content.Messages["failure."+class]) == "" {
		t.Fatal("the public failure text is missing")
	}
	if strings.TrimSpace(app.Chat.Presentation.Messages.GenerationFailedByClass[class]) == "" {
		t.Fatal("the Agent-facing failure text is missing")
	}
	// The system prompt tells the Agent what to do when a provider fails.
	prompt := app.Chat.Agent.SystemPrompt
	for _, needle := range []string{"换一个当前可用的模型", "list_offerings", "余额不足"} {
		if !strings.Contains(prompt, needle) {
			t.Errorf("system prompt lacks %q", needle)
		}
	}
	// Every connection that declares the class does so without retrying it.
	declared := 0
	for _, connection := range app.Runtime.ProviderConnections {
		mapping := connection.ErrorMapping
		if mapping == nil {
			continue
		}
		for _, rule := range mapping.UpstreamCodeMap {
			if rule.Class == class {
				declared++
				if rule.Retryable {
					t.Errorf("%s: balance failures must not be retryable", connection.ID)
				}
			}
		}
		for _, rule := range mapping.HTTPStatusRanges {
			if rule.Class == class {
				declared++
				if rule.Retryable {
					t.Errorf("%s: balance failures must not be retryable", connection.ID)
				}
			}
		}
		for _, matcher := range mapping.MessageMatchers {
			if matcher.Class == class {
				declared++
				if matcher.Retryable {
					t.Errorf("%s: balance failures must not be retryable", connection.ID)
				}
			}
		}
	}
	if declared == 0 {
		t.Fatal("no provider connection maps an upstream balance error")
	}
}

func TestBalanceDeclarationsAreValidated(t *testing.T) {
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"unknown failure class": func(a *catalog.ApplicationSchema) {
			a.Chat.Presentation.Messages.GenerationFailedByClass["no_such_class"] = "text"
		},
		"empty text": func(a *catalog.ApplicationSchema) {
			a.Chat.Presentation.Messages.GenerationFailedByClass["insufficient_balance"] = " "
		},
		"public text removed": func(a *catalog.ApplicationSchema) {
			delete(a.Content.Messages, "failure.insufficient_balance")
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			mutate(&app)
			if err := catalog.ValidateApplicationSchema(app); err == nil {
				t.Fatal("an invalid balance declaration was accepted")
			}
		})
	}
}

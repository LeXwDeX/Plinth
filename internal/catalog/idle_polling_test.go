package catalog

import (
	"encoding/json"
	"testing"
)

// #385: how the page's repeating reads slow down while nothing changes is declared by the Schema
// (runtime.frontend.idle_polling, with the interval of the pending-approvals read that used to be a
// constant in the page) and reaches the page through the public projection.
func TestIdlePollingIsDeclaredValidatedAndProjected(t *testing.T) {
	t.Parallel()
	product, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	frontend := application.Runtime.Frontend
	if frontend.PendingApprovalsRefreshMilliseconds <= 0 || frontend.IdlePolling.BackoffFactor <= 1 ||
		frontend.IdlePolling.MaxIntervalMilliseconds < frontend.PendingApprovalsRefreshMilliseconds {
		t.Fatalf("default idle polling = %+v / %d", frontend.IdlePolling, frontend.PendingApprovalsRefreshMilliseconds)
	}

	for name, mutate := range map[string]func(*FrontendRuntimeSchema){
		"zero":             func(f *FrontendRuntimeSchema) { f.IdlePolling = IdlePollingSchema{} },
		"factor one":       func(f *FrontendRuntimeSchema) { f.IdlePolling.BackoffFactor = 1 },
		"factor unbounded": func(f *FrontendRuntimeSchema) { f.IdlePolling.BackoffFactor = 11 },
		"ceiling below a read": func(f *FrontendRuntimeSchema) {
			f.IdlePolling.MaxIntervalMilliseconds = f.BalanceRefreshMilliseconds - 1
		},
		"ceiling over an hour": func(f *FrontendRuntimeSchema) {
			f.IdlePolling.MaxIntervalMilliseconds = maxIdlePollingIntervalMilliseconds + 1
		},
		"pending read missing": func(f *FrontendRuntimeSchema) { f.PendingApprovalsRefreshMilliseconds = 0 },
		"harness read missing": func(f *FrontendRuntimeSchema) { f.HarnessRefreshMilliseconds = 0 },
		"ceiling below pending": func(f *FrontendRuntimeSchema) {
			f.PendingApprovalsRefreshMilliseconds = f.IdlePolling.MaxIntervalMilliseconds + 1
		},
	} {
		broken := application
		mutate(&broken.Runtime.Frontend)
		if err := ValidateApplicationSchema(broken); err == nil {
			t.Fatalf("%s: invalid idle polling accepted", name)
		}
	}

	public, err := json.Marshal(application.Public())
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Runtime struct {
			Frontend struct {
				PendingApprovals int               `json:"pending_approvals_refresh_milliseconds"`
				IdlePolling      IdlePollingSchema `json:"idle_polling"`
			} `json:"frontend"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(public, &document); err != nil {
		t.Fatal(err)
	}
	if document.Runtime.Frontend.IdlePolling != frontend.IdlePolling ||
		document.Runtime.Frontend.PendingApprovals != frontend.PendingApprovalsRefreshMilliseconds {
		t.Fatalf("public idle polling = %+v / %d", document.Runtime.Frontend.IdlePolling, document.Runtime.Frontend.PendingApprovals)
	}
}

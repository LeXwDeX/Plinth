package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type failingBalanceReader struct{}

func (failingBalanceReader) Balance(context.Context) (float64, error) {
	return 0, errors.New("configured upstream failure")
}
func (failingBalanceReader) NativeBalance(context.Context) (schema.NativeAmount, error) {
	return schema.NativeAmount{}, errors.New("configured upstream failure")
}

func TestLegacyProviderBalanceContract(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarness(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/meshy/balance",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var payload struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(
		response.Body.Bytes(),
		&payload,
	); err != nil {
		t.Fatal(err)
	}
	if len(payload.Accounts) != 3 {
		t.Fatalf("accounts = %#v", payload.Accounts)
	}
	providers := map[string]bool{}
	for _, account := range payload.Accounts {
		provider, _ := account["provider"].(string)
		providers[provider] = true
		if provider == "meshy" || provider == "tripo" || provider == "hyper3d" {
			// The native mock amount is explicitly configured as a decimal string.
			if account["price_mode"] != "native" {
				t.Fatalf("native account mode missing: %#v", account)
			}
			wantUnit, wantAmount := "meshy.credit", "1000"
			if provider == "tripo" {
				wantUnit, wantAmount = "tripo.credit", "5000"
			}
			if provider == "hyper3d" {
				wantUnit, wantAmount = "hyper3d.credit", "100"
			}
			if v, ok := account["native_balance"].(map[string]any); !ok || v["unit"] != wantUnit || v["amount"] != wantAmount {
				t.Fatalf("native balance missing: %#v", account)
			}
			if _, ok := account["balance"]; ok {
				t.Fatalf("native account leaked an ambiguous float balance: %#v", account)
			}
			continue
		}
		if _, hasBalance := account["balance"]; !hasBalance {
			t.Fatalf("account %s 缺少 balance: %#v", provider, account)
		}
	}
	if !providers["meshy"] || !providers["tripo"] || !providers["hyper3d"] {
		t.Fatalf("accounts = %#v", payload.Accounts)
	}
}

func TestLegacyProviderBalanceFailureDoesNotInventNativeAmount(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.ProviderBalance =
				"/configured-failing-balance"
		},
		nil,
		nil,
		map[string]provideraccounts.BalanceReader{
			"meshy": failingBalanceReader{},
		},
	)

	request := httptest.NewRequest(
		http.MethodGet,
		"/configured-failing-balance",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	accounts, _ := payload["accounts"].([]any)
	if len(accounts) == 0 {
		t.Fatalf("payload = %#v", payload)
	}
	failed := false
	for _, raw := range accounts {
		account, _ := raw.(map[string]any)
		if account["provider"] == "meshy" && account["price_mode"] == "native" && account["native_balance"] == nil {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("failed native account did not report an unavailable exact amount: %#v", payload)
	}
}

func TestLegacyProviderBalanceWireComesFromProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			account := application.Runtime.ProviderAccounts["meshy"]
			delete(application.Runtime.ProviderAccounts, "meshy")
			account.Balance.MockValue = 88.5
			application.Runtime.ProviderAccounts["configured-account"] =
				account
			application.Runtime.HTTP.LegacyRoutes.ProviderBalance =
				"/configured-balance"
			contract := &application.Runtime.HTTP.LegacyProviderBalance
			contract.Accounts = []catalog.ProviderBalanceAccountSchema{{
				AccountID: "configured-account",
			}}
			contract.BalanceField = "configuredRemaining"
			contract.ErrorField = "configuredError"
			application.Administration.Usage.Balance.AccountID =
				"configured-account"
		},
	)

	request := httptest.NewRequest(
		http.MethodGet,
		"/configured-balance",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	accounts, _ := payload["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("payload = %#v", payload)
	}
	account, _ := accounts[0].(map[string]any)
	if account["configuredRemaining"] != 88.5 {
		t.Fatalf("account = %#v", account)
	}
	if _, exists := payload["balance"]; exists {
		t.Fatalf("hard-coded balance field leaked: %#v", payload)
	}
}

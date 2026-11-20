package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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

// hangingBalanceReader is a provider that never answers: it waits for the
// deadline of the read, as an upstream that accepts the connection and then
// stays silent does.
type hangingBalanceReader struct {
	calls   atomic.Int32
	started atomic.Int64
}

func (reader *hangingBalanceReader) hang(ctx context.Context) error {
	reader.calls.Add(1)
	reader.started.CompareAndSwap(0, time.Now().UnixNano())
	<-ctx.Done()
	return ctx.Err()
}

func (reader *hangingBalanceReader) Balance(ctx context.Context) (float64, error) {
	return 0, reader.hang(ctx)
}

func (reader *hangingBalanceReader) NativeBalance(ctx context.Context) (schema.NativeAmount, error) {
	return schema.NativeAmount{}, reader.hang(ctx)
}

// #385: the balance route is polled by every open page. Two silent providers
// used to hold the page for the sum of their own timeouts (30 s each, 90 s in
// the field logs); now the accounts are read together, each within the Schema's
// read timeout, and the answer is kept for the TTL, so the next poll is free.
func TestLegacyProviderBalanceNeverWaitsOnSilentProviders(t *testing.T) {
	t.Parallel()
	meshy, tripo := &hangingBalanceReader{}, &hangingBalanceReader{}
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		func(application *catalog.ApplicationSchema) {
			// The Schema bounds one read (1 s here) and keeps its answer (60 s).
			policy := *application.StepPolicy
			policy.BalanceReadTimeoutSeconds = 1
			policy.BalanceCacheSeconds = 60
			application.StepPolicy = &policy
		},
		nil,
		nil,
		map[string]provideraccounts.BalanceReader{
			"meshy": meshy,
			"tripo": tripo,
			"hyper3d": provideraccounts.FixedNativeBalanceReader{
				FixedBalanceReader: 100,
				Amount:             schema.NativeAmount{Unit: "hyper3d.credit", UnitLabel: "Hyper3D credits", Amount: "100"},
			},
		},
	)
	poll := func() ([]map[string]any, time.Duration) {
		started := time.Now()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/meshy/balance", nil))
		elapsed := time.Since(started)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		var payload struct {
			Accounts []map[string]any `json:"accounts"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Accounts, elapsed
	}

	accounts, first := poll()
	// The silent accounts are given the Schema's read timeout and no more.
	if first < 900*time.Millisecond || first > 10*time.Second {
		t.Fatalf("first poll took %v, want about one read timeout (1 s)", first)
	}
	// Together, not one after the other: both were asked at the same moment, where a read of one account after the
	// other would have started the second a whole timeout (1 s) later.
	apart := time.Duration(meshy.started.Load() - tripo.started.Load())
	if apart < 0 {
		apart = -apart
	}
	if meshy.started.Load() == 0 || tripo.started.Load() == 0 || apart > 500*time.Millisecond {
		t.Fatalf("accounts were read one after the other: started %v apart", apart)
	}
	byProvider := map[string]map[string]any{}
	for _, account := range accounts {
		byProvider[account["provider"].(string)] = account
	}
	for _, silent := range []string{"meshy", "tripo"} {
		// The amount is unknown, never invented.
		if account := byProvider[silent]; account["price_mode"] != "native" || account["native_balance"] != nil {
			t.Fatalf("%s = %#v", silent, account)
		}
	}
	if amount, _ := byProvider["hyper3d"]["native_balance"].(map[string]any); amount["amount"] != "100" {
		t.Fatalf("a healthy account was lost: %#v", byProvider["hyper3d"])
	}

	// The next polls are answered from the short cache, failed reads included.
	for range 3 {
		// A read of the silent providers would take the whole timeout (1 s) again.
		if _, elapsed := poll(); elapsed > 800*time.Millisecond {
			t.Fatalf("cached poll took %v", elapsed)
		}
	}
	if meshy.calls.Load() != 1 || tripo.calls.Load() != 1 {
		t.Fatalf("providers were asked again within the TTL: meshy=%d tripo=%d", meshy.calls.Load(), tripo.calls.Load())
	}
}

// A page that goes away does not leave the shared read behind, and a healthy
// provider is read once per TTL however often the page polls.
func TestLegacyProviderBalanceReadsAHealthyProviderOncePerTTL(t *testing.T) {
	t.Parallel()
	counting := &countingBalanceReader{}
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		nil,
		nil,
		nil,
		nil,
		map[string]provideraccounts.BalanceReader{"meshy": counting},
	)
	for range 5 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/meshy/balance", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	}
	if calls := counting.calls.Load(); calls != 1 {
		t.Fatalf("the provider was read %d times for 5 polls inside the TTL, want 1", calls)
	}
}

type countingBalanceReader struct{ calls atomic.Int32 }

func (reader *countingBalanceReader) Balance(context.Context) (float64, error) {
	reader.calls.Add(1)
	return 1000, nil
}

func (reader *countingBalanceReader) NativeBalance(context.Context) (schema.NativeAmount, error) {
	reader.calls.Add(1)
	return schema.NativeAmount{Unit: "meshy.credit", UnitLabel: "Meshy credits", Amount: "1000"}, nil
}

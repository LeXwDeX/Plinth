package httpjson

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func connectionMapping(t *testing.T, id string) *generation.ErrorMapping {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range product.Application().Runtime.ProviderConnections {
		if connection.ID == id {
			if connection.ErrorMapping == nil {
				t.Fatalf("%s has no error mapping", id)
			}
			return connection.ErrorMapping
		}
	}
	t.Fatalf("connection %s missing", id)
	return nil
}

func classify(t *testing.T, mapping *generation.ErrorMapping, status int, body string) *generation.Failure {
	t.Helper()
	response := &http.Response{StatusCode: status, Header: make(http.Header)}
	err := ClassifyWithMapping(mapping)(response, []byte(body))
	var providerErr *generation.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("status %d body %s: no classification (%v)", status, body, err)
	}
	return providerErr.Failure
}

// An exhausted provider account is its own failure class whichever status the
// provider chooses, including 403 which these connections map to invalid input.
func TestInsufficientBalanceHasItsOwnClassAcrossProviders(t *testing.T) {
	cases := []struct {
		name       string
		connection string
		status     int
		body       string
	}{
		{"openai quota", "openai-images", http.StatusTooManyRequests, `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`},
		{"openai message", "openai-images", http.StatusForbidden, `{"error":{"message":"Insufficient balance, please recharge"}}`},
		{"ark arrears", "jimeng-images", http.StatusForbidden, `{"error":{"code":"AccountOverdueError","message":"The account is in arrears"}}`},
		{"gateway arrears message", "jimeng-images", http.StatusForbidden, `{"error":{"message":"Your account in arrears, please top up"}}`},
		{"meshy payment required", "meshy", http.StatusPaymentRequired, `{"message":"Insufficient credits"}`},
		{"tripo credit", "tripo", http.StatusForbidden, `{"code":2010,"message":"You need more credits to continue"}`},
		{"rodin funds", "hyper3d", http.StatusBadRequest, `{"error":"API_INSUFFICIENT_FUNDS"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapping := connectionMapping(t, tc.connection)
			if tc.connection == "hyper3d" {
				// Rodin carries its code in a JSON field the adapter reads itself.
				rule, ok := mapping.UpstreamCodeMap["API_INSUFFICIENT_FUNDS"]
				if !ok || rule.Class != string(generation.FailureInsufficientBalance) || rule.Retryable {
					t.Fatalf("hyper3d code map = %+v", rule)
				}
				return
			}
			failure := classify(t, mapping, tc.status, tc.body)
			if failure.Class != generation.FailureInsufficientBalance || failure.Retryable || failure.Ambiguous {
				t.Fatalf("failure = %+v", failure)
			}
			if strings.Contains(failure.SafeDetail, "quota") || strings.Contains(failure.SafeDetail, "balance") {
				t.Fatalf("upstream text reached the public detail: %q", failure.SafeDetail)
			}
		})
	}
}

// Ordinary refusals on the same statuses keep their classes.
func TestBalanceEvidenceDoesNotRecastOtherErrors(t *testing.T) {
	openai := connectionMapping(t, "openai-images")
	if failure := classify(t, openai, http.StatusForbidden, `{"error":{"message":"Your request was blocked by the safety system"}}`); failure.Class != generation.FailureInvalidInput {
		t.Fatalf("403 = %+v", failure)
	}
	if failure := classify(t, openai, http.StatusUnauthorized, `{"error":{"message":"Incorrect API key"}}`); failure.Class != generation.FailureAuthentication {
		t.Fatalf("401 = %+v", failure)
	}
	// A status the connection does not declare falls through to the default
	// HTTP classification instead of becoming a balance error.
	meshy := connectionMapping(t, "meshy")
	response := &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header)}
	if err := ClassifyWithMapping(meshy)(response, []byte(`{"message":"Unauthorized"}`)); err != nil {
		t.Fatalf("undeclared status was classified by the mapping: %v", err)
	}
}

// A Meshy 402 that names the API key's credit limit is a key quota refusal,
// not an empty account (#93); every other 402 stays an insufficient balance.
func TestMeshyKeyCreditLimitIsNotInsufficientBalance(t *testing.T) {
	mapping := connectionMapping(t, "meshy")
	quota := classify(t, mapping, http.StatusPaymentRequired, `{"message":"API key credit limit reached"}`)
	if quota.Class != generation.FailureCredentialQuota || quota.Retryable {
		t.Fatalf("key credit limit = %+v, want a non-retryable credential_quota", quota)
	}
	for name, body := range map[string]string{
		"funds": `{"message":"Insufficient funds"}`,
		"empty": `{}`,
	} {
		if got := classify(t, mapping, http.StatusPaymentRequired, body); got.Class != generation.FailureInsufficientBalance {
			t.Fatalf("%s: class = %s, want insufficient_balance", name, got.Class)
		}
	}
}

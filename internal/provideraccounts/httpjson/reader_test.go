package httpjson

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReaderUsesConfiguredWireContract(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet ||
				request.URL.Path != "/configured/root/account/balance" {
				t.Fatalf(
					"request = %s %s",
					request.Method,
					request.URL.String(),
				)
			}
			if request.Header.Get("Configured-Authorization") !=
				"Configured configured-secret" {
				t.Fatalf("authorization = %#v", request.Header)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"configuredRemaining":73.25}`))
		},
	))
	defer upstream.Close()

	reader, err := New(Config{
		BaseURL:              upstream.URL + "/configured/root/",
		APIKey:               "configured-secret",
		EndpointPath:         "/account/balance",
		AuthorizationHeader:  "Configured-Authorization",
		AuthorizationType:    "Configured",
		ResponseBalanceField: "configuredRemaining",
		RequestTimeout:       time.Second,
		ResponseMaxBytes:     65536,
		ErrorBodyMaxBytes:    65536,
		Client:               upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	balance, err := reader.Balance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if balance != 73.25 {
		t.Fatalf("balance = %v", balance)
	}
}

func TestReaderRejectsNonSuccessAndInvalidResponse(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"status": func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "upstream detail", http.StatusBadGateway)
		},
		"invalid balance": func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"balance":"not-a-number"}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(handler)
			defer upstream.Close()
			reader, err := New(Config{
				BaseURL:              upstream.URL,
				EndpointPath:         "/balance",
				AuthorizationHeader:  "Authorization",
				AuthorizationType:    "Bearer",
				ResponseBalanceField: "balance",
				RequestTimeout:       time.Second,
				ResponseMaxBytes:     65536,
				ErrorBodyMaxBytes:    65536,
				Client:               upstream.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Balance(context.Background()); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNativeBalanceKeepsOriginalDecimalAndConfiguredUnit(t *testing.T) {
	for _, raw := range []string{`9007199254.740991`, `"9007199254.740991"`, `1000`, `"1000.000000"`} {
		t.Run(raw, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"remaining":` + raw + `}}`))
			}))
			defer upstream.Close()
			reader, err := New(Config{BaseURL: upstream.URL, EndpointPath: "/balance", AuthorizationHeader: "Authorization", ResponseBalanceField: "data.remaining", RequestTimeout: time.Second, ResponseMaxBytes: 1024, ErrorBodyMaxBytes: 1024, NativeUnit: "configured.credit", NativeUnitLabel: "Configured credits"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := reader.NativeBalance(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := "9007199254.740991"
			if raw == `1000` || raw == `"1000.000000"` {
				want = "1000"
			}
			if got.Amount != want || got.Unit != "configured.credit" || got.UnitLabel != "Configured credits" {
				t.Fatalf("exact balance=%+v", got)
			}
			_, legacyErr := reader.Balance(context.Background())
			if raw[0] == '"' && legacyErr == nil {
				t.Fatal("legacy numeric-only contract changed")
			}
		})
	}
}
func TestNativeBalanceRejectsUnknownOrUnrepresentableAmounts(t *testing.T) {
	for _, body := range []string{`{}`, `{"balance":null}`, `{"balance":true}`, `{"balance":[]}`, `{"balance":-1}`, `{"balance":0.0000001}`, `{"balance":"bad"}`, `{"balance":9223372036855}`, `{"balance":1e3}`} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer upstream.Close()
			reader, err := New(Config{BaseURL: upstream.URL, EndpointPath: "/balance", AuthorizationHeader: "Authorization", ResponseBalanceField: "balance", RequestTimeout: time.Second, ResponseMaxBytes: 1024, ErrorBodyMaxBytes: 1024, NativeUnit: "configured.credit", NativeUnitLabel: "Configured credits"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.NativeBalance(context.Background()); err == nil {
				t.Fatalf("unknown amount accepted: %s", body)
			}
		})
	}
}

// The Hyper3D (Rodin) check_balance wire contract as declared in the Schema's
// provider_accounts entry: GET /check_balance, Bearer auth, {"balance": n}.
func TestReaderParsesHyper3DCheckBalance(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet ||
				request.URL.Path != "/api/v2/check_balance" {
				t.Fatalf("request = %s %s", request.Method, request.URL.String())
			}
			if request.Header.Get("Authorization") != "Bearer rodin-test-key" {
				t.Fatalf("authorization = %#v", request.Header)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"balance": 10}`))
		},
	))
	defer upstream.Close()

	reader, err := New(Config{
		BaseURL:              upstream.URL + "/api/v2",
		APIKey:               "rodin-test-key",
		EndpointPath:         "/check_balance",
		AuthorizationHeader:  "Authorization",
		AuthorizationType:    "Bearer",
		ResponseBalanceField: "balance",
		NativeUnit:           "hyper3d.credit",
		NativeUnitLabel:      "Hyper3D credits",
		RequestTimeout:       time.Second,
		ResponseMaxBytes:     65536,
		ErrorBodyMaxBytes:    65536,
		Client:               upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	balance, err := reader.Balance(context.Background())
	if err != nil || balance != 10 {
		t.Fatalf("balance = %v, err = %v", balance, err)
	}
	native, err := reader.NativeBalance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if native.Amount != "10" || native.Unit != "hyper3d.credit" || native.UnitLabel != "Hyper3D credits" {
		t.Fatalf("native = %+v", native)
	}
}

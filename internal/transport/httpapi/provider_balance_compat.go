package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var errBalanceUnknown = errors.New("provider balance unknown")

// ProviderBalanceReader is the shared, short-lived balance reader
// (provideraccounts.Cache): one reading per request, in the order asked, read
// concurrently, each bounded by the Schema's read timeout and kept for its TTL,
// a failed one included. It never fails; an account it could not read answers
// Known == false (#385).
type ProviderBalanceReader interface {
	ReadRequests(ctx context.Context, requests []provideraccounts.Request) []provideraccounts.Reading
}

// balanceAccountPlan is what the route shows for one account: its provider's
// own unit (native) with the units its Offerings are priced in, or a plain
// number.
type balanceAccountPlan struct {
	native bool
	units  map[string]bool
}

func (s *Server) legacyProviderBalance(
	writer http.ResponseWriter,
	request *http.Request,
	_ generation.OwnerID,
) {
	contract := s.application.Runtime.HTTP.LegacyProviderBalance
	plans := make([]balanceAccountPlan, len(contract.Accounts))
	for index, account := range contract.Accounts {
		plan := balanceAccountPlan{units: map[string]bool{}}
		for _, o := range s.generation.Offerings("") {
			if o.ProviderID == account.AccountID && o.NativePricing != nil {
				plan.native = true
				plan.units[o.NativePricing.Unit] = true
			}
		}
		plans[index] = plan
	}
	// The page asks again every few seconds from every open tab. Through the
	// shared cache a slow or failing provider costs one bounded read per TTL,
	// concurrently for all accounts, instead of one unbounded read per account
	// per request, which held the page for the sum of every account's timeout.
	var readings []provideraccounts.Reading
	if s.providerBalances != nil {
		requests := make([]provideraccounts.Request, len(contract.Accounts))
		for index, account := range contract.Accounts {
			requests[index] = provideraccounts.Request{AccountID: account.AccountID, Plain: !plans[index].native}
		}
		readings = s.providerBalances.ReadRequests(request.Context(), requests)
	}
	accounts := make([]map[string]any, 0, len(contract.Accounts))
	for index, account := range contract.Accounts {
		entry := map[string]any{
			contract.ProviderField: account.AccountID,
		}
		plan := plans[index]
		if plan.native {
			entry["price_mode"] = "native"
			var exact *schema.NativeAmount
			if readings != nil {
				if reading := readings[index]; reading.Known {
					exact = reading.Native
				}
			} else if direct, err := s.providerAccounts.NativeBalance(request.Context(), account.AccountID); err == nil {
				exact = direct
			}
			if exact != nil && plan.units[exact.Unit] {
				entry["native_balance"] = exact
			} else {
				entry["native_balance"] = nil
			}
			accounts = append(accounts, entry)
			continue
		}
		var balance float64
		var err error
		if readings != nil {
			// The shared cache has already logged a failed read.
			if reading := readings[index]; reading.Known {
				balance = reading.Value
			} else {
				err = errBalanceUnknown
			}
		} else if balance, err = s.providerAccounts.Balance(request.Context(), account.AccountID); err != nil {
			s.logger.Warn(
				"Provider 余额读取失败",
				"account_id", account.AccountID,
				"error", err,
			)
		}
		if err != nil {
			entry[contract.ErrorField] =
				s.messageIn(requestLocale(request), "error.provider_balance_failed")
			accounts = append(accounts, entry)
			continue
		}
		entry[contract.BalanceField] = balance
		accounts = append(accounts, entry)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		contract.AccountsField: accounts,
	})
}

func (s *Server) withProviderBalanceIdentity(
	next identityHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			contract := s.application.Runtime.HTTP.LegacyProviderBalance
			writeJSON(writer, http.StatusUnauthorized, map[string]any{
				contract.ErrorField: s.application.Authentication.Messages.
					Unauthenticated,
			})
			return
		}
		next(
			writer,
			request,
			generation.OwnerID(principal.ID()),
		)
	}
}

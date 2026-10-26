package httpapi

import (
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) legacyProviderBalance(
	writer http.ResponseWriter,
	request *http.Request,
	_ generation.OwnerID,
) {
	contract := s.application.Runtime.HTTP.LegacyProviderBalance
	accounts := make([]map[string]any, 0, len(contract.Accounts))
	for _, account := range contract.Accounts {
		entry := map[string]any{
			contract.ProviderField: account.AccountID,
		}
		native := false
		units := map[string]bool{}
		for _, o := range s.generation.Offerings("") {
			if o.ProviderID == account.AccountID && o.NativePricing != nil {
				native = true
				units[o.NativePricing.Unit] = true
			}
		}
		if native {
			entry["price_mode"] = "native"
			exact, err := s.providerAccounts.NativeBalance(request.Context(), account.AccountID)
			if err == nil && exact != nil && units[exact.Unit] {
				entry["native_balance"] = exact
			} else {
				entry["native_balance"] = nil
			}
			accounts = append(accounts, entry)
			continue
		}
		balance, err := s.providerAccounts.Balance(
			request.Context(),
			account.AccountID,
		)
		if err != nil {
			s.logger.Warn(
				"Provider 余额读取失败",
				"account_id", account.AccountID,
				"error", err,
			)
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

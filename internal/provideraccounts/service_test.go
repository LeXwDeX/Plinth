package provideraccounts

import (
	"context"
	"errors"
	"testing"
)

type balanceReaderFunc func(context.Context) (float64, error)

func (function balanceReaderFunc) Balance(
	ctx context.Context,
) (float64, error) {
	return function(ctx)
}

func TestServiceReadsConfiguredProviderAccount(t *testing.T) {
	service, err := New(Config{
		Readers: map[string]BalanceReader{
			"configured-account": balanceReaderFunc(
				func(context.Context) (float64, error) {
					return 42.5, nil
				},
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	balance, err := service.Balance(
		context.Background(),
		"configured-account",
	)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 42.5 {
		t.Fatalf("balance = %v", balance)
	}
}

func TestServiceRejectsUnknownAccountAndWrapsReaderFailure(t *testing.T) {
	upstreamFailure := errors.New("configured upstream failure")
	service, err := New(Config{
		Readers: map[string]BalanceReader{
			"failing": balanceReaderFunc(
				func(context.Context) (float64, error) {
					return 0, upstreamFailure
				},
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Balance(
		context.Background(),
		"missing",
	); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("unknown account error = %v", err)
	}
	if _, err := service.Balance(
		context.Background(),
		"failing",
	); !errors.Is(err, upstreamFailure) {
		t.Fatalf("reader error = %v", err)
	}
}

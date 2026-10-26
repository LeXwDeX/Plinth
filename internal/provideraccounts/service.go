package provideraccounts

import (
	"context"
	"errors"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"strings"
)

var ErrAccountNotFound = errors.New("provider account not found")

type BalanceReader interface {
	Balance(context.Context) (float64, error)
}

type Config struct {
	Readers map[string]BalanceReader
}

type Service struct {
	readers map[string]BalanceReader
}

func New(config Config) (*Service, error) {
	if len(config.Readers) == 0 {
		return nil, errors.New("provider accounts: readers are empty")
	}
	readers := make(map[string]BalanceReader, len(config.Readers))
	for accountID, reader := range config.Readers {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" || reader == nil {
			return nil, errors.New(
				"provider accounts: invalid reader configuration",
			)
		}
		if _, exists := readers[accountID]; exists {
			return nil, fmt.Errorf(
				"provider accounts: duplicate account %q",
				accountID,
			)
		}
		readers[accountID] = reader
	}
	return &Service{readers: readers}, nil
}

func (service *Service) Balance(
	ctx context.Context,
	accountID string,
) (float64, error) {
	reader, exists := service.readers[strings.TrimSpace(accountID)]
	if !exists {
		return 0, fmt.Errorf(
			"provider accounts: %w: %s",
			ErrAccountNotFound,
			accountID,
		)
	}
	balance, err := reader.Balance(ctx)
	if err != nil {
		return 0, fmt.Errorf(
			"provider accounts: read %q balance: %w",
			accountID,
			err,
		)
	}
	return balance, nil
}

type FixedBalanceReader float64

func (reader FixedBalanceReader) Balance(context.Context) (float64, error) {
	return float64(reader), nil
}

// NativeBalanceReader must parse an upstream decimal without an intermediate float.
type NativeBalanceReader interface {
	NativeBalance(context.Context) (schema.NativeAmount, error)
}

func (s *Service) NativeBalance(ctx context.Context, id string) (*schema.NativeAmount, error) {
	r, ok := s.readers[strings.TrimSpace(id)]
	if !ok {
		return nil, ErrAccountNotFound
	}
	native, ok := r.(NativeBalanceReader)
	if !ok {
		return nil, nil
	}
	amount, e := native.NativeBalance(ctx)
	if e != nil {
		return nil, e
	}
	n, e := schema.ParseNativeAmount(amount.Amount)
	if e != nil || amount.Unit == "" || amount.UnitLabel == "" {
		return nil, errors.New("invalid exact native balance")
	}
	amount.Amount = schema.FormatNativeAmount(n)
	if e := amount.Validate(); e != nil {
		return nil, e
	}
	return &amount, nil
}

// FixedNativeBalanceReader keeps mock native amounts explicit and independent
// of the compatibility float fixture.
type FixedNativeBalanceReader struct {
	FixedBalanceReader
	Amount schema.NativeAmount
}

func (r FixedNativeBalanceReader) NativeBalance(context.Context) (schema.NativeAmount, error) {
	return r.Amount, r.Amount.Validate()
}

package administration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrBalanceSummaryFailed = errors.New(
	"administration balance summary failed",
)

type BalanceSnapshot struct {
	ID         string
	AccountID  string
	Balance    float64
	CapturedAt time.Time
}

type BalanceSummary struct {
	CurrentBalance float64
	History        []BalanceSnapshot
}

type AccountBalanceReader interface {
	Balance(context.Context, string) (float64, error)
}

type BalanceSnapshotRepository interface {
	RecordBalanceSnapshot(context.Context, BalanceSnapshot) error
	QueryBalanceSnapshots(context.Context, time.Time) ([]BalanceSnapshot, error)
}

type BalanceSummaryServiceConfig struct {
	Accounts            AccountBalanceReader
	Snapshots           BalanceSnapshotRepository
	AccountID           string
	HistoryDays         int
	SnapshotIDPrefix    string
	SnapshotWritePolicy string
	Now                 func() time.Time
	NewID               func(string) (string, error)
}

type BalanceSummaryService struct {
	accounts            AccountBalanceReader
	snapshots           BalanceSnapshotRepository
	accountID           string
	historyDays         int
	snapshotIDPrefix    string
	snapshotWritePolicy string
	now                 func() time.Time
	newID               func(string) (string, error)
}

func NewBalanceSummaryService(
	config BalanceSummaryServiceConfig,
) (*BalanceSummaryService, error) {
	config.AccountID = strings.TrimSpace(config.AccountID)
	config.SnapshotIDPrefix = strings.TrimSpace(config.SnapshotIDPrefix)
	if config.Accounts == nil ||
		config.Snapshots == nil ||
		config.AccountID == "" ||
		config.HistoryDays <= 0 ||
		config.SnapshotIDPrefix == "" {
		return nil, errors.New(
			"administration: balance summary configuration is invalid",
		)
	}
	switch config.SnapshotWritePolicy {
	case "best-effort", "required":
	default:
		return nil, errors.New(
			"administration: balance snapshot write policy is invalid",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewID == nil {
		config.NewID = newBalanceSnapshotID
	}
	return &BalanceSummaryService{
		accounts:            config.Accounts,
		snapshots:           config.Snapshots,
		accountID:           config.AccountID,
		historyDays:         config.HistoryDays,
		snapshotIDPrefix:    config.SnapshotIDPrefix,
		snapshotWritePolicy: config.SnapshotWritePolicy,
		now:                 config.Now,
		newID:               config.NewID,
	}, nil
}

func (service *BalanceSummaryService) Summary(
	ctx context.Context,
) (BalanceSummary, error) {
	balance, err := service.accounts.Balance(ctx, service.accountID)
	if err != nil {
		return BalanceSummary{}, fmt.Errorf(
			"%w: read account balance: %v",
			ErrBalanceSummaryFailed,
			err,
		)
	}
	capturedAt := service.now().UTC()
	snapshotID, err := service.newID(service.snapshotIDPrefix)
	if err != nil {
		return BalanceSummary{}, fmt.Errorf(
			"%w: create snapshot id: %v",
			ErrBalanceSummaryFailed,
			err,
		)
	}
	err = service.snapshots.RecordBalanceSnapshot(
		ctx,
		BalanceSnapshot{
			ID:         snapshotID,
			AccountID:  service.accountID,
			Balance:    balance,
			CapturedAt: capturedAt,
		},
	)
	if err != nil && service.snapshotWritePolicy == "required" {
		return BalanceSummary{}, fmt.Errorf(
			"%w: record snapshot: %v",
			ErrBalanceSummaryFailed,
			err,
		)
	}
	history, err := service.snapshots.QueryBalanceSnapshots(
		ctx,
		capturedAt.AddDate(0, 0, -service.historyDays),
	)
	if err != nil {
		return BalanceSummary{}, fmt.Errorf(
			"%w: query history: %v",
			ErrBalanceSummaryFailed,
			err,
		)
	}
	return BalanceSummary{
		CurrentBalance: balance,
		History:        history,
	}, nil
}

func newBalanceSnapshotID(prefix string) (string, error) {
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return strings.TrimSpace(prefix) + "-" +
		hex.EncodeToString(token[:]), nil
}

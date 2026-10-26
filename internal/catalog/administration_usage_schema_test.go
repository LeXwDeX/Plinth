package catalog

import "testing"

func TestDefaultCatalogOwnsAdministrationUsageContract(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	usage := application.Administration.Usage
	if application.Runtime.HTTP.LegacyRoutes.AdminUsage !=
		"/api/admin/usage" {
		t.Fatalf(
			"administration usage route = %q",
			application.Runtime.HTTP.LegacyRoutes.AdminUsage,
		)
	}
	if application.Runtime.HTTP.LegacyRoutes.AdminBalanceSummary !=
		"/api/admin/balance-summary" {
		t.Fatalf(
			"administration balance summary route = %q",
			application.Runtime.HTTP.LegacyRoutes.AdminBalanceSummary,
		)
	}
	for name, value := range map[string]string{
		"data":              usage.API.Fields.Data,
		"error":             usage.API.Fields.Error,
		"user_id":           usage.API.Fields.UserID,
		"user_name":         usage.API.Fields.UserName,
		"user_email":        usage.API.Fields.UserEmail,
		"total_tasks":       usage.API.Fields.TotalTasks,
		"succeeded_tasks":   usage.API.Fields.SucceededTasks,
		"failed_tasks":      usage.API.Fields.FailedTasks,
		"estimated_credits": usage.API.Fields.EstimatedCredits,
		"last_active_at":    usage.API.Fields.LastActiveAt,
		"by_type":           usage.API.Fields.ByType,
		"total":             usage.API.Fields.Total,
		"days":              usage.API.Query.Days,
		"start_date":        usage.API.Query.StartDate,
		"end_date":          usage.API.Query.EndDate,
		"current_balance":   usage.API.BalanceFields.CurrentBalance,
		"history":           usage.API.BalanceFields.History,
		"history_balance":   usage.API.BalanceFields.Balance,
		"captured_at":       usage.API.BalanceFields.CapturedAt,
	} {
		if value == "" {
			t.Fatalf("administration usage %s is empty", name)
		}
	}
	if usage.Permission != "generations.manage" ||
		usage.Defaults.Days != 7 ||
		usage.Defaults.CustomRangeDays != 7 ||
		usage.Validation.DatePattern != `^\d{4}-\d{2}-\d{2}$` ||
		usage.Validation.DateLayout != "2006-01-02" ||
		usage.Validation.MaximumDays != 3660 ||
		usage.Validation.Timezone != "UTC" ||
		usage.Balance.AccountID != "meshy" ||
		usage.Balance.HistoryDays != 7 ||
		usage.Balance.SnapshotIDPrefix == "" ||
		usage.Balance.SnapshotWritePolicy != "best-effort" ||
		len(usage.Presentation.QuickRanges) != 3 ||
		usage.Presentation.LoadingRows != 3 ||
		usage.Presentation.Text["ranking_title"] == "" ||
		usage.Messages.InternalError == "" {
		t.Fatalf("administration usage contract = %+v", usage)
	}
}

func TestAdministrationUsageRequiresPositiveMaximumDays(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{0, -1} {
		application := c.Application()
		application.Administration.Usage.Validation.MaximumDays = days
		if err := ValidateApplicationSchema(application); err == nil {
			t.Fatalf("accepted maximum_days=%d", days)
		}
	}
}

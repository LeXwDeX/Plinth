package catalog

import (
	"encoding/json"
	"testing"
)

func TestTaskAssetPresentationRejectsMissingOrUnsafeContract(t *testing.T) {
	cases := map[string]func(*TaskHistoryAssetSchema){
		"attachment title":        func(s *TaskHistoryAssetSchema) { s.Attachments.Title = "" },
		"attachment role label":   func(s *TaskHistoryAssetSchema) { s.Attachments.RoleLabels["report"] = "" },
		"report version":          func(s *TaskHistoryAssetSchema) { s.PrintReport.Format = "" },
		"report MIME":             func(s *TaskHistoryAssetSchema) { s.PrintReport.MediaType = "application/json" },
		"report zero budget":      func(s *TaskHistoryAssetSchema) { s.PrintReport.MaxBytes = 0 },
		"report excessive budget": func(s *TaskHistoryAssetSchema) { s.PrintReport.MaxBytes = 65537 },
		"unknown value":           func(s *TaskHistoryAssetSchema) { s.PrintReport.UnknownValue = "" },
		"diagnostic label":        func(s *TaskHistoryAssetSchema) { delete(s.PrintReport.StatusLabels, "error") },
		"not reported label":      func(s *TaskHistoryAssetSchema) { delete(s.PrintReport.DetailsStatusLabels, "not_reported") },
		"field label":             func(s *TaskHistoryAssetSchema) { delete(s.PrintReport.FieldLabels, "faces_after") },
		"boolean label":           func(s *TaskHistoryAssetSchema) { delete(s.PrintReport.ValueLabels, "false") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			loaded, err := LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := loaded.Application()
			mutate(&app.TaskHistory.Assets)
			if err := validateTaskHistory(app.TaskHistory); err == nil {
				t.Fatal("accepted incomplete attachment/report presentation")
			}
		})
	}
}

func TestTaskAssetPresentationIsPublicAndKeepsReportStatesSeparate(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(loaded.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		TaskHistory TaskHistorySchema `json:"task_history"`
	}
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	assets := public.TaskHistory.Assets
	if assets.Attachments.RoleLabels["report"] == "" || assets.PrintReport.StatusLabels["error"] == "" || assets.PrintReport.NotReported == "" {
		t.Fatal("default presentation missing")
	}
	if assets.PrintReport.Format != "art-ai.print-report.v1" || assets.PrintReport.MediaType != "application/vnd.art-ai.print-report+json" {
		t.Fatal("not the normalized report protocol")
	}
}

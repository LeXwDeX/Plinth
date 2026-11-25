package catalog

import "testing"

// #599: the project-group split of administration usage is declared by Product
// Schema: the wire names of the by_group rows and their per-unit amounts, and
// the page text that states the attribution rule and the per-unit share.
func TestDefaultCatalogDeclaresUsageProjectGroupContract599(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := taskCatalog.Application().Administration.Usage
	fields := usage.API.Fields
	for name, got := range map[string][2]string{
		"project_group_key":  {fields.ProjectGroupKey, "projectGroupKey"},
		"by_group":           {fields.ByGroup, "byGroup"},
		"amounts":            {fields.Amounts, "amounts"},
		"unit":               {fields.Unit, "unit"},
		"unit_label":         {fields.UnitLabel, "unit_label"},
		"amount":             {fields.Amount, "amount"},
		"share_basis_points": {fields.ShareBasisPoints, "share_basis_points"},
	} {
		if got[0] != got[1] {
			t.Fatalf("usage.api.fields.%s = %q, want %q", name, got[0], got[1])
		}
	}
	for _, key := range []string{
		"column_project_group", "group_title", "group_attribution",
		"group_share_note", "group_ungrouped", "column_group_amounts", "group_share",
	} {
		if usage.Presentation.Text[key] == "" {
			t.Fatalf("usage.presentation.text[%q] is empty", key)
		}
	}
	if usage.Presentation.Text["group_title"] != "项目组支出占比" {
		t.Fatalf("group title = %q", usage.Presentation.Text["group_title"])
	}
}

func TestAdministrationUsageSchemaRejectsBrokenProjectGroupContract599(t *testing.T) {
	cases := map[string]func(*AdministrationUsageSchema){
		"missing by_group": func(usage *AdministrationUsageSchema) { usage.API.Fields.ByGroup = " " },
		"missing group key": func(usage *AdministrationUsageSchema) {
			usage.API.Fields.ProjectGroupKey = ""
		},
		"missing share": func(usage *AdministrationUsageSchema) { usage.API.Fields.ShareBasisPoints = "" },
		"by_group collides with data": func(usage *AdministrationUsageSchema) {
			usage.API.Fields.ByGroup = usage.API.Fields.Data
		},
		"group key collides with user name": func(usage *AdministrationUsageSchema) {
			usage.API.Fields.ProjectGroupKey = usage.API.Fields.UserName
		},
		"group key collides with amounts": func(usage *AdministrationUsageSchema) {
			usage.API.Fields.Amounts = usage.API.Fields.ProjectGroupKey
		},
		"amounts collides with task count": func(usage *AdministrationUsageSchema) {
			usage.API.Fields.Amounts = usage.API.Fields.TotalTasks
		},
		"share collides with amount": func(usage *AdministrationUsageSchema) {
			usage.API.Fields.ShareBasisPoints = usage.API.Fields.Amount
		},
		"group key takes the price_totals key": func(usage *AdministrationUsageSchema) {
			usage.API.Fields.ProjectGroupKey = "price_totals"
		},
		"missing text": func(usage *AdministrationUsageSchema) {
			delete(usage.Presentation.Text, "group_attribution")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			taskCatalog, err := LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			application := taskCatalog.Application()
			mutate(&application.Administration.Usage)
			if err := ValidateApplicationSchema(application); err == nil {
				t.Fatal("broken usage project group contract accepted")
			}
		})
	}
}

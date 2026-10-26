package catalog

import "testing"

func TestCoreHTTPRoutesRejectUnexpectedSchemaParameters(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Runtime.HTTP.Routes.TaskAsset =
		"/configured/files/{assetID}/tasks/{taskID}/{other}"
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("accepted unexpected core HTTP route parameter")
	}
}

func TestCoreHTTPRoutesAllowSchemaParameterReordering(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Runtime.HTTP.Routes.TaskAsset =
		"/configured/files/{assetID}/tasks/{taskID}"
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("reordered Schema route rejected: %v", err)
	}
}

func TestTaskHistoryWireSchemaIsRequired(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.TaskHistory.API.Query.Page = ""
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("accepted missing task history wire field")
	}
	application = taskCatalog.Application()
	application.TaskHistory.Assets.PreviewRoles = nil
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("accepted missing task history preview roles")
	}
}

func TestProviderDiscoveryWireSchemaIsRequired(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.ProviderDiscovery.API.Query.Capability = ""
	if err := ValidateApplicationSchema(application); err == nil {
		t.Fatal("accepted missing provider discovery wire field")
	}
}

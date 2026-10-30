package catalog

import "testing"

func TestDefaultCatalogOwnsTaskAndAssetAuditContracts(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	runtime := taskCatalog.Application().Runtime
	if runtime.TaskAudit.CreateAction != "create_task" ||
		runtime.TaskAudit.DeleteAction != "delete_task" ||
		runtime.TaskAudit.ReadOtherAction != "admin_read_task" ||
		runtime.TaskAudit.ResourceType != "task" ||
		runtime.TaskAudit.DetailFields.TaskID != "taskId" ||
		runtime.TaskAudit.DetailFields.OwnerID != "ownerId" ||
		runtime.TaskAudit.DetailFields.Capability != "capability" ||
		runtime.TaskAudit.DetailFields.OfferingID != "offeringId" ||
		runtime.TaskAudit.DetailFields.Replayed != "replayed" {
		t.Fatalf("task audit = %+v", runtime.TaskAudit)
	}
	inputAudit := runtime.InputAssets.Audit
	if inputAudit.UploadAction != "upload_file" ||
		inputAudit.AccessAction != "access_asset" ||
		inputAudit.ResourceType != "asset" ||
		inputAudit.DetailFields.Filename != "filename" ||
		inputAudit.DetailFields.MediaType != "mimeType" ||
		inputAudit.DetailFields.FileSize != "fileSize" {
		t.Fatalf("input asset audit = %+v", inputAudit)
	}
	taskAssetAudit := runtime.AssetDelivery.TaskAssetAudit
	if taskAssetAudit.DownloadAction != "download_asset" ||
		taskAssetAudit.ResourceType != "asset" ||
		taskAssetAudit.DetailFields.TaskID != "taskId" ||
		taskAssetAudit.DetailFields.AssetID != "assetId" {
		t.Fatalf("task asset audit = %+v", taskAssetAudit)
	}
}

func TestTaskAndAssetAuditContractsAreRequired(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ApplicationSchema){
		"task": func(application *ApplicationSchema) {
			application.Runtime.TaskAudit.CreateAction = ""
		},
		"task read other": func(application *ApplicationSchema) {
			application.Runtime.TaskAudit.ReadOtherAction = ""
		},
		"input asset": func(application *ApplicationSchema) {
			application.Runtime.InputAssets.Audit.UploadAction = ""
		},
		"task asset": func(application *ApplicationSchema) {
			application.Runtime.AssetDelivery.TaskAssetAudit.DownloadAction = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			application := taskCatalog.Application()
			mutate(&application)
			if err := ValidateApplicationSchema(application); err == nil {
				t.Fatal("accepted missing Schema-driven audit contract")
			}
		})
	}
}

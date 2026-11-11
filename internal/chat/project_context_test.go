package chat

import "testing"

func TestCopyFrozenProjectReplacesAClientScopeAndCopiesTheSnapshot(t *testing.T) {
	stored := TurnOptions{
		ProjectID: "prj_1",
		ProjectContext: &ProjectContext{
			ID:   "prj_1",
			Name: "Star Harbor",
			StyleGuide: ProjectStyleGuideContext{
				Enabled: true, Text: "warm ink", References: []string{"https://example.test/guide.png"},
			},
			Conventions: ProjectConventionsContext{
				Brief: ProductionBrief{"purpose": []byte(`"character"`)},
				Texts: map[string]string{"naming": "hero_"},
			},
			Assets: []ProjectAssetContext{{
				Handle: "@project1", Kind: "character", Name: "Role A",
				Images: []string{"https://example.test/role-a.png"},
				Models: []ProjectModelContext{{TaskID: "task_1", AssetID: "asset_1"}},
			}},
		},
	}
	options := TurnOptions{
		ProjectID:      "client-project",
		ProjectContext: &ProjectContext{ID: "client-project", Name: "Client"},
	}
	copyFrozenProject(&options, stored)
	if options.ProjectID != "prj_1" || options.ProjectContext == nil || options.ProjectContext.ID != "prj_1" {
		t.Fatalf("the stored project was not copied: %+v", options)
	}
	// The stored snapshot keeps its own backing storage.
	options.ProjectContext.StyleGuide.References[0] = "changed"
	options.ProjectContext.Conventions.Brief["purpose"][0] = 'x'
	options.ProjectContext.Conventions.Texts["naming"] = "changed"
	options.ProjectContext.Assets[0].Images[0] = "changed"
	options.ProjectContext.Assets[0].Models[0].AssetID = "changed"
	if stored.ProjectContext.StyleGuide.References[0] != "https://example.test/guide.png" ||
		string(stored.ProjectContext.Conventions.Brief["purpose"]) != `"character"` ||
		stored.ProjectContext.Conventions.Texts["naming"] != "hero_" ||
		stored.ProjectContext.Assets[0].Images[0] != "https://example.test/role-a.png" ||
		stored.ProjectContext.Assets[0].Models[0].AssetID != "asset_1" {
		t.Fatalf("the stored project snapshot was mutated: %+v", stored.ProjectContext)
	}
}

func TestCopyFrozenProjectLeavesASessionOutsideAnyProjectEmpty(t *testing.T) {
	options := TurnOptions{ProjectID: "client-project"}
	copyFrozenProject(&options, TurnOptions{})
	if options.ProjectID != "" || options.ProjectContext != nil {
		t.Fatalf("a project-less turn kept a client scope: %+v", options)
	}
}

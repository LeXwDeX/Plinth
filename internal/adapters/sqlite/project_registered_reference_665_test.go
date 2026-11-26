package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/project"
)

// #665: the Agent's read-only tools read another session's Task or upload
// only when the session's project stores it: a picture or a model of a
// project asset, or a style guide reference. Another project and another
// owner never match.
func TestProjectRegisteredReferenceMatchesTasksUploadsAndModels665(t *testing.T) {
	ctx := context.Background()
	_, projects := openProjectRepository(t)
	for _, created := range []struct{ id, owner string }{{"p1", "owner-a"}, {"p2", "owner-a"}, {"p3", "owner-b"}} {
		if _, err := projects.CreateProject(ctx, testProject(created.id, created.owner, created.id), 10); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 27, 2, 0, 0, 0, time.UTC)
	if _, err := projects.SetStyleGuide(ctx, "owner-a", "p1", project.StyleGuide{
		ProjectID: "p1", Enabled: true, References: []project.Reference{{InputAssetID: "upload-guide"}}, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.CreateAsset(ctx, "p1", project.Asset{
		ID: "passet_1", ProjectID: "p1", OwnerID: "owner-a", Kind: "character", Name: "Role A",
		Images:    []project.Reference{{TaskID: "task-image", AssetID: "asset-image"}},
		Models:    []project.Reference{{TaskID: "task-model", AssetID: "asset-model"}},
		CreatedAt: now, UpdatedAt: now,
	}, 10); err != nil {
		t.Fatal(err)
	}
	for name, probe := range map[string]struct {
		owner, project string
		reference      project.Reference
		want           bool
	}{
		"image task":         {"owner-a", "p1", project.Reference{TaskID: "task-image"}, true},
		"model task":         {"owner-a", "p1", project.Reference{TaskID: "task-model"}, true},
		"exact model asset":  {"owner-a", "p1", project.Reference{TaskID: "task-model", AssetID: "asset-model"}, true},
		"other asset":        {"owner-a", "p1", project.Reference{TaskID: "task-model", AssetID: "asset-other"}, false},
		"style guide upload": {"owner-a", "p1", project.Reference{InputAssetID: "upload-guide"}, true},
		"unknown task":       {"owner-a", "p1", project.Reference{TaskID: "task-sibling"}, false},
		"other project":      {"owner-a", "p2", project.Reference{TaskID: "task-image"}, false},
		"other owner":        {"owner-b", "p1", project.Reference{TaskID: "task-image"}, false},
		"empty reference":    {"owner-a", "p1", project.Reference{}, false},
	} {
		got, err := projects.RegisteredReference(ctx, probe.owner, probe.project, probe.reference)
		if err != nil || got != probe.want {
			t.Errorf("%s = %v err=%v want %v", name, got, err, probe.want)
		}
	}
}

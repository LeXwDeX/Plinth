package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestSQLiteCanvasRepositoryPreservesOwnershipNodesAndLinkedSession(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "canvas.db")
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	createdAt := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	linkedSession := chat.Session{
		ID:        "canvas-session",
		OwnerID:   "owner-a",
		OwnerName: "Owner A",
		Title:     "Canvas chat",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if _, err := repository.CreateSession(ctx, linkedSession); err != nil {
		t.Fatal(err)
	}
	project := canvas.Project{
		ID:          "canvas-project",
		OwnerID:     "owner-a",
		Name:        "Initial",
		SessionID:   linkedSession.ID,
		OwnsSession: true,
		Viewport:    `{"x":0,"y":0,"zoom":1}`,
		BGStyle:     "grid",
		CreatedAt:   createdAt,
		UpdatedAt:   createdAt,
	}
	created, err := repository.CreateProject(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if created.Nodes == nil || len(created.Nodes) != 0 {
		t.Fatalf("created nodes = %#v, want an empty non-nil slice", created.Nodes)
	}

	if _, found, err := repository.GetProject(
		ctx,
		"owner-b",
		project.ID,
	); err != nil || found {
		t.Fatalf("cross-owner get found=%v error=%v", found, err)
	}
	if replaced, err := repository.ReplaceNodes(
		ctx,
		"owner-b",
		project.ID,
		0,
		[]canvas.Node{{ID: "forbidden"}},
		nil,
		nil,
		50,
	); err != nil || replaced {
		t.Fatalf("cross-owner replace=%v error=%v", replaced, err)
	}

	updatedAt := createdAt.Add(time.Minute)
	nodes := []canvas.Node{
		{
			ID:        "node-top",
			Type:      "image",
			X:         10,
			Y:         20,
			Width:     320,
			Height:    180,
			ZIndex:    2,
			Data:      `{"url":"/asset/top"}`,
			Visible:   true,
			Locked:    true,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
		{
			ID:        "node-bottom",
			Type:      "text",
			X:         -5.5,
			Y:         2.25,
			Width:     100,
			Height:    40,
			ZIndex:    1,
			Data:      `{"text":"Schema"}`,
			Visible:   true,
			Locked:    false,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
	}
	if replaced, err := repository.ReplaceNodes(
		ctx,
		project.OwnerID,
		project.ID,
		0,
		nodes,
		nil,
		nil,
		50,
	); err != nil || !replaced {
		t.Fatalf("replace=%v error=%v", replaced, err)
	}
	invalidNodes := []canvas.Node{
		nodes[0],
		nodes[0],
	}
	if replaced, err := repository.ReplaceNodes(
		ctx,
		project.OwnerID,
		project.ID,
		0,
		invalidNodes,
		nil,
		nil,
		50,
	); err == nil || replaced {
		t.Fatalf("invalid replace=%v error=%v, want rollback error", replaced, err)
	}
	afterRollback, found, err := repository.GetProject(
		ctx,
		project.OwnerID,
		project.ID,
	)
	if err != nil || !found || len(afterRollback.Nodes) != len(nodes) {
		t.Fatalf(
			"nodes after failed replacement=%#v found=%v error=%v",
			afterRollback.Nodes,
			found,
			err,
		)
	}

	project.Name = "Updated"
	project.Viewport = `{"x":12,"y":24,"zoom":0.75}`
	project.BGStyle = "dots"
	project.UpdatedAt = updatedAt
	if updated, err := repository.UpdateProject(
		ctx,
		canvas.Project{
			ID:        project.ID,
			OwnerID:   "owner-b",
			Name:      "Forbidden",
			Viewport:  project.Viewport,
			BGStyle:   project.BGStyle,
			UpdatedAt: updatedAt,
		},
	); err != nil || updated {
		t.Fatalf("cross-owner update=%v error=%v", updated, err)
	}
	if updated, err := repository.UpdateProject(
		ctx,
		project,
	); err != nil || !updated {
		t.Fatalf("update=%v error=%v", updated, err)
	}

	summaries, err := repository.ListProjects(ctx, project.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 ||
		summaries[0].ID != project.ID ||
		summaries[0].NodeCount != len(nodes) ||
		summaries[0].SessionID != linkedSession.ID {
		t.Fatalf("summaries = %#v", summaries)
	}
	if crossOwner, err := repository.ListProjects(ctx, "owner-b"); err != nil ||
		len(crossOwner) != 0 {
		t.Fatalf("cross-owner summaries=%#v error=%v", crossOwner, err)
	}

	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	persisted, found, err := repository.GetProject(
		ctx,
		project.OwnerID,
		project.ID,
	)
	if err != nil || !found {
		t.Fatalf("persisted found=%v error=%v", found, err)
	}
	if persisted.Name != project.Name ||
		persisted.Viewport != project.Viewport ||
		persisted.BGStyle != project.BGStyle ||
		len(persisted.Nodes) != 2 {
		t.Fatalf("persisted project = %#v", persisted)
	}
	if persisted.Nodes[0].ID != "node-bottom" ||
		persisted.Nodes[0].ProjectID != project.ID ||
		persisted.Nodes[1].ID != "node-top" ||
		!persisted.Nodes[1].Visible ||
		!persisted.Nodes[1].Locked {
		t.Fatalf("persisted nodes = %#v", persisted.Nodes)
	}

	if deleted, err := repository.DeleteProjectAndSession(
		ctx,
		"owner-b",
		project.ID,
	); err != nil || deleted {
		t.Fatalf("cross-owner delete=%v error=%v", deleted, err)
	}
	if owns, err := repository.OwnsSession(
		ctx,
		project.OwnerID,
		linkedSession.ID,
	); err != nil || !owns {
		t.Fatalf("linked session before delete owns=%v error=%v", owns, err)
	}
	if deleted, err := repository.DeleteProjectAndSession(
		ctx,
		project.OwnerID,
		project.ID,
	); err != nil || !deleted {
		t.Fatalf("delete=%v error=%v", deleted, err)
	}
	if _, found, err := repository.GetProject(
		ctx,
		project.OwnerID,
		project.ID,
	); err != nil || found {
		t.Fatalf("deleted project found=%v error=%v", found, err)
	}
	if owns, err := repository.OwnsSession(
		ctx,
		project.OwnerID,
		linkedSession.ID,
	); err != nil || owns {
		t.Fatalf("linked session after delete owns=%v error=%v", owns, err)
	}
}

func TestWorkspaceCanvasDeletionPreservesExistingChatSession(t *testing.T) {
	ctx := context.Background()
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "workspace-canvas.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	session := chat.Session{ID: "creative-session", OwnerID: "owner", OwnerName: "Owner", Title: "Task", CreatedAt: now, UpdatedAt: now}
	if _, err := repository.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	project := canvas.Project{ID: "board", OwnerID: "owner", Name: "Board", SessionID: session.ID,
		OwnsSession: false, Viewport: `{}`, BGStyle: "dots", CreatedAt: now, UpdatedAt: now}
	if _, err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repository.GetProjectBySession(ctx, "foreign", session.ID); err != nil || found {
		t.Fatalf("foreign lookup: found=%v err=%v", found, err)
	}
	loaded, found, err := repository.GetProjectBySession(ctx, "owner", session.ID)
	if err != nil || !found || loaded.ID != project.ID || loaded.OwnsSession {
		t.Fatalf("owned lookup: %+v found=%v err=%v", loaded, found, err)
	}
	if deleted, err := repository.DeleteProjectAndSession(ctx, "owner", project.ID); err != nil || !deleted {
		t.Fatalf("delete board: %v %v", deleted, err)
	}
	if owned, err := repository.OwnsSession(ctx, "owner", session.ID); err != nil || !owned {
		t.Fatalf("chat session after board deletion: owned=%v err=%v", owned, err)
	}
	if _, err := repository.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	if deleted, err := repository.DeleteSession(ctx, "owner", session.ID); err != nil || !deleted {
		t.Fatalf("delete chat session: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := repository.GetProject(ctx, "owner", project.ID); err != nil || found {
		t.Fatalf("canvas after chat session deletion: found=%v err=%v", found, err)
	}
}

func TestSQLiteCanvasRepositoryRevisionLockAndCreatedAtPreservation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "canvas-revision.db")
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	createdAt := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	for _, session := range []chat.Session{
		{ID: "revision-session-a", OwnerID: "owner-a", CreatedAt: createdAt, UpdatedAt: createdAt},
		{ID: "revision-session-b", OwnerID: "owner-b", CreatedAt: createdAt, UpdatedAt: createdAt},
	} {
		if _, err := repository.CreateSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	project, err := repository.CreateProject(ctx, canvas.Project{
		ID:        "revision-project",
		OwnerID:   "owner-a",
		Name:      "Revision",
		SessionID: "revision-session-a",
		Viewport:  `{"x":0,"y":0,"zoom":1}`,
		BGStyle:   "grid",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	node := canvas.Node{
		ID:        "node-a",
		Type:      "image",
		ProjectID: project.ID,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if _, err := repository.ReplaceNodes(
		ctx,
		"owner-a",
		project.ID,
		1,
		[]canvas.Node{node},
		nil,
		nil,
		50,
	); err != nil {
		t.Fatal(err)
	}

	// 跨项目同 ID 节点不再因主键冲突互相覆盖（B15 复合主键）。
	other, err := repository.CreateProject(ctx, canvas.Project{
		ID:        "revision-project-b",
		OwnerID:   "owner-b",
		Name:      "Revision B",
		SessionID: "revision-session-b",
		Viewport:  `{"x":0,"y":0,"zoom":1}`,
		BGStyle:   "grid",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ReplaceNodes(
		ctx,
		"owner-b",
		other.ID,
		1,
		[]canvas.Node{node},
		nil,
		nil,
		50,
	); err != nil {
		t.Fatal(err)
	}

	// 陈旧 revision 被拒绝，节点不被覆盖。
	stale := node
	stale.Data = "stale"
	if _, err := repository.ReplaceNodes(
		ctx,
		"owner-a",
		project.ID,
		1,
		[]canvas.Node{stale},
		nil,
		nil,
		50,
	); !errors.Is(err, canvas.ErrRevisionConflict) {
		t.Fatalf("stale revision error=%v, want ErrRevisionConflict", err)
	}

	// 当前 revision 保存成功且 revision 自增。
	fresh := node
	fresh.Data = "fresh"
	if replaced, err := repository.ReplaceNodes(
		ctx,
		"owner-a",
		project.ID,
		2,
		[]canvas.Node{fresh},
		nil,
		nil,
		50,
	); err != nil || !replaced {
		t.Fatalf("fresh replace=%v error=%v", replaced, err)
	}

	// CreatedAt 保留：ReplaceNodes 不重置创建时间（service 层负责传入，
	// 仓储侧验证原值不丢——直接检查读回的节点）。
	stored, found, err := repository.GetProject(ctx, "owner-a", project.ID)
	if err != nil || !found {
		t.Fatalf("get project found=%v error=%v", found, err)
	}
	if len(stored.Nodes) != 1 {
		t.Fatalf("stored nodes=%d, want 1", len(stored.Nodes))
	}
	if !stored.Nodes[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("stored created_at=%v, want %v", stored.Nodes[0].CreatedAt, createdAt)
	}
	if stored.Nodes[0].Data != "fresh" {
		t.Fatalf("stored data=%q, want fresh", stored.Nodes[0].Data)
	}
}

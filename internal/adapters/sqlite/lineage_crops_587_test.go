package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// #587: the Agent's crops are input assets with no session column; the
// session's chat attachments (role crop) are their only tie to it.
func TestSessionCropsReadTheSessionsCropAttachments(t *testing.T) {
	repo, err := openTestRepository(filepath.Join(t.TempDir(), "crops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := repo.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	stamp := "2026-01-01T00:00:00Z"
	for _, row := range [][2]string{{"owner-a", "session-a"}, {"owner-a", "session-other"}, {"owner-b", "session-b"}} {
		exec(`INSERT INTO go_chat_sessions(id,owner_id,owner_name,title,created_at,updated_at) VALUES (?,?,?,?,?,?)`, row[1], row[0], "n", "t", stamp, stamp)
	}
	for _, asset := range []struct{ id, owner, kind, created string }{
		{"crop-1", "owner-a", "list_crop", "2026-01-01T00:01:00Z"},
		{"crop-2", "owner-a", "list_crop", "2026-01-01T00:02:00.5Z"},
		{"crop-other", "owner-a", "list_crop", "2026-01-01T00:03:00Z"},
		{"upload-1", "owner-a", "image", "2026-01-01T00:04:00Z"},
		{"crop-foreign", "owner-b", "list_crop", "2026-01-01T00:05:00Z"},
	} {
		exec(`INSERT INTO generation_input_assets(id,owner_id,storage_key,asset_type,media_type,size_bytes,sha256,created_at) VALUES (?,?,?,?,?,?,?,?)`,
			asset.id, asset.owner, asset.id+".png", asset.kind, "image/png", 4, "h-"+asset.id, asset.created)
	}
	exec(`INSERT INTO input_asset_crops(crop_asset_id,owner_id,source_asset_id,source_kind,source_input_asset_id,source_width,source_height,region_json,x0,y0,x1,y1,width,height,sha256,created_at)
		VALUES ('crop-1','owner-a','task-asset-1','task_asset','copy-1',100,100,'{}',0,0,50,50,50,50,'h-crop-1',?)`, stamp)
	message := func(id, session, attachments string) {
		exec(`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			id, session, "assistant", "", "[]", "[]", attachments, "", stamp)
	}
	message("m1", "session-a", `[{"handle":"@img2","kind":"image","role":"crop","asset_id":"crop-1"},{"handle":"@img3","kind":"image","role":"crop","asset_id":"crop-2"}]`)
	// The same crop shown again is listed once; an upload attachment is not a crop.
	message("m2", "session-a", `[{"handle":"@img2","kind":"image","role":"crop","asset_id":"crop-1"},{"handle":"@img4","kind":"image","role":"upload","asset_id":"upload-1"}]`)
	// A crop attachment that names another owner's asset, or a non-crop asset, is not listed.
	message("m3", "session-a", `[{"kind":"image","role":"crop","asset_id":"crop-foreign"},{"kind":"image","role":"crop","asset_id":"upload-1"}]`)
	message("m4", "session-a", `[]`)
	message("m5", "session-other", `[{"kind":"image","role":"crop","asset_id":"crop-other"}]`)

	crops, err := repo.SessionCrops(ctx, "owner-a", "session-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(crops) != 2 || crops[0].Asset.ID != "crop-2" || crops[1].Asset.ID != "crop-1" {
		t.Fatalf("crops = %+v", crops)
	}
	if crops[1].SourceAssetID != "task-asset-1" || crops[0].SourceAssetID != "" || crops[1].Asset.AssetType != "list_crop" || crops[1].Asset.OwnerID != "owner-a" {
		t.Fatalf("crop facts = %+v", crops)
	}
	limited, err := repo.SessionCrops(ctx, "owner-a", "session-a", 1)
	if err != nil || len(limited) != 1 || limited[0].Asset.ID != "crop-2" {
		t.Fatalf("limited = %+v err=%v", limited, err)
	}
	// Another owner cannot read the session's crops by naming it.
	foreign, err := repo.SessionCrops(ctx, "owner-b", "session-a", 10)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("foreign = %+v err=%v", foreign, err)
	}
}

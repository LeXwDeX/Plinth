package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestBranchCopiesCurrentCanvas(t *testing.T) {
	for _, withCanvas := range []bool{false, true} {
		t.Run(fmt.Sprintf("canvas=%v", withCanvas), func(t *testing.T) {
			r := openApprovalRepo(t)
			createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Parent"})
			if _, err := r.db.Exec(`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m','parent','user','message','[]','[]','[]','','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			if withCanvas {
				for _, query := range []string{
					`INSERT INTO go_canvas_projects(id,owner_id,name,session_id,owns_session,viewport,bg_style,created_at,updated_at,revision,placed_results_json,canvas_width,canvas_height,coordinate_version) VALUES ('original','owner','art','parent',1,'{"zoom":2}','grid','2026-01-01','2026-01-02',7,'["task"]',1200,800,2)`,
					`INSERT INTO go_canvas_nodes(project_id,id,type,x,y,width,height,z_index,data_json,visible,locked,created_at,updated_at) VALUES ('original','group','group',1,2,3,4,8,'{"groupId":"group"}',1,0,'2026-01-01','2026-01-02'),('original','child','image',5,6,7,8,9,'{"parentId":"group","url":"/asset"}',0,1,'2026-01-03','2026-01-04')`,
				} {
					if _, err := r.db.Exec(query); err != nil {
						t.Fatal(err)
					}
				}
			}
			request := chat.BranchRequest{OwnerID: "owner", SessionID: "parent", MessageID: "m", NewSessionID: "branch", TitleTemplate: "{title} branch", At: time.Now(), CanvasProjectIDPrefix: "canvas", ActiveStates: []string{"queued", "running", "waiting_task"}, NewID: func(kind string) string { return kind + "-new" }}
			branch, err := r.BranchSession(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if branch.BranchCanvasCopied != withCanvas {
				t.Fatalf("canvas copied=%v, want %v", branch.BranchCanvasCopied, withCanvas)
			}
			var projects int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_canvas_projects WHERE session_id='branch'`).Scan(&projects); err != nil || (projects == 1) != withCanvas {
				t.Fatalf("projects=%d err=%v", projects, err)
			}
			if !withCanvas {
				return
			}
			var different int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM (SELECT name,viewport,bg_style,created_at,updated_at,revision,placed_results_json,canvas_width,canvas_height,coordinate_version FROM go_canvas_projects WHERE id='original' EXCEPT SELECT name,viewport,bg_style,created_at,updated_at,revision,placed_results_json,canvas_width,canvas_height,coordinate_version FROM go_canvas_projects WHERE id='canvas-new' AND owner_id='owner' AND session_id='branch' AND owns_session=0)`).Scan(&different); err != nil || different != 0 {
				t.Fatalf("project difference=%d err=%v", different, err)
			}
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM (SELECT id,type,x,y,width,height,z_index,data_json,visible,locked,created_at,updated_at FROM go_canvas_nodes WHERE project_id='original' EXCEPT SELECT id,type,x,y,width,height,z_index,data_json,visible,locked,created_at,updated_at FROM go_canvas_nodes WHERE project_id='canvas-new')`).Scan(&different); err != nil || different != 0 {
				t.Fatalf("node difference=%d err=%v", different, err)
			}
			var nodes int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_canvas_nodes WHERE project_id='canvas-new'`).Scan(&nodes); err != nil || nodes != 2 {
				t.Fatalf("nodes=%d err=%v", nodes, err)
			}
			if _, err := r.db.Exec(`UPDATE go_canvas_nodes SET x=999 WHERE project_id='canvas-new' AND id='group'`); err != nil {
				t.Fatal(err)
			}
			var sourceCount int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_canvas_projects p JOIN go_canvas_nodes n ON n.project_id=p.id WHERE p.id='original' AND p.owner_id='owner' AND p.session_id='parent' AND p.owns_session=1 AND p.revision=7 AND n.id='group' AND n.x=1`).Scan(&sourceCount); err != nil || sourceCount != 1 {
				t.Fatalf("source changed: count=%d err=%v", sourceCount, err)
			}
			if deleted, err := r.DeleteProjectAndSession(context.Background(), "owner", "canvas-new"); err != nil || !deleted {
				t.Fatalf("delete canvas: %v %v", deleted, err)
			}
			if owned, err := r.OwnsSession(context.Background(), "owner", "branch"); err != nil || !owned {
				t.Fatalf("branch deleted with canvas: %v %v", owned, err)
			}
		})
	}
}

func TestBranchCanvasFailureRollsBackAllWrites(t *testing.T) {
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Parent"})
	for _, query := range []string{
		`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m','parent','user','message','[]','[]','[]','','2026-01-01T00:00:00Z')`,
		`INSERT INTO go_canvas_projects(id,owner_id,name,session_id,viewport,bg_style,created_at,updated_at) VALUES ('original','owner','art','parent','{}','grid','2026-01-01','2026-01-01')`,
		`INSERT INTO go_canvas_nodes(project_id,id,type,x,y,width,height,z_index,data_json,visible,locked,created_at,updated_at) VALUES ('original','node','image',1,2,3,4,0,'{}',1,0,'2026-01-01','2026-01-01')`,
		`CREATE TRIGGER fail_canvas_copy BEFORE INSERT ON go_canvas_nodes WHEN NEW.project_id='canvas-new' BEGIN SELECT RAISE(ABORT,'forced'); END`,
	} {
		if _, err := r.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.BranchSession(context.Background(), chat.BranchRequest{OwnerID: "owner", SessionID: "parent", MessageID: "m", NewSessionID: "branch", TitleTemplate: "{title} branch", At: time.Now(), CanvasProjectIDPrefix: "canvas", ActiveStates: []string{"queued", "running", "waiting_task"}, NewID: func(kind string) string { return kind + "-new" }})
	if err == nil {
		t.Fatal("expected canvas copy failure")
	}
	for _, q := range []string{`SELECT COUNT(*) FROM go_chat_sessions WHERE id='branch'`, `SELECT COUNT(*) FROM go_chat_messages WHERE session_id='branch'`, `SELECT COUNT(*) FROM go_canvas_projects WHERE session_id='branch'`, `SELECT COUNT(*) FROM go_canvas_nodes WHERE project_id='canvas-new'`} {
		var count int
		if err := r.db.QueryRow(q).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: %d %v", q, count, err)
		}
	}
}

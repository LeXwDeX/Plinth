package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestBranchCopiesMessagesAndSettingsAtomically(t *testing.T) {
	ctx := context.Background()
	r := openApprovalRepo(t)
	createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Original"})
	if _, err := r.db.Exec(`UPDATE go_chat_sessions SET notes='notes', brief='{}', chat_model='model', reasoning_effort='high', picked='pick', approval_mode='auto', agent_scope='2d' WHERE id='parent'`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{
		`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m1','parent','user','one','[]','[]','[]','','2026-01-01T00:00:00Z')`,
		`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m2','parent','assistant','two','["url"]','[{"name":"tool"}]','[{"kind":"image"}]','{}','2026-01-01T00:00:00Z')`,
		`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m3','parent','user','three','[]','[]','[]','','2026-01-02T00:00:00Z')`,
		`INSERT INTO chat_context_compactions(id,owner_id,session_id,trigger,summary,through_message_id,created_at) VALUES ('c','owner','parent','manual','summary','m1','2026-01-03T00:00:00Z')`,
		`INSERT INTO chat_choices(id,owner_id,session_id,assistant_message_id,call_id,tool_name,capability,step_index,options_json,default_option_id,allow_default,default_arguments,state,created_at) VALUES ('choice','owner','parent','m2','call','tool','cap',0,'[]','',0,'{}','answered','2026-01-03T00:00:00Z')`,
	} {
		if _, err := r.db.Exec(row); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	newID := func(string) string {
		n++
		if n == 1 {
			return "copy1"
		}
		return "copy2"
	}
	request := chat.BranchRequest{OwnerID: "owner", SessionID: "parent", MessageID: "m2", NewSessionID: "branch", TitleTemplate: "{title}（分支）", At: time.Now(), NewID: newID, ActiveStates: []string{"running", "queued", "waiting_task"}}
	branch, err := r.BranchSession(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if branch.Title != "Original（分支）" || branch.TitleSource != chat.TitleSourceAuto || branch.ParentDeleted || branch.ParentSessionID != "parent" || branch.ParentMessageID != "m2" || branch.ParentTitle != "Original" || branch.Messages != 2 || branch.ApprovalMode != "auto" || branch.AgentScope != "2d" {
		t.Fatalf("branch: %+v", branch)
	}
	var notes, model, effort, picked string
	if err := r.db.QueryRow(`SELECT notes,chat_model,reasoning_effort,picked FROM go_chat_sessions WHERE id='branch'`).Scan(&notes, &model, &effort, &picked); err != nil || notes != "notes" || model != "model" || effort != "high" || picked != "pick" {
		t.Fatalf("settings: %q %q %q %q %v", notes, model, effort, picked, err)
	}
	var content, images, tools, attachments, multiview string
	if err := r.db.QueryRow(`SELECT content,image_urls,tool_calls,attachments,multiview FROM go_chat_messages WHERE id='copy2' AND session_id='branch'`).Scan(&content, &images, &tools, &attachments, &multiview); err != nil || content != "two" || images != `["url"]` || tools != `[{"name":"tool"}]` || attachments != `[{"kind":"image"}]` || multiview != `{}` {
		t.Fatalf("copy: %q %q %q %q %q %v", content, images, tools, attachments, multiview, err)
	}
	if _, err := r.BranchSession(ctx, chat.BranchRequest{OwnerID: "other", SessionID: "parent", MessageID: "m2"}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign: %v", err)
	}
	if _, err := r.BranchSession(ctx, chat.BranchRequest{OwnerID: "owner", SessionID: "branch", MessageID: "m2"}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("message in another session: %v", err)
	}
	for _, table := range []string{"chat_choices", "chat_context_compactions", "go_chat_turns", "go_chat_streams", "go_chat_events"} {
		var count int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE session_id='branch'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	var parentCount int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_chat_messages WHERE session_id='parent'`).Scan(&parentCount); err != nil || parentCount != 3 {
		t.Fatalf("parent messages %d err=%v", parentCount, err)
	}
}

func TestBranchRejectsUnsafeStatesAndRollsBack(t *testing.T) {
	for _, test := range []struct {
		name, setup string
		want        error
	}{
		{"running", `INSERT INTO go_chat_turns(assistant_message_id,session_id,owner_id,options_json,state,created_at,updated_at) VALUES ('m','parent','owner','{}','running','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, chat.ErrTurnActive},
		{"pending grant", `INSERT INTO execution_grants(request_id,owner_id,session_id,offering_id,offering_revision,prepared_input_id,prepared_input_hash,idempotency_key,operation_id,status,source,request_version,created_at,updated_at) VALUES ('g','owner','parent','offering','1','input','hash','key','op','pending','human_browser',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, chat.ErrBranchPendingApproval},
		{"pending budget", `INSERT INTO production_budgets(id,owner_id,session_id,plan_json,limits_json,state,created_at) VALUES ('b','owner','parent','{}','[{}]','pending','2026-01-01T00:00:00Z')`, chat.ErrBranchPendingApproval},
		{"scene", `INSERT INTO scene_objects(id,owner_id,session_id,revision,active_representation_id,rect_x,rect_y,rect_width,rect_height,anchor_x,anchor_y,created_at,updated_at) VALUES ('o','owner','parent',1,'r',0,0,1,1,0,0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, chat.ErrBranchScene},
		{"insert failure", `CREATE TRIGGER fail_branch_copy BEFORE INSERT ON go_chat_messages WHEN NEW.session_id='branch' BEGIN SELECT RAISE(ABORT, 'forced failure'); END`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := openApprovalRepo(t)
			ctx := context.Background()
			createApprovalSession(t, r, "parent", "owner", chat.Session{Title: "Parent"})
			if _, err := r.db.Exec(`INSERT INTO go_chat_messages(id,session_id,role,content,image_urls,tool_calls,attachments,multiview,created_at) VALUES ('m','parent','user','one','[]','[]','[]','','2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			if _, err := r.db.Exec(test.setup); err != nil {
				t.Fatal(err)
			}
			_, err := r.BranchSession(ctx, chat.BranchRequest{OwnerID: "owner", SessionID: "parent", MessageID: "m", NewSessionID: "branch", TitleTemplate: "{title} branch", At: time.Now(), NewID: func(string) string { return "copy" }, ActiveStates: []string{"queued", "running", "waiting_task"}})
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
			var count int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM go_chat_sessions WHERE id='branch'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("orphan count=%d err=%v", count, err)
			}
		})
	}
}

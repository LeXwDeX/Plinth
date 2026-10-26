package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

type threeViewSlot struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Status        string `json:"status"`
	Attempt       int    `json:"attempt"`
	TaskID        string `json:"task_id"`
	OutputAssetID string `json:"output_asset_id"`
	Startable     bool   `json:"startable"`
	Retryable     bool   `json:"retryable"`
}

type threeViewGroup struct {
	RunID       string          `json:"run_id"`
	Kind        string          `json:"kind"`
	Label       string          `json:"label"`
	Complete    bool            `json:"complete"`
	Slots       []threeViewSlot `json:"slots"`
	ModelSource struct {
		Slot      string `json:"slot"`
		Available bool   `json:"available"`
		TaskID    string `json:"task_id"`
		AssetID   string `json:"asset_id"`
		Note      string `json:"note"`
	} `json:"model_source"`
}

func (g threeViewGroup) slot(t *testing.T, key string) threeViewSlot {
	t.Helper()
	for _, slot := range g.Slots {
		if slot.Key == key {
			return slot
		}
	}
	t.Fatalf("group has no %q slot: %+v", key, g.Slots)
	return threeViewSlot{}
}

// threeViewWorld is a session with one three-view run whose views have not
// started, the way the Agent tool leaves the other two.
type threeViewWorld struct {
	pendingApprovalWorld
	runID string
}

func newThreeViewWorld(t *testing.T) threeViewWorld {
	t.Helper()
	world := pendingApprovalBrowsers(t, nil)
	asset := uploadSkillSource(t, world.handler, world.cookie)
	create := grantTestRequest(t, world.handler, world.cookie, http.MethodPost, "/api/v1/skill-runs",
		`{"session_id":"`+world.sessionID+`","recipe_id":"image-three-view","recipe_revision":"2026-10-13.1",`+
			`"offering_id":"openai/gpt-image-2/image-to-image","source_asset_id":"`+asset+`"}`, "three-view-group")
	var run struct {
		ID string `json:"id"`
	}
	if create.Code != http.StatusCreated || json.Unmarshal(create.Body.Bytes(), &run) != nil || run.ID == "" {
		t.Fatalf("create run: %d %s", create.Code, create.Body.String())
	}
	return threeViewWorld{pendingApprovalWorld: world, runID: run.ID}
}

func (w threeViewWorld) groups(t *testing.T, sessionID string) []threeViewGroup {
	t.Helper()
	response := grantTestRequest(t, w.handler, w.cookie, http.MethodGet,
		"/api/v1/chat/sessions/"+sessionID+"/three-views", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list groups: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Items []threeViewGroup `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Items
}

func (w threeViewWorld) advance(t *testing.T, view, action string, attempt int) (int, threeViewGroup) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"action": action, "attempt": attempt})
	response := grantTestRequest(t, w.handler, w.cookie, http.MethodPost,
		"/api/v1/chat/sessions/"+w.sessionID+"/three-views/"+w.runID+"/views/"+view, string(body), "")
	var result struct {
		Group threeViewGroup `json:"group"`
	}
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("advance body=%s err=%v", response.Body.String(), err)
		}
	}
	return response.Code, result.Group
}

func (w threeViewWorld) tasks(t *testing.T) int { return w.taskCount(t) }

func TestThreeViewGroupProducesAllThreeViewsOneAtATime(t *testing.T) {
	t.Parallel()
	world := newThreeViewWorld(t)
	groups := world.groups(t, world.sessionID)
	if len(groups) != 1 || groups[0].RunID != world.runID || groups[0].Kind != "three_view" ||
		groups[0].Label != "三视图" || groups[0].Complete || len(groups[0].Slots) != 3 {
		t.Fatalf("fresh group = %+v", groups)
	}
	for index, key := range []string{"front", "side", "back"} {
		slot := groups[0].Slots[index]
		if slot.Key != key || slot.Status != "not_started" || !slot.Startable || slot.TaskID != "" || slot.Retryable {
			t.Fatalf("fresh %s slot = %+v", key, slot)
		}
	}
	if groups[0].ModelSource.Slot != "front" || groups[0].ModelSource.Available || groups[0].ModelSource.Note == "" {
		t.Fatalf("the 3D source is the front view and says so: %+v", groups[0].ModelSource)
	}

	// Starting a view is one paid task, and a repeat is the same task.
	status, started := world.advance(t, "front", "start", 0)
	front := started.slot(t, "front")
	if status != http.StatusOK || front.TaskID == "" || front.Status == "not_started" || front.Startable {
		t.Fatalf("start front: %d %+v", status, started.Slots)
	}
	if status, again := world.advance(t, "front", "start", 0); status != http.StatusOK || again.slot(t, "front").TaskID != front.TaskID {
		t.Fatalf("double start: %d %+v", status, again.Slots)
	}
	if world.tasks(t) != 1 {
		t.Fatalf("double start made %d tasks", world.tasks(t))
	}
	if started.slot(t, "side").TaskID != "" || started.slot(t, "back").TaskID != "" {
		t.Fatalf("starting the front started another view: %+v", started.Slots)
	}

	for _, key := range []string{"side", "back"} {
		if status, group := world.advance(t, key, "start", 0); status != http.StatusOK || group.slot(t, key).TaskID == "" {
			t.Fatalf("start %s: %d %+v", key, status, group.Slots)
		}
	}
	if world.tasks(t) != 3 {
		t.Fatalf("three views made %d tasks", world.tasks(t))
	}
	groups = world.groups(t, world.sessionID)
	for _, slot := range groups[0].Slots {
		world.runTasksToCompletion(t, slot.TaskID)
	}
	groups = world.groups(t, world.sessionID)
	if !groups[0].Complete {
		t.Fatalf("all three finished but the group is not complete: %+v", groups[0].Slots)
	}
	for _, slot := range groups[0].Slots {
		if slot.Status != "succeeded" || slot.OutputAssetID == "" || slot.Retryable {
			t.Fatalf("finished slot = %+v", slot)
		}
	}
	source := groups[0].ModelSource
	if !source.Available || source.TaskID != groups[0].slot(t, "front").TaskID ||
		source.AssetID != groups[0].slot(t, "front").OutputAssetID {
		t.Fatalf("the 3D source must be the front view's picture: %+v", source)
	}

	// The version strip sees one group of three finished tasks.
	lineage := grantTestRequest(t, world.handler, world.cookie, http.MethodGet,
		"/api/v1/chat/sessions/"+world.sessionID+"/lineage", "", "")
	var graph struct {
		Nodes []struct {
			TaskID string `json:"task_id"`
			Group  string `json:"group"`
			Slot   string `json:"slot"`
		} `json:"nodes"`
		Groups []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			Label      string `json:"label"`
			Complete   bool   `json:"complete"`
			SourceSlot string `json:"source_slot"`
			Members    []struct {
				Slot   string `json:"slot"`
				TaskID string `json:"task_id"`
			} `json:"members"`
		} `json:"groups"`
	}
	if lineage.Code != http.StatusOK || json.Unmarshal(lineage.Body.Bytes(), &graph) != nil ||
		len(graph.Groups) != 1 || graph.Groups[0].ID != world.runID || !graph.Groups[0].Complete ||
		graph.Groups[0].Kind != "three_view" || graph.Groups[0].SourceSlot != "front" || len(graph.Groups[0].Members) != 3 {
		t.Fatalf("lineage groups: %d %s", lineage.Code, lineage.Body.String())
	}
	grouped := 0
	for _, node := range graph.Nodes {
		if node.Group == world.runID && node.Slot != "" {
			grouped++
		}
	}
	if grouped != 3 {
		t.Fatalf("%d of 3 nodes carry the group: %s", grouped, lineage.Body.String())
	}
}

func TestThreeViewFailedViewIsRetriedAlone(t *testing.T) {
	t.Parallel()
	world := newThreeViewWorld(t)
	tasks := map[string]string{}
	start := func(key string) {
		status, group := world.advance(t, key, "start", 0)
		if status != http.StatusOK {
			t.Fatalf("start %s: %d", key, status)
		}
		tasks[key] = group.slot(t, key).TaskID
	}
	start("front")
	start("back")
	world.runTasksToCompletion(t, tasks["front"])
	world.runTasksToCompletion(t, tasks["back"])
	start("side")

	// A running view cannot be retried, and a finished one cannot either.
	if status, _ := world.advance(t, "side", "retry", 0); status != http.StatusConflict {
		t.Fatalf("retry of a running view = %d", status)
	}
	if status, _ := world.advance(t, "front", "retry", 0); status != http.StatusConflict {
		t.Fatalf("retry of a finished view = %d", status)
	}

	// The person cancels the side view; only that view is started again.
	cancel := grantTestRequest(t, world.handler, world.cookie, http.MethodPost, "/api/v1/tasks/"+tasks["side"]+"/cancel", "{}", "")
	if cancel.Code != http.StatusOK && cancel.Code != http.StatusAccepted {
		t.Fatalf("cancel side: %d %s", cancel.Code, cancel.Body.String())
	}
	group := world.groups(t, world.sessionID)[0]
	if side := group.slot(t, "side"); side.Status != "canceled" || !side.Retryable || side.Attempt != 0 {
		t.Fatalf("canceled side = %+v", side)
	}
	status, retried := world.advance(t, "side", "retry", 0)
	side := retried.slot(t, "side")
	if status != http.StatusOK || side.Attempt != 1 || side.TaskID == "" || side.TaskID == tasks["side"] || side.Status == "canceled" {
		t.Fatalf("retry side: %d %+v", status, retried.Slots)
	}
	if retried.slot(t, "front").TaskID != tasks["front"] || retried.slot(t, "back").TaskID != tasks["back"] ||
		retried.slot(t, "front").Status != "succeeded" || retried.slot(t, "back").Status != "succeeded" {
		t.Fatalf("a retry moved another view: %+v", retried.Slots)
	}
	if world.tasks(t) != 4 {
		t.Fatalf("retrying one view made %d tasks in all", world.tasks(t))
	}
	// A repeated retry (double click, lost response) is the same attempt.
	if status, again := world.advance(t, "side", "retry", 0); status != http.StatusOK ||
		again.slot(t, "side").TaskID != side.TaskID || again.slot(t, "side").Attempt != 1 {
		t.Fatalf("repeated retry: %d %+v", status, again.Slots)
	}
	if world.tasks(t) != 4 {
		t.Fatalf("repeated retry made %d tasks", world.tasks(t))
	}
	// A retry that names an attempt the view never had is refused.
	if status, _ := world.advance(t, "side", "retry", 7); status != http.StatusConflict {
		t.Fatalf("retry of a future attempt = %d", status)
	}
	world.runTasksToCompletion(t, side.TaskID)
	if group := world.groups(t, world.sessionID)[0]; !group.Complete || group.slot(t, "side").Attempt != 1 {
		t.Fatalf("after the retry the group completes: %+v", group.Slots)
	}
}

func TestThreeViewGroupsStayWithTheirOwner(t *testing.T) {
	t.Parallel()
	world := newThreeViewWorld(t)
	other := grantTestRequest(t, world.handler, world.other, http.MethodPost, "/api/chat/sessions", `{"title":"other"}`, "")
	var otherSession struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(other.Body.Bytes(), &otherSession); err != nil || otherSession.ID == "" {
		t.Fatalf("other session: %s", other.Body.String())
	}
	foreign := grantTestRequest(t, world.handler, world.other, http.MethodGet,
		"/api/v1/chat/sessions/"+world.sessionID+"/three-views", "", "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("another user listed the groups of a session: %d %s", foreign.Code, foreign.Body.String())
	}
	startForeign := grantTestRequest(t, world.handler, world.other, http.MethodPost,
		"/api/v1/chat/sessions/"+world.sessionID+"/three-views/"+world.runID+"/views/front", `{"action":"start"}`, "")
	if startForeign.Code != http.StatusNotFound {
		t.Fatalf("another user started a view: %d %s", startForeign.Code, startForeign.Body.String())
	}
	// Their own session does not make the run theirs either.
	viaOwnSession := grantTestRequest(t, world.handler, world.other, http.MethodPost,
		"/api/v1/chat/sessions/"+otherSession.ID+"/three-views/"+world.runID+"/views/front", `{"action":"start"}`, "")
	if viaOwnSession.Code != http.StatusNotFound {
		t.Fatalf("a foreign run was started through the caller's own session: %d %s", viaOwnSession.Code, viaOwnSession.Body.String())
	}
	// Nor does the owner's other session expose the run under a different session.
	mine := grantTestRequest(t, world.handler, world.cookie, http.MethodPost, "/api/chat/sessions", `{"title":"second"}`, "")
	var second struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(mine.Body.Bytes(), &second); err != nil || second.ID == "" {
		t.Fatalf("second session: %s", mine.Body.String())
	}
	wrongSession := grantTestRequest(t, world.handler, world.cookie, http.MethodPost,
		"/api/v1/chat/sessions/"+second.ID+"/three-views/"+world.runID+"/views/front", `{"action":"start"}`, "")
	if wrongSession.Code != http.StatusNotFound {
		t.Fatalf("a run was started through another session: %d %s", wrongSession.Code, wrongSession.Body.String())
	}
	if groups := world.groups(t, second.ID); len(groups) != 0 {
		t.Fatalf("the second session lists the first one's group: %+v", groups)
	}
	if world.tasks(t) != 0 {
		t.Fatalf("a refused request made %d tasks", world.tasks(t))
	}
	for _, body := range []string{`{"action":"explode"}`, `{"action":"start","attempt":-1}`} {
		bad := grantTestRequest(t, world.handler, world.cookie, http.MethodPost,
			"/api/v1/chat/sessions/"+world.sessionID+"/three-views/"+world.runID+"/views/front", body, "")
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", body, bad.Code, bad.Body.String())
		}
	}
	unknownView := grantTestRequest(t, world.handler, world.cookie, http.MethodPost,
		"/api/v1/chat/sessions/"+world.sessionID+"/three-views/"+world.runID+"/views/top", `{"action":"start"}`, "")
	if unknownView.Code != http.StatusNotFound {
		t.Fatalf("unknown view = %d %s", unknownView.Code, unknownView.Body.String())
	}
}

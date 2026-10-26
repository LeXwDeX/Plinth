package httpapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestLegacyCanvasProjectCreateListAndGetContract(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})

	create := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{"name":"  概念板  ","created":false}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	projectID, _ := project["id"].(string)
	sessionID, _ := project["sessionId"].(string)
	if projectID == "" ||
		sessionID == "" ||
		project["name"] != "概念板" ||
		project["bgStyle"] != "dots" ||
		project["created"] != true {
		t.Fatalf("created project = %#v", project)
	}

	list := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/canvas/projects",
		"",
	)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body)
	}
	var summaries []map[string]any
	decodeCanvasRecorder(t, list, &summaries)
	if len(summaries) != 1 ||
		summaries[0]["id"] != projectID ||
		summaries[0]["sessionId"] != sessionID {
		t.Fatalf("project summaries = %#v", summaries)
	}
	if _, present := summaries[0]["created"]; present {
		t.Fatalf("project list leaked one-time creation signal: %#v", summaries[0])
	}
	count, _ := summaries[0]["_count"].(map[string]any)
	if count["nodes"] != float64(0) {
		t.Fatalf("project summary count = %#v", summaries[0]["_count"])
	}

	get := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/canvas/projects/"+projectID,
		"",
	)
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body)
	}
	var loaded map[string]any
	decodeCanvasRecorder(t, get, &loaded)
	nodes, nodesOK := loaded["nodes"].([]any)
	if loaded["id"] != projectID ||
		loaded["sessionId"] != sessionID ||
		!nodesOK ||
		len(nodes) != 0 {
		t.Fatalf("loaded project = %#v", loaded)
	}
	if _, present := loaded["created"]; present {
		t.Fatalf("project GET leaked one-time creation signal: %#v", loaded)
	}
}

func TestWorkspaceCanvasUsesExistingOwnedSessionWithoutCreatingAnother(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"user"})
	createSession := performCanvasRequest(handler, http.MethodPost,
		"/api/chat/sessions", `{"title":"creative task"}`)
	if createSession.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", createSession.Code, createSession.Body)
	}
	var session map[string]any
	decodeCanvasRecorder(t, createSession, &session)
	sessionID := session["id"].(string)
	createBody := `{"sessionId":"` + sessionID + `","name":"2D board","created":false}`
	first := performCanvasRequest(handler, http.MethodPost,
		"/api/canvas/projects", createBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("create board: %d %s", first.Code, first.Body)
	}
	var board map[string]any
	decodeCanvasRecorder(t, first, &board)
	if board["sessionId"] != sessionID || board["created"] != true {
		t.Fatalf("board session: %#v", board)
	}
	second := performCanvasRequest(handler, http.MethodPost,
		"/api/canvas/projects", `{"sessionId":"`+sessionID+`","name":"2D board","created":true}`)
	var again map[string]any
	decodeCanvasRecorder(t, second, &again)
	if second.Code != http.StatusCreated || again["id"] != board["id"] || again["created"] != false {
		t.Fatalf("idempotent board: %d %#v", second.Code, again)
	}
	list := performCanvasRequest(handler, http.MethodGet,
		"/api/canvas/projects", "")
	var projects []map[string]any
	decodeCanvasRecorder(t, list, &projects)
	if list.Code != http.StatusOK || len(projects) != 1 {
		t.Fatalf("project list: %d %#v", list.Code, projects)
	}
	missing := performCanvasRequest(handler, http.MethodPost,
		"/api/canvas/projects", `{"sessionId":"foreign-session"}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("foreign session: %d %s", missing.Code, missing.Body)
	}
	legacy := performCanvasRequest(handler, http.MethodPost,
		"/api/canvas/projects", `{"name":"unlinked"}`)
	if legacy.Code != http.StatusForbidden {
		t.Fatalf("unlinked create: %d %s", legacy.Code, legacy.Body)
	}
}

func TestLegacyCanvasProjectAuditUsesProductSchemaAndPersists(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Canvas.Audit.CreateAction = "login"
			application.Canvas.Audit.DeleteAction = "logout"
			application.Canvas.Audit.ProjectResourceType = "configured_canvas"
			application.Canvas.Audit.DetailFields.Name = "configured_name"
		},
		nil,
		nil,
		nil,
	)
	create := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{"name":"审计画板"}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	projectID := project["id"].(string)
	assertPersistedCanvasAudit(
		t,
		handler,
		"login",
		"configured_canvas",
		projectID,
		map[string]any{"configured_name": "审计画板"},
	)

	remove := performCanvasRequest(
		handler,
		http.MethodDelete,
		"/api/canvas/projects/"+projectID,
		"",
	)
	if remove.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", remove.Code, remove.Body)
	}
	assertPersistedCanvasAudit(
		t,
		handler,
		"logout",
		"configured_canvas",
		projectID,
		map[string]any{},
	)
}

func assertPersistedCanvasAudit(
	t *testing.T,
	handler http.Handler,
	action string,
	resourceType string,
	resourceID string,
	detail map[string]any,
) {
	t.Helper()
	response := administrationRequest(
		handler,
		http.MethodGet,
		"/api/admin/audit-logs?action="+action,
		"",
		nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body)
	}
	body := decodeAdministrationBody(t, response)
	if body["total"] != float64(1) {
		t.Fatalf("%s audit body=%#v", action, body)
	}
	event := body["data"].([]any)[0].(map[string]any)
	if event["action"] != action ||
		event["userId"] != "local-user" ||
		event["resourceType"] != resourceType ||
		event["resourceId"] != resourceID {
		t.Fatalf("%s audit event=%#v", action, event)
	}
	var gotDetail map[string]any
	if err := json.Unmarshal([]byte(event["detail"].(string)), &gotDetail); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(gotDetail, detail) {
		t.Fatalf("%s audit detail=%#v", action, gotDetail)
	}
}

func TestLegacyCanvasProjectWireAndDefaultsComeFromProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.CanvasProjects =
				"/configured-canvas"
			application.Runtime.HTTP.LegacyRoutes.CanvasProject =
				"/configured-canvas/{configuredProjectKey}"
			application.Runtime.HTTP.LegacyRoutes.CanvasNodes =
				"/configured-canvas/{configuredProjectKey}/configured-nodes"
			application.Runtime.HTTP.LegacyRoutes.CanvasMerge =
				"/configured-canvas-merge"
			application.Canvas.API.PathParameters.ProjectID =
				"configuredProjectKey"
			application.Canvas.API.Fields.Name = "configuredName"
			application.Canvas.API.Fields.SessionID = "configuredSession"
			application.Canvas.API.Fields.BGStyle = "configuredBackground"
			application.Canvas.Defaults.ProjectName = "配置默认项目"
			application.Canvas.Defaults.BGStyle = "configured-grid"
			application.Canvas.Frontend.Backgrounds[0].Value =
				"configured-grid"
		},
		nil,
		nil,
		nil,
	)

	create := performCanvasRequest(
		handler,
		http.MethodPost,
		"/configured-canvas",
		`{}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	projectID, _ := project["id"].(string)
	if project["configuredName"] != "配置默认项目" ||
		project["configuredBackground"] != "configured-grid" ||
		project["configuredSession"] == "" {
		t.Fatalf("configured project = %#v", project)
	}
	if _, leaked := project["name"]; leaked {
		t.Fatalf("hard-coded name field leaked: %#v", project)
	}

	get := performCanvasRequest(
		handler,
		http.MethodGet,
		"/configured-canvas/"+projectID,
		"",
	)
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body)
	}
}

func TestLegacyCanvasProjectPatchUpdatesProjectAndLinkedSession(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	create := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{"name":"初始项目"}`,
	)
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	projectID := project["id"].(string)
	sessionID := project["sessionId"].(string)

	update := performCanvasRequest(
		handler,
		http.MethodPatch,
		"/api/canvas/projects/"+projectID,
		`{
			"name":"  更新项目  ",
			"viewport":"{\"zoom\":2,\"panX\":8,\"panY\":13}",
			"bgStyle":"grid"
		}`,
	)
	if update.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", update.Code, update.Body)
	}
	var updated map[string]any
	decodeCanvasRecorder(t, update, &updated)
	if updated["name"] != "更新项目" ||
		updated["viewport"] != `{"zoom":2,"panX":8,"panY":13}` ||
		updated["bgStyle"] != "grid" {
		t.Fatalf("updated project = %#v", updated)
	}

	session := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions",
		"",
	)
	if session.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", session.Code, session.Body)
	}
	var sessions []map[string]any
	decodeCanvasRecorder(t, session, &sessions)
	var linkedSession map[string]any
	for _, candidate := range sessions {
		if candidate["id"] == sessionID {
			linkedSession = candidate
			break
		}
	}
	if linkedSession["title"] != "更新项目" {
		t.Fatalf("linked session = %#v, all = %#v", linkedSession, sessions)
	}
}

func TestLegacyCanvasProjectDeleteRemovesProjectAndLinkedSession(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	create := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{"name":"待删除项目"}`,
	)
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	projectID := project["id"].(string)
	sessionID := project["sessionId"].(string)

	deleted := performCanvasRequest(
		handler,
		http.MethodDelete,
		"/api/canvas/projects/"+projectID,
		"",
	)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body)
	}
	var result map[string]any
	decodeCanvasRecorder(t, deleted, &result)
	if result["ok"] != true {
		t.Fatalf("delete result = %#v", result)
	}

	get := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/canvas/projects/"+projectID,
		"",
	)
	if get.Code != http.StatusNotFound {
		t.Fatalf("deleted project status=%d body=%s", get.Code, get.Body)
	}
	listSessions := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/chat/sessions",
		"",
	)
	var sessions []map[string]any
	decodeCanvasRecorder(t, listSessions, &sessions)
	for _, session := range sessions {
		if session["id"] == sessionID {
			t.Fatalf("linked session still exists: %#v", session)
		}
	}
}

func TestLegacyCanvasNodeReplaceUsesSchemaDefaultsAndZOrder(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	create := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{"name":"节点项目"}`,
	)
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	projectID := project["id"].(string)

	replace := performCanvasRequest(
		handler,
		http.MethodPut,
		"/api/canvas/projects/"+projectID+"/nodes",
		`{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"nodes":[
			{
				"id":"node-high",
				"type":"image",
				"x":1.5,
				"y":2.5,
				"width":640,
				"height":480,
				"zIndex":5,
				"data":"{\"imageUrl\":\"/api/assets/image-1\"}",
				"visible":false,
				"locked":true
			},
			{"id":"node-default","type":"text"}
		]}`,
	)
	if replace.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", replace.Code, replace.Body)
	}
	var result map[string]any
	decodeCanvasRecorder(t, replace, &result)
	if result["ok"] != true || result["count"] != float64(2) {
		t.Fatalf("replace result = %#v", result)
	}

	get := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/canvas/projects/"+projectID,
		"",
	)
	var loaded map[string]any
	decodeCanvasRecorder(t, get, &loaded)
	nodes := loaded["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %#v", nodes)
	}
	defaultNode := nodes[0].(map[string]any)
	highNode := nodes[1].(map[string]any)
	if defaultNode["id"] != "node-default" ||
		defaultNode["x"] != float64(0) ||
		defaultNode["y"] != float64(0) ||
		defaultNode["width"] != float64(256) ||
		defaultNode["height"] != float64(256) ||
		defaultNode["zIndex"] != float64(0) ||
		defaultNode["data"] != "{}" ||
		defaultNode["visible"] != true ||
		defaultNode["locked"] != false {
		t.Fatalf("default node = %#v", defaultNode)
	}
	if highNode["id"] != "node-high" ||
		highNode["zIndex"] != float64(5) ||
		highNode["visible"] != false ||
		highNode["locked"] != true {
		t.Fatalf("high node = %#v", highNode)
	}

	secondReplace := performCanvasRequest(
		handler,
		http.MethodPut,
		"/api/canvas/projects/"+projectID+"/nodes",
		`{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"nodes":[{"id":"node-only","type":"image"}]}`,
	)
	if secondReplace.Code != http.StatusOK {
		t.Fatalf(
			"second replace status=%d body=%s",
			secondReplace.Code,
			secondReplace.Body,
		)
	}
	reloaded := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/canvas/projects/"+projectID,
		"",
	)
	var replacedProject map[string]any
	decodeCanvasRecorder(t, reloaded, &replacedProject)
	replacedNodes := replacedProject["nodes"].([]any)
	if len(replacedNodes) != 1 ||
		replacedNodes[0].(map[string]any)["id"] != "node-only" {
		t.Fatalf("replacement retained stale nodes: %#v", replacedNodes)
	}
}

func TestLegacyCanvasBitmapLimitUsesSchemaErrorAndPreservesRevision(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(t, []string{"admin"}, func(application *catalog.ApplicationSchema) {
		application.Canvas.Limits.MaximumBitmapNodes = 2
		application.Canvas.Presentation.Messages.BitmapLimitExceeded = "configured bitmap limit"
	})
	create := performCanvasRequest(handler, http.MethodPost, "/api/canvas/projects", `{}`)
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	projectID := project["id"].(string)
	path := "/api/canvas/projects/" + projectID
	accepted := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":1,"nodes":[{"id":"image-a","type":"image"},{"id":"image-b","type":"image"},{"id":"shape-a","type":"shape"}]}`)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accepted status=%d body=%s", accepted.Code, accepted.Body)
	}
	rejected := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":2,"nodes":[{"id":"image-a","type":"image"},{"id":"image-b","type":"image"},{"id":"image-c","type":"image"}]}`)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("rejected status=%d body=%s", rejected.Code, rejected.Body)
	}
	var failure map[string]any
	decodeCanvasRecorder(t, rejected, &failure)
	if failure["error"] != "configured bitmap limit" {
		t.Fatalf("error payload = %#v", failure)
	}
	get := performCanvasRequest(handler, http.MethodGet, path, "")
	var stored map[string]any
	decodeCanvasRecorder(t, get, &stored)
	if stored["revision"] != float64(2) || len(stored["nodes"].([]any)) != 3 {
		t.Fatalf("stored after rejected save = %#v", stored)
	}
}

func TestLegacyCanvasNodeRejectsNonArrayUsingProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Canvas.API.Fields.Error = "configuredError"
			application.Canvas.Presentation.Messages.NodesMustArray =
				"configured nodes validation"
		},
		nil,
		nil,
		nil,
	)
	create := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{}`,
	)
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)

	response := performCanvasRequest(
		handler,
		http.MethodPut,
		"/api/canvas/projects/"+project["id"].(string)+"/nodes",
		`{}`,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var payload map[string]any
	decodeCanvasRecorder(t, response, &payload)
	if payload["configuredError"] != "configured nodes validation" {
		t.Fatalf("configured validation = %#v", payload)
	}
}

func TestLegacyCanvasTrailingJSONFallsBackToSchemaDefaults(t *testing.T) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Canvas.API.Fields.Name = "configuredName"
			application.Canvas.Defaults.ProjectName = "configured default"
		},
		nil,
		nil,
		nil,
	)
	response := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{"configuredName":"first"} {"configuredName":"trailing"}`,
	)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var project map[string]any
	decodeCanvasRecorder(t, response, &project)
	if project["configuredName"] != "configured default" {
		t.Fatalf("trailing JSON was accepted: %#v", project)
	}
}

func TestLegacyCanvasMergeComposesRealPixelsIntoOwnedAsset(t *testing.T) {
	t.Parallel()
	_, handler, assets := newHTTPHarnessWithRoles(t, []string{"admin"})
	redID := uploadCanvasPNG(
		t,
		assets,
		color.NRGBA{R: 255, A: 255},
	)
	blueID := uploadCanvasPNG(
		t,
		assets,
		color.NRGBA{B: 255, A: 255},
	)

	merge := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/merge-images",
		`{"nodes":[
			{"imageUrl":"/api/assets/`+redID+`","x":0,"y":0,"width":2,"height":2},
			{"imageUrl":"/api/assets/`+blueID+`","x":3,"y":0,"width":2,"height":2}
		]}`,
	)
	if merge.Code != http.StatusOK {
		t.Fatalf("merge status=%d body=%s", merge.Code, merge.Body)
	}
	var result map[string]any
	decodeCanvasRecorder(t, merge, &result)
	assetID, _ := result["assetId"].(string)
	if assetID == "" || result["url"] != "/api/assets/"+assetID {
		t.Fatalf("merge result = %#v", result)
	}
	assertPersistedCanvasAudit(
		t,
		handler,
		"upload_file",
		"canvas_merge",
		assetID,
		map[string]any{},
	)

	download := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/assets/"+assetID,
		"",
	)
	if download.Code != http.StatusOK {
		t.Fatalf("download status=%d body=%s", download.Code, download.Body)
	}
	merged, err := png.Decode(bytes.NewReader(download.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode merged PNG: %v", err)
	}
	if merged.Bounds().Dx() != 5 || merged.Bounds().Dy() != 2 {
		t.Fatalf("merged bounds = %v", merged.Bounds())
	}
	assertCanvasPixel(t, merged, 0, 0, color.NRGBA{R: 255, A: 255})
	assertCanvasPixel(t, merged, 2, 0, color.NRGBA{})
	assertCanvasPixel(t, merged, 4, 1, color.NRGBA{B: 255, A: 255})
}

func TestLegacyCanvasMergeDecodesWebPInput(t *testing.T) {
	t.Parallel()
	_, handler, assets := newHTTPHarnessWithRoles(t, []string{"admin"})
	content, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"..",
		"public",
		"gpt-skills",
		"_placeholder.webp",
	))
	if err != nil {
		t.Fatal(err)
	}
	source, err := assets.UploadAsset(
		t.Context(),
		generation.OwnerID("local-user"),
		generation.InputAssetUpload{
			Body:             bytes.NewReader(content),
			MediaType:        "image/webp",
			OriginalFilename: "canvas-source.webp",
			AssetType:        "image",
			MaxBytes:         10 * 1024 * 1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	merge := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/merge-images",
		`{"nodes":[{"imageUrl":"/api/assets/`+
			string(source.ID)+
			`","x":0,"y":0,"width":2,"height":2}]}`,
	)
	if merge.Code != http.StatusOK {
		t.Fatalf("merge status=%d body=%s", merge.Code, merge.Body)
	}
	var result map[string]any
	decodeCanvasRecorder(t, merge, &result)
	assetID, _ := result["assetId"].(string)
	if assetID == "" {
		t.Fatalf("merge result = %#v", result)
	}

	download := performCanvasRequest(
		handler,
		http.MethodGet,
		"/api/assets/"+assetID,
		"",
	)
	if download.Code != http.StatusOK {
		t.Fatalf("download status=%d body=%s", download.Code, download.Body)
	}
	merged, err := png.Decode(bytes.NewReader(download.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode merged PNG: %v", err)
	}
	if merged.Bounds().Dx() != 2 || merged.Bounds().Dy() != 2 {
		t.Fatalf("merged bounds = %v", merged.Bounds())
	}
}

func TestLegacyCanvasMergeWireLimitsAndTextComeFromProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, assets, _ := newHTTPHarnessWithServices(
		t,
		[]string{"admin"},
		func(application *catalog.ApplicationSchema) {
			application.Runtime.HTTP.LegacyRoutes.CanvasMerge =
				"/configured-merge"
			application.Runtime.HTTP.LegacyRoutes.AssetItem =
				"/configured-assets/{assetID}"
			fields := &application.Canvas.API.Fields
			fields.Nodes = "configuredNodes"
			fields.ImageURL = "configuredImage"
			fields.X = "configuredX"
			fields.Y = "configuredY"
			fields.Width = "configuredWidth"
			fields.Height = "configuredHeight"
			fields.AssetID = "configuredAsset"
			fields.URL = "configuredURL"
			fields.Error = "configuredError"
			application.Canvas.Limits.MaximumDimension = 8
			// The canvas frame range (#246) shares the maximum; keep the minimum below it.
			application.Canvas.Limits.MinimumDimension = 1
			application.Canvas.Frontend.Artboard.DefaultWidth = 8
			application.Canvas.Frontend.Artboard.DefaultHeight = 8
			application.Canvas.Frontend.Artboard.RatioPixels = map[string]catalog.CanvasPixelSize{"1:1": {Width: 8, Height: 8}}
			application.Canvas.Frontend.Artboard.RatioPixelTiers = nil
			application.Canvas.Presentation.Messages.InvalidMerge =
				"configured invalid merge"
			application.Canvas.Presentation.Messages.DimensionLimit =
				"configured dimension limit"
		},
		nil,
		nil,
		nil,
	)
	sourceID := uploadCanvasPNG(
		t,
		assets,
		color.NRGBA{G: 255, A: 255},
	)

	invalid := performCanvasRequest(
		handler,
		http.MethodPost,
		"/configured-merge",
		`{}`,
	)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body)
	}
	var invalidPayload map[string]any
	decodeCanvasRecorder(t, invalid, &invalidPayload)
	if invalidPayload["configuredError"] != "configured invalid merge" {
		t.Fatalf("configured invalid result = %#v", invalidPayload)
	}

	tooLarge := performCanvasRequest(
		handler,
		http.MethodPost,
		"/configured-merge",
		`{"configuredNodes":[{
			"configuredImage":"/configured-assets/`+sourceID+`",
			"configuredX":0,
			"configuredY":0,
			"configuredWidth":9,
			"configuredHeight":1
		}]}`,
	)
	if tooLarge.Code != http.StatusBadRequest {
		t.Fatalf("dimension status=%d body=%s", tooLarge.Code, tooLarge.Body)
	}
	var dimensionPayload map[string]any
	decodeCanvasRecorder(t, tooLarge, &dimensionPayload)
	if dimensionPayload["configuredError"] != "configured dimension limit" {
		t.Fatalf("configured dimension result = %#v", dimensionPayload)
	}

	merged := performCanvasRequest(
		handler,
		http.MethodPost,
		"/configured-merge",
		`{"configuredNodes":[{
			"configuredImage":"/configured-assets/`+sourceID+`",
			"configuredX":0,
			"configuredY":0,
			"configuredWidth":2,
			"configuredHeight":1
		}]}`,
	)
	if merged.Code != http.StatusOK {
		t.Fatalf("merge status=%d body=%s", merged.Code, merged.Body)
	}
	var result map[string]any
	decodeCanvasRecorder(t, merged, &result)
	assetID, _ := result["configuredAsset"].(string)
	if assetID == "" ||
		result["configuredURL"] != "/configured-assets/"+assetID {
		t.Fatalf("configured merge result = %#v", result)
	}
	if _, leaked := result["assetId"]; leaked {
		t.Fatalf("hard-coded asset field leaked: %#v", result)
	}
	download := performCanvasRequest(
		handler,
		http.MethodGet,
		"/configured-assets/"+assetID,
		"",
	)
	if download.Code != http.StatusOK {
		t.Fatalf("configured download=%d body=%s", download.Code, download.Body)
	}
}

func TestLegacyCanvasAdminFailureWireAndTextComeFromProductSchema(
	t *testing.T,
) {
	t.Parallel()
	_, handler, _, _ := newHTTPHarnessWithServices(
		t,
		[]string{"user"},
		func(application *catalog.ApplicationSchema) {
			application.Canvas.API.Fields.Error = "configuredError"
			application.Canvas.Presentation.Messages.Forbidden =
				"configured canvas denial"
		},
		nil,
		nil,
		nil,
	)
	response := performCanvasRequest(
		handler,
		http.MethodPost,
		"/api/canvas/projects",
		`{"name":"legacy canvas"}`,
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var payload map[string]any
	decodeCanvasRecorder(t, response, &payload)
	if payload["configuredError"] != "configured canvas denial" {
		t.Fatalf("configured error = %#v", payload)
	}
	if _, leaked := payload["error"]; leaked {
		t.Fatalf("hard-coded error field leaked: %#v", payload)
	}
}

func performCanvasRequest(
	handler http.Handler,
	method string,
	target string,
	body string,
) *httptest.ResponseRecorder {
	var requestBody *bytes.Reader
	if body == "" {
		requestBody = bytes.NewReader(nil)
	} else {
		requestBody = bytes.NewReader([]byte(body))
	}
	request := httptest.NewRequest(method, target, requestBody)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeCanvasRecorder(
	t *testing.T,
	response *httptest.ResponseRecorder,
	target any,
) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}

func uploadCanvasPNG(
	t *testing.T,
	assets *inputassets.Service,
	pixel color.NRGBA,
) string {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, pixel)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	asset, err := assets.UploadAsset(
		t.Context(),
		generation.OwnerID("local-user"),
		generation.InputAssetUpload{
			Body:             bytes.NewReader(encoded.Bytes()),
			MediaType:        "image/png",
			OriginalFilename: "canvas-source.png",
			AssetType:        "image",
			MaxBytes:         1024 * 1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return string(asset.ID)
}

func assertCanvasPixel(
	t *testing.T,
	source image.Image,
	x int,
	y int,
	want color.NRGBA,
) {
	t.Helper()
	got := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
	if got != want {
		t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got, want)
	}
}

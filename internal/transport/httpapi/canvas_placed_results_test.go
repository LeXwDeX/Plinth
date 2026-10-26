package httpapi_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/canvas"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func createPlacedResultsCanvas(t *testing.T, handler http.Handler) string {
	t.Helper()
	create := performCanvasRequest(handler, http.MethodPost, "/api/canvas/projects", `{"name":"placed"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body)
	}
	var project map[string]any
	decodeCanvasRecorder(t, create, &project)
	if _, present := project["placedResults"]; present {
		t.Fatalf("metadata payload without nodes exposed placed results: %#v", project)
	}
	return project["id"].(string)
}

func loadPlacedResultsCanvas(t *testing.T, handler http.Handler, projectID string) map[string]any {
	t.Helper()
	get := performCanvasRequest(handler, http.MethodGet, "/api/canvas/projects/"+projectID, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body)
	}
	var loaded map[string]any
	decodeCanvasRecorder(t, get, &loaded)
	return loaded
}

func TestCanvasPlacedResultsPersistWithNodesAndRevision(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	projectID := createPlacedResultsCanvas(t, handler)
	path := "/api/canvas/projects/" + projectID

	// Never initialized: the field is present and JSON null.
	loaded := loadPlacedResultsCanvas(t, handler, projectID)
	if value, present := loaded["placedResults"]; !present || value != nil {
		t.Fatalf("unseeded placedResults = %#v present=%v", value, present)
	}

	// A save omitting the field keeps it uninitialized.
	legacy := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":1,"nodes":[{"id":"n1","type":"image"}]}`)
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy save status=%d body=%s", legacy.Code, legacy.Body)
	}
	if value := loadPlacedResultsCanvas(t, handler, projectID)["placedResults"]; value != nil {
		t.Fatalf("omitted field initialized placed results: %#v", value)
	}

	// Initialized empty is distinct from null.
	seeded := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":2,"nodes":[],"placedResults":[]}`)
	if seeded.Code != http.StatusOK {
		t.Fatalf("seed status=%d body=%s", seeded.Code, seeded.Body)
	}
	loaded = loadPlacedResultsCanvas(t, handler, projectID)
	if value, ok := loaded["placedResults"].([]any); !ok || len(value) != 0 || loaded["revision"] != float64(3) {
		t.Fatalf("empty placed results = %#v", loaded)
	}

	// Values are de-duplicated preserving first-seen order and stored untrimmed.
	saved := performCanvasRequest(handler, http.MethodPut, path+"/nodes",
		`{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":3,"nodes":[{"id":"n2","type":"image"}],"placedResults":["task-2#asset-b"," task-1#/api/assets/a","task-2#asset-b"]}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body)
	}
	loaded = loadPlacedResultsCanvas(t, handler, projectID)
	want := []any{"task-2#asset-b", " task-1#/api/assets/a"}
	if !reflect.DeepEqual(loaded["placedResults"], want) || loaded["revision"] != float64(4) || len(loaded["nodes"].([]any)) != 1 {
		t.Fatalf("saved placed results = %#v", loaded)
	}

	// A stale revision is still rejected and changes neither nodes nor the record.
	stale := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":3,"nodes":[],"placedResults":["stale#x"]}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body)
	}
	// Omitting the field on a fresh save leaves the stored record untouched.
	untouched := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":4,"nodes":[]}`)
	if untouched.Code != http.StatusOK {
		t.Fatalf("untouched status=%d body=%s", untouched.Code, untouched.Body)
	}
	// Explicit null is treated like an omitted field (never resets to unseeded).
	nullSave := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":5,"nodes":[],"placedResults":null}`)
	if nullSave.Code != http.StatusOK {
		t.Fatalf("null status=%d body=%s", nullSave.Code, nullSave.Body)
	}
	loaded = loadPlacedResultsCanvas(t, handler, projectID)
	if !reflect.DeepEqual(loaded["placedResults"], want) || loaded["revision"] != float64(6) || len(loaded["nodes"].([]any)) != 0 {
		t.Fatalf("after stale/omitted saves = %#v", loaded)
	}
}

func TestCanvasPlacedResultsValidationUsesProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(t, []string{"admin"}, func(application *catalog.ApplicationSchema) {
		application.Canvas.API.Fields.PlacedResults = "configuredPlaced"
		application.Canvas.Limits.MaximumPlacedResults = 2
		application.Canvas.Limits.MaximumPlacedResultKeyCharacters = 4
		application.Canvas.Presentation.Messages.PlacedResultsInvalid = "configured placed validation"
	})
	projectID := createPlacedResultsCanvas(t, handler)
	path := "/api/canvas/projects/" + projectID
	for name, value := range map[string]string{
		"object":        `{}`,
		"string":        `"a"`,
		"number":        `1`,
		"non-string":    `["a",1]`,
		"nested":        `[["a"]]`,
		"empty":         `["a",""]`,
		"control":       `["a\u0007"]`,
		"newline":       `["a\nb"]`,
		"delete":        `["a\u007f"]`,
		"too long":      `["abcde"]`,
		"too many":      `["a","b","c"]`,
		"too long utf8": `["汉字汉字汉"]`,
	} {
		response := performCanvasRequest(handler, http.MethodPut, path+"/nodes",
			`{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":1,"nodes":[{"id":"n1","type":"image"}],"configuredPlaced":`+value+`}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body)
		}
		var failure map[string]any
		decodeCanvasRecorder(t, response, &failure)
		if failure["error"] != "configured placed validation" {
			t.Fatalf("%s: error payload = %#v", name, failure)
		}
	}
	loaded := loadPlacedResultsCanvas(t, handler, projectID)
	if loaded["revision"] != float64(1) || len(loaded["nodes"].([]any)) != 0 || loaded["configuredPlaced"] != nil {
		t.Fatalf("invalid saves mutated the project: %#v", loaded)
	}
	// Boundary values are accepted: limit counts after de-duplication, length counts characters.
	accepted := performCanvasRequest(handler, http.MethodPut, path+"/nodes",
		`{"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2,"revision":1,"nodes":[],"configuredPlaced":["汉字汉字","abcd","abcd"]}`)
	if accepted.Code != http.StatusOK {
		t.Fatalf("boundary status=%d body=%s", accepted.Code, accepted.Body)
	}
	loaded = loadPlacedResultsCanvas(t, handler, projectID)
	if !reflect.DeepEqual(loaded["configuredPlaced"], []any{"汉字汉字", "abcd"}) {
		t.Fatalf("boundary placed results = %#v", loaded)
	}
	if _, present := loaded["placedResults"]; present {
		t.Fatalf("wire name not taken from Product Schema: %#v", loaded)
	}
}

func TestCanvasPlacedResultsAreOwnerScoped(t *testing.T) {
	t.Parallel()
	const foreignProject = "foreign-placed-project"
	foreign := []string{"secret-task#secret-asset"}
	var databasePath string
	_, handler, _, _ := newHTTPHarnessWithServices(t, []string{"admin"}, nil, nil, nil, nil,
		func(t *testing.T, path string) {
			databasePath = path
			repository, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			ctx := context.Background()
			now := time.Now().UTC()
			if _, err := repository.CreateSession(ctx, chat.Session{ID: "foreign-placed-session", OwnerID: "owner-foreign", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.CreateProject(ctx, canvas.Project{
				ID: foreignProject, OwnerID: "owner-foreign", Name: "foreign", SessionID: "foreign-placed-session",
				Viewport: "{}", BGStyle: "dots", CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.ReplaceNodes(ctx, "owner-foreign", foreignProject, 1, nil, &foreign, nil, 50); err != nil {
				t.Fatal(err)
			}
		})
	path := "/api/canvas/projects/" + foreignProject
	get := performCanvasRequest(handler, http.MethodGet, path, "")
	put := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"nodes":[],"placedResults":["mine#x"]}`)
	if get.Code != http.StatusNotFound || put.Code != http.StatusNotFound {
		t.Fatalf("foreign get=%d put=%d body=%s", get.Code, put.Code, put.Body)
	}
	if strings.Contains(get.Body.String(), "secret") {
		t.Fatalf("foreign placed results leaked: %s", get.Body)
	}
	repository, err := sqlite.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	stored, found, err := repository.GetProject(context.Background(), "owner-foreign", foreignProject)
	if err != nil || !found || !reflect.DeepEqual(stored.PlacedResults, foreign) || stored.Revision != 2 {
		t.Fatalf("foreign record changed: %#v found=%v error=%v", stored, found, err)
	}
}

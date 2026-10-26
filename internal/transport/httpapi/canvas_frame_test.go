package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #246: the canvas pixel size and coordinate version persist with the nodes, in the same revision.
func TestCanvasFramePersistsWithNodesAndRevision(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithRoles(t, []string{"admin"})
	projectID := createPlacedResultsCanvas(t, handler)
	path := "/api/canvas/projects/" + projectID

	// A new project is a pixel canvas whose size is not chosen yet: version 2, no width or height.
	loaded := loadPlacedResultsCanvas(t, handler, projectID)
	if loaded["coordinateVersion"] != float64(2) {
		t.Fatalf("new project coordinateVersion = %#v", loaded["coordinateVersion"])
	}
	for _, field := range []string{"canvasWidth", "canvasHeight"} {
		if value, present := loaded[field]; !present || value != nil {
			t.Fatalf("new project %s = %#v present=%v", field, value, present)
		}
	}

	// A pixel canvas refuses a save without the frame even before its size is chosen: such a save comes from
	// a page still writing 512-unit nodes. Nothing is written.
	legacy := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"revision":1,"nodes":[{"id":"n1","type":"image"}]}`)
	if legacy.Code != http.StatusConflict {
		t.Fatalf("frame-less save on an unsized pixel canvas status=%d body=%s", legacy.Code, legacy.Body)
	}
	if loaded := loadPlacedResultsCanvas(t, handler, projectID); loaded["canvasWidth"] != nil || loaded["revision"] != float64(1) {
		t.Fatalf("rejected frame-less save changed the project: %#v", loaded)
	}

	saved := performCanvasRequest(handler, http.MethodPut, path+"/nodes",
		`{"revision":1,"nodes":[{"id":"n1","type":"image","x":-424,"y":-624,"width":2048,"height":2048}],"canvasWidth":2048,"canvasHeight":2048,"coordinateVersion":2}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body)
	}
	loaded = loadPlacedResultsCanvas(t, handler, projectID)
	if loaded["canvasWidth"] != float64(2048) || loaded["canvasHeight"] != float64(2048) ||
		loaded["coordinateVersion"] != float64(2) || loaded["revision"] != float64(2) {
		t.Fatalf("saved frame = %#v", loaded)
	}

	// A stale revision changes neither the nodes nor the frame.
	stale := performCanvasRequest(handler, http.MethodPut, path+"/nodes",
		`{"revision":1,"nodes":[],"canvasWidth":1024,"canvasHeight":1024,"coordinateVersion":2}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body)
	}
	// Once sized, a save without the frame (a page still writing 512-unit nodes) is a conflict and writes nothing.
	for _, body := range []string{
		`{"revision":2,"nodes":[]}`,
		`{"revision":2,"nodes":[],"canvasWidth":null,"canvasHeight":null,"coordinateVersion":null}`,
	} {
		if response := performCanvasRequest(handler, http.MethodPut, path+"/nodes", body); response.Code != http.StatusConflict {
			t.Fatalf("frame-less save on a sized canvas status=%d body=%s", response.Code, response.Body)
		}
	}
	loaded = loadPlacedResultsCanvas(t, handler, projectID)
	if loaded["canvasWidth"] != float64(2048) || loaded["revision"] != float64(2) || len(loaded["nodes"].([]any)) != 1 {
		t.Fatalf("frame after rejected frame-less saves = %#v", loaded)
	}

	// A resize is one more save: the new size replaces the old one.
	resized := performCanvasRequest(handler, http.MethodPut, path+"/nodes",
		`{"revision":2,"nodes":[],"canvasWidth":3840,"canvasHeight":2160,"coordinateVersion":2}`)
	if resized.Code != http.StatusOK {
		t.Fatalf("resize status=%d body=%s", resized.Code, resized.Body)
	}
	loaded = loadPlacedResultsCanvas(t, handler, projectID)
	if loaded["canvasWidth"] != float64(3840) || loaded["canvasHeight"] != float64(2160) {
		t.Fatalf("resized frame = %#v", loaded)
	}
}

func TestCanvasFrameValidationUsesProductSchema(t *testing.T) {
	t.Parallel()
	_, handler, _ := newHTTPHarnessWithApplication(t, []string{"admin"}, func(application *catalog.ApplicationSchema) {
		application.Canvas.API.Fields.CanvasWidth = "configuredWidth"
		application.Canvas.Limits.MinimumDimension = 100
		application.Canvas.Limits.MaximumDimension = 4096
		// The 4K tier sizes exceed this maximum; the tiers are not under test here.
		application.Canvas.Frontend.Artboard.RatioPixelTiers = nil
		application.Canvas.Presentation.Messages.CanvasFrameInvalid = "configured frame validation"
	})
	projectID := createPlacedResultsCanvas(t, handler)
	path := "/api/canvas/projects/" + projectID
	for name, frame := range map[string]string{
		"partial":             `"configuredWidth":1024`,
		"missing version":     `"configuredWidth":1024,"canvasHeight":1024`,
		"fractional":          `"configuredWidth":1024.5,"canvasHeight":1024,"coordinateVersion":2`,
		"string":              `"configuredWidth":"1024","canvasHeight":1024,"coordinateVersion":2`,
		"below minimum":       `"configuredWidth":99,"canvasHeight":1024,"coordinateVersion":2`,
		"above maximum":       `"configuredWidth":1024,"canvasHeight":4097,"coordinateVersion":2`,
		"legacy version":      `"configuredWidth":1024,"canvasHeight":1024,"coordinateVersion":1`,
		"future version":      `"configuredWidth":1024,"canvasHeight":1024,"coordinateVersion":3`,
		"unconfigured name":   `"canvasWidth":1024,"canvasHeight":1024,"coordinateVersion":2`,
		"negative dimensions": `"configuredWidth":-1024,"canvasHeight":-1024,"coordinateVersion":2`,
	} {
		response := performCanvasRequest(handler, http.MethodPut, path+"/nodes", `{"revision":1,"nodes":[],`+frame+`}`)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "configured frame validation") {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body)
		}
	}
	// Nothing was written by the rejected requests.
	loaded := loadPlacedResultsCanvas(t, handler, projectID)
	if loaded["configuredWidth"] != nil || loaded["revision"] != float64(1) {
		t.Fatalf("rejected frames changed the project: %#v", loaded)
	}
	accepted := performCanvasRequest(handler, http.MethodPut, path+"/nodes",
		`{"revision":1,"nodes":[],"configuredWidth":100,"canvasHeight":4096,"coordinateVersion":2}`)
	if accepted.Code != http.StatusOK {
		t.Fatalf("boundary frame status=%d body=%s", accepted.Code, accepted.Body)
	}
	if loaded := loadPlacedResultsCanvas(t, handler, projectID); loaded["configuredWidth"] != float64(100) || loaded["canvasHeight"] != float64(4096) {
		t.Fatalf("boundary frame = %#v", loaded)
	}
}

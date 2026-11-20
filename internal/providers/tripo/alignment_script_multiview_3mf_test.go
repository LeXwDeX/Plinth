package tripo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
	tripov3 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/tripo/v3"
)

// The third partial run of the Tripo v3 alignment script (#386) is one approved
// real run: the multiview model Plinth requests with its production defaults, and
// its 3MF export. The script writes those two requests and the uploads before them
// by hand, so this test pins them to what the adapter sends: a change of the
// Offering's defaults, of how the adapter builds the body, or of the export request
// fails here and the script has to be brought along before the next real run.
func TestAlignmentScriptSendsWhatTheAdapterSends(t *testing.T) {
	steps := map[string]align.Step{}
	for _, step := range tripov3.Profile().Steps {
		steps[step.Name] = step
	}

	// The adapter's three uploads and its generation request, at the defaults.
	var mu sync.Mutex
	var uploads []string
	var generationBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			uploads = append(uploads, part.FormName()+" "+part.FileName()+" "+part.Header.Get("Content-Type"))
			token := fmt.Sprintf("tok-%d", len(uploads))
			mu.Unlock()
			writeEnvelope(w, map[string]string{"file_token": token})
		case "/generation/multiview-to-model":
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			_ = json.Unmarshal(raw, &generationBody)
			mu.Unlock()
			writeEnvelope(w, map[string]string{"task_id": "tripo-mv-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := c.Get(tripoMultiviewOffering)
	if !ok {
		t.Fatal("multiview offering missing")
	}
	adapter, err := tripo.New(tripo.Config{
		BaseURL: upstream.URL, APIKey: "k", HTTPClient: upstream.Client(),
		InputAssets: memoryInputAssets{content: []byte("\x89PNG\r\n\x1a\nmv")},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Three references, no views given: the Offering's default front, left, back.
	ids := []string{"asset-0", "asset-1", "asset-2"}
	assets := make([]generation.InputAsset, 0, len(ids))
	for _, id := range ids {
		assets = append(assets, generation.InputAsset{ID: generation.AssetID(id), MediaType: "image/png"})
	}
	seed, _ := json.Marshal(map[string]any{"reference_assets": ids, "authorize_extra_credits": true})
	_, input, err := c.Normalize(generation.ImageTo3D, tripoMultiviewOffering, seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Start(context.Background(), generation.StartRequest{
		AttemptID: "a", SubmissionKey: "s", Offering: offering, Input: input,
		InputAssets: map[string][]generation.InputAsset{"reference_assets": assets},
	}); err != nil {
		t.Fatal(err)
	}

	// The uploads: the script's three steps send the same part the adapter does.
	wantPart := "file input.png image/png"
	for i, name := range []string{"mv-upload-front", "mv-upload-left", "mv-upload-back"} {
		file := steps[name].Multipart
		if file == nil || file.Field+" "+file.Filename+" "+file.MediaType != wantPart || i >= len(uploads) || uploads[i] != wantPart {
			t.Errorf("%s: script sends %+v, adapter sent %v", name, file, uploads)
		}
	}
	// The generation: the script's body with its three tokens is the adapter's.
	script := strings.NewReplacer("{mv_front}", "tok-1", "{mv_left}", "tok-2", "{mv_back}", "tok-3").
		Replace(marshalStepBody(t, steps["multiview-to-model"]))
	var want map[string]any
	if err := json.Unmarshal([]byte(script), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, generationBody) {
		t.Errorf("generation request:\n script  %v\n adapter %v", want, generationBody)
	}
	if !steps["multiview-to-model"].Paid || steps["multiview-to-model"].Path != "/generation/multiview-to-model" {
		t.Errorf("generation step: %+v", steps["multiview-to-model"])
	}

	// The export: the adapter's 3MF request of an Offering at its defaults is the
	// generation's task id and the format and nothing else (every export option is on
	// its upstream default and left out).
	exported, _, _ := runExports(t, nil)
	var scriptExport map[string]any
	if err := json.Unmarshal([]byte(marshalStepBody(t, steps["convert-3mf"])), &scriptExport); err != nil {
		t.Fatal(err)
	}
	if scriptExport["input"] != "{m2m}" || steps["convert-3mf"].Capture["m2m_convert"] == "" || steps["multiview-to-model"].Capture["m2m"] != "data.task_id" {
		t.Errorf("the export must name the generation's task id: %+v", steps["convert-3mf"])
	}
	delete(scriptExport, "input")
	if !reflect.DeepEqual(scriptExport, exported["3MF"]) {
		t.Errorf("export request without its source:\n script  %v\n adapter %v", scriptExport, exported["3MF"])
	}
	if steps["convert-3mf"].Path != "/models/convert" || !steps["convert-3mf"].Paid {
		t.Errorf("export step: %+v", steps["convert-3mf"])
	}
}

func marshalStepBody(t *testing.T, step align.Step) string {
	t.Helper()
	raw, err := json.Marshal(step.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

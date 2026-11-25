package generationtool_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite/sqlitetest"
	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/hyper3d"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	hyper3dapiv2 "github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/hyper3d/api-v2"
)

// #376 end to end on the fake stack: an Agent's create_model_3d_from_image call
// with view_labels goes through the real executor, the kernel's preparation and
// grant, the real Hyper3D Adapter and the Hyper3D fake. What the fake receives
// is the wire the real service was observed to take: one image_label part per
// mark, in picture order and ahead of the pictures, and each mark sits on its
// own picture however the Agent ordered its handles. Nothing leaves the
// process: the fake is an in-process loopback server.

// assetNameResolver resolves a stored input picture URL to its asset ID, so the
// executor's final picture order reaches the kernel as the real resolver sends it.
type assetNameResolver struct{}

func (assetNameResolver) Resolve(_ context.Context, _ references.ReferenceScope, urls []string) ([]generation.AssetID, error) {
	ids := make([]generation.AssetID, 0, len(urls))
	for _, url := range urls {
		ids = append(ids, generation.AssetID(path.Base(url)))
	}
	return ids, nil
}

// rodinSubmissions keeps the parts of every submission the fake receives.
type rodinSubmissions struct {
	mu    sync.Mutex
	forms [][]rodinPart
}

type rodinPart struct {
	name  string
	value string // a text part's value; a file part's is empty
	file  []byte
}

func (s *rodinSubmissions) record(t *testing.T, contentType string, body []byte) {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Errorf("submission content type %q: %v", contentType, err)
		return
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var parts []rodinPart
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Errorf("unreadable submission: %v", err)
			return
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			parts = append(parts, rodinPart{name: part.FormName(), file: data})
		} else {
			parts = append(parts, rodinPart{name: part.FormName(), value: string(data)})
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forms = append(s.forms, parts)
}

func (s *rodinSubmissions) all() [][]rodinPart {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.forms)
}

func TestAgentViewLabelsReachTheHyper3DWireInPictureOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()

	// The Hyper3D fake behind a loopback server that records what is posted to it.
	fake, err := fakeupstream.New(fakeupstream.Config{}, hyper3dapiv2.Version())
	if err != nil {
		t.Fatal(err)
	}
	submissions := &rodinSubmissions{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == hyper3dapiv2.Prefix+"/rodin" {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			submissions.record(t, r.Header.Get("Content-Type"), raw)
		}
		fake.ServeHTTP(w, r)
	}))
	defer server.Close()

	repo, err := sqlitetest.Open(filepath.Join(t.TempDir(), "labels.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	blobs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := inputassets.New(repo, blobs)
	if err != nil {
		t.Fatal(err)
	}
	registry := providers.NewRegistry()
	for _, connection := range app.Runtime.ProviderConnections {
		if connection.ID != "hyper3d" {
			continue
		}
		adapter, err := hyper3d.New(hyper3d.Config{
			BaseURL: server.URL + hyper3dapiv2.Prefix, APIKey: "synthetic-key",
			AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix,
			StatusMapping: connection.StatusMapping, ErrorMapping: connection.ErrorMapping,
			HTTPClient: server.Client(), AllowHTTPAssets: true, InputAssets: inputs,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.Register("hyper3d.rodin.v1", adapter); err != nil {
			t.Fatal(err)
		}
	}
	module, err := generation.New(generation.Dependencies{Conversions: generation.NoConversions, Repository: repo, Catalog: product, Providers: registry, Inputs: inputs, Assets: blobs})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := make(chan error, 1)
	go func() {
		coordinator <- module.Run(ctx, generation.CoordinatorConfig{PollEvery: 5 * time.Millisecond, LeaseFor: time.Second, BatchSize: 10})
	}()

	now := time.Now().UTC()
	if _, err := repo.CreateSession(ctx, chat.Session{ID: "session", OwnerID: "owner", OwnerName: "Owner", Title: "Views", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.StartTurn(ctx, "owner", chat.Message{ID: "user", SessionID: "session", Role: "user", Content: "views", CreatedAt: now},
		chat.Message{ID: "turn", SessionID: "session", Role: "assistant", Content: "pending", CreatedAt: now},
		chat.Event{SessionID: "session", Data: map[string]any{"type": "message.created"}, CreatedAt: now}, chat.Stream{SessionID: "session", Status: "active"},
		chat.Turn{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", State: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Three pictures of different sizes, so their bytes tell them apart on the wire.
	var ids []string
	var originals [][]byte
	var attachments []chat.Attachment
	var urls []string
	for index := 0; index < 3; index++ {
		picture := image.NewNRGBA(image.Rect(0, 0, 4+index, 4+index))
		picture.SetNRGBA(0, 0, color.NRGBA{R: uint8(40 + index), G: 50, B: 90, A: 255})
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, picture); err != nil {
			t.Fatal(err)
		}
		asset, err := inputs.UploadAsset(ctx, "owner", generation.InputAssetUpload{Body: bytes.NewReader(encoded.Bytes()), MediaType: "image/png", AssetType: "image", MaxBytes: 20 << 20})
		if err != nil {
			t.Fatal(err)
		}
		ids, originals = append(ids, string(asset.ID)), append(originals, encoded.Bytes())
		urls = append(urls, "/api/v1/input-assets/"+string(asset.ID))
		attachments = append(attachments, chat.Attachment{Handle: fmt.Sprintf("@img%d", index+1), Kind: chat.AttachmentKindImage,
			Role: chat.AttachmentRoleUpload, AssetID: string(asset.ID), ImageSlot: index + 1})
	}

	var manual, known []string
	for _, capability := range app.Chat.ApprovalPolicy.ManualApprovalCapabilities {
		manual = append(manual, string(capability))
	}
	for _, capability := range app.Capabilities {
		known = append(known, string(capability.ID))
	}
	grants := &executiongrant.Service{Grants: repo, Prepared: repo, Modes: sessionMode{mode: "auto"},
		Policy: approvalpolicy.New(app.Chat.ApprovalPolicy.DefaultMode, app.Chat.ApprovalPolicy.Modes, manual, known)}
	executor, err := generationtool.New(generationtool.Config{
		Gateway: module, References: assetNameResolver{}, Operations: repo, Grants: grants, Chat: app.Chat, TaskStates: app.TaskStates,
		InputAssetRoute: "/api/v1/input-assets/{assetID}",
		StopRequested:   func(context.Context, string, string, string) (bool, error) { return false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var tool catalog.ChatToolSchema
	for _, candidate := range app.Chat.Tools {
		if candidate.Name == "create_model_3d_from_image" {
			tool = candidate
		}
	}
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn", Tool: tool,
		Options: chat.TurnOptions{AIModel: "rodin-gen-2.5", CreativeDomain: "model-3d"},
		History: []chat.Message{{Role: app.Chat.Roles.User, Content: "make it 3D", ImageURLs: urls, Attachments: attachments}}}
	// call runs one tool call and returns the input the kernel froze for it. The
	// step is submitted, then the coordinator starts it: the call returns with
	// the Task to await, and the Adapter posts to the fake after that.
	call := func(id, arguments string) generation.PreparedInput {
		t.Helper()
		command.Call = agents.ToolCall{ID: id, Name: tool.Name, Arguments: json.RawMessage(arguments)}
		result, err := executor.Execute(ctx, command, nil)
		if err != nil || result.NoSubmission || result.TaskID == "" {
			t.Fatalf("%s was not submitted: %+v / %v", id, result, err)
		}
		operation, err := repo.LoadAgentOperation(ctx, "owner", "session", "turn", id)
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := module.GetPreparedInput(ctx, "owner", operation.PreparedInputID)
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	const inOrder = `"prompt":"a small robot","reference_handles":["@img1","@img2","@img3"]`
	plain := call("call-plain", `{`+inOrder+`}`)
	empty := call("call-empty", `{`+inOrder+`,"view_labels":{}}`)
	unknown := call("call-unknown", `{`+inOrder+`,"view_labels":{"@img2":"?"}}`)
	marked := call("call-marked", `{`+inOrder+`,"view_labels":{"@img1":"F","@img2":"L","@img3":"B"}}`)
	// The handles come in the order third, first, second; the marks in any order.
	shuffled := call("call-shuffled", `{"prompt":"a small robot","reference_handles":["@img3","@img1","@img2"],
		"view_labels":{"@img2":"L","@img3":"B","@img1":"F"}}`)

	// The kernel froze nothing new for a call whose marks say nothing: the
	// input and its hash are those of the call without the argument.
	for name, prepared := range map[string]generation.PreparedInput{"an empty map": empty, "an unknown mark only": unknown} {
		if prepared.InputHash != plain.InputHash || !bytes.Equal(prepared.NormalizedInput, plain.NormalizedInput) {
			t.Errorf("view_labels as %s changed the frozen input:\n got %s\nwant %s", name, prepared.NormalizedInput, plain.NormalizedInput)
		}
	}
	if bytes.Contains(plain.NormalizedInput, []byte("reference_image_labels")) {
		t.Errorf("the call without marks froze marks: %s", plain.NormalizedInput)
	}
	if marked.InputHash == plain.InputHash || !bytes.Contains(marked.NormalizedInput, []byte(`"reference_image_labels":["F","L","B"]`)) {
		t.Errorf("the marks are not part of what the grant authorizes: %s", marked.NormalizedInput)
	}
	if !bytes.Contains(shuffled.NormalizedInput, []byte(`"reference_image_labels":["B","F","L"]`)) {
		t.Errorf("shuffled marks froze as %s, want them in picture order", shuffled.NormalizedInput)
	}

	// All five steps reach the fake once the coordinator starts them.
	// A slow race runner starts the five steps late: the wait ends as soon as the fifth arrives, so a long limit costs nothing.
	deadline := time.Now().Add(240 * time.Second)
	forms := submissions.all()
	for len(forms) < 5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		forms = submissions.all()
	}
	if len(forms) != 5 {
		t.Fatalf("the fake received %d submissions, want 5", len(forms))
	}
	var withoutMarks [][]string
	var wires = map[string][2][]string{}
	for _, parts := range forms {
		var marks []string
		var shape []string
		var pictures []string
		firstPicture, lastMark := -1, -1
		for position, part := range parts {
			switch part.name {
			case "image_label":
				marks, lastMark = append(marks, part.value), position
			case "images":
				pictures = append(pictures, pictureNumber(t, part.file, originals))
				if firstPicture < 0 {
					firstPicture = position
				}
			}
			shape = append(shape, part.name+"="+part.value)
		}
		if marks == nil {
			withoutMarks = append(withoutMarks, append(shape, pictures...))
			continue
		}
		// The real service takes the marks ahead of the pictures, one text part each.
		if lastMark >= firstPicture {
			t.Errorf("the marks come after the pictures (last mark part %d, first picture part %d)", lastMark, firstPicture)
		}
		wires[strings.Join(marks, ",")] = [2][]string{marks, pictures}
	}
	// Each mark sits on its own picture: F, L, B on the first, second and third
	// pictures when they are sent in that order, B, F, L on the third, first and
	// second when the Agent names them that way round.
	if got := wires["F,L,B"]; !slices.Equal(got[1], []string{"1", "2", "3"}) {
		t.Errorf("marks F, L, B are on pictures %v, want 1, 2, 3 in that order (%v)", got[1], wires)
	}
	if got := wires["B,F,L"]; !slices.Equal(got[1], []string{"3", "1", "2"}) {
		t.Errorf("marks B, F, L are on pictures %v, want 3, 1, 2 in that order (%v)", got[1], wires)
	}
	// The three steps without a usable mark sent the request they would have sent
	// without the argument: no image_label part, and the same parts in the same order.
	if len(withoutMarks) != 3 || !slices.Equal(withoutMarks[0], withoutMarks[1]) || !slices.Equal(withoutMarks[0], withoutMarks[2]) {
		t.Errorf("requests without marks differ or are not three: %v", withoutMarks)
	}

	cancel()
	select {
	case <-coordinator:
	case <-time.After(30 * time.Second):
		t.Fatal("the coordinator did not stop")
	}
}

// pictureNumber names an uploaded picture on the wire by its place in the order
// it was uploaded, counted from 1.
func pictureNumber(t *testing.T, sent []byte, originals [][]byte) string {
	t.Helper()
	for index, original := range originals {
		if bytes.Equal(sent, original) {
			return fmt.Sprint(index + 1)
		}
	}
	t.Fatal("a picture on the wire is none of the uploaded ones")
	return ""
}

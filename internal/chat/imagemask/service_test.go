package imagemask_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/imagemask"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fakeInputs struct {
	pictures map[generation.AssetID][]byte
	uploads  []generation.InputAssetUpload
	bodies   [][]byte
	opens    []generation.AssetID
}

func (inputs *fakeInputs) UploadAsset(_ context.Context, owner generation.OwnerID,
	upload generation.InputAssetUpload) (generation.InputAsset, error) {
	body, err := io.ReadAll(upload.Body)
	if err != nil {
		return generation.InputAsset{}, err
	}
	inputs.uploads = append(inputs.uploads, upload)
	inputs.bodies = append(inputs.bodies, body)
	return generation.InputAsset{ID: "uploaded-mask", OwnerID: owner, Size: int64(len(body))}, nil
}

func (inputs *fakeInputs) OpenOwned(_ context.Context, owner generation.OwnerID,
	id generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	inputs.opens = append(inputs.opens, id)
	body, found := inputs.pictures[id]
	if !found || owner != "owner-a" {
		return generation.InputAsset{}, nil, errors.New("not owned")
	}
	return generation.InputAsset{ID: id, OwnerID: owner, Size: int64(len(body))},
		io.NopCloser(bytes.NewReader(body)), nil
}

type fakeTasks struct {
	pictures map[generation.AssetID][]byte
	owners   []generation.OwnerID
	taskIDs  []generation.TaskID
	assets   []generation.AssetID
}

func (tasks *fakeTasks) OpenAsset(_ context.Context, owner generation.OwnerID,
	taskID generation.TaskID, assetID generation.AssetID) (generation.AssetContent, error) {
	tasks.owners = append(tasks.owners, owner)
	tasks.taskIDs = append(tasks.taskIDs, taskID)
	tasks.assets = append(tasks.assets, assetID)
	body, found := tasks.pictures[assetID]
	if !found || owner != "owner-a" || taskID != "generated-task" {
		return generation.AssetContent{}, errors.New("no task asset")
	}
	return generation.AssetContent{Body: io.NopCloser(bytes.NewReader(body)), Size: int64(len(body))}, nil
}

func maskService(t *testing.T, inputs *fakeInputs, tasks *fakeTasks) (*imagemask.Service, catalog.ChatSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	service, err := imagemask.New(imagemask.Config{
		Context:          schema.Context,
		Mask:             schema.ImageMask,
		Inputs:           inputs,
		Tasks:            tasks,
		AssetItemRoute:   "/api/v1/assets/{assetID}",
		DefaultAssetType: "image",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, schema
}

func picturePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func command(schema catalog.ChatSchema, arguments string) chat.ToolCommand {
	return chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		History: []chat.Message{{Role: schema.Roles.User, ImageURLs: []string{"picture-url"},
			Attachments: []chat.Attachment{{Handle: "@img1", Kind: chat.AttachmentKindImage,
				Role: chat.AttachmentRoleUpload, AssetID: "picture", ImageSlot: 1}}}},
		Call: agents.ToolCall{ID: "call-mask", Name: schema.ImageMask.Tool, Arguments: []byte(arguments)},
	}
}

// #244: a rectangle mode call stores an owned PNG mask and names its URL.
func TestCreateSelectionMaskStoresTheRegionMask(t *testing.T) {
	inputs := &fakeInputs{pictures: map[generation.AssetID][]byte{"picture": picturePNG(t, 10, 10)}}
	service, schema := maskService(t, inputs, &fakeTasks{})
	result, err := service.Execute(context.Background(),
		command(schema, `{"image":"@img1","mode":"region","x0":2,"y0":2,"x1":6,"y1":6}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.NoSubmission || len(inputs.uploads) != 1 || len(result.ImageURLs) != 1 ||
		result.ImageURLs[0] != "/api/v1/assets/uploaded-mask" {
		t.Fatalf("result = %+v uploads = %+v", result, inputs.uploads)
	}
	if inputs.uploads[0].MediaType != "image/png" || inputs.uploads[0].OriginalFilename != "selection-mask.png" {
		t.Fatalf("upload = %+v", inputs.uploads[0])
	}
	decoded, _, err := image.Decode(bytes.NewReader(inputs.bodies[0]))
	if err != nil {
		t.Fatalf("uploaded mask does not decode: %v", err)
	}
	if _, _, _, alpha := decoded.At(3, 3).RGBA(); alpha>>8 >= 128 {
		t.Fatal("selected pixel is opaque")
	}
	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha>>8 < 128 {
		t.Fatal("kept pixel is transparent")
	}
	var content map[string]any
	if err := json.Unmarshal([]byte(result.AgentContent), &content); err != nil {
		t.Fatal(err)
	}
	if content["mask_url"] != "/api/v1/assets/uploaded-mask" || content["success"] != true {
		t.Fatalf("content = %#v", content)
	}
	note, _ := content["note"].(string)
	if !strings.Contains(note, "/api/v1/assets/uploaded-mask") || !strings.Contains(note, "透明=重画") {
		t.Fatalf("note = %q", note)
	}
}

// #244: subject mode splits an alpha picture; unknown handles and invalid
// regions are refusals with no upload.
func TestCreateSelectionMaskRefusals(t *testing.T) {
	inputs := &fakeInputs{pictures: map[generation.AssetID][]byte{"picture": picturePNG(t, 10, 10)}}
	service, schema := maskService(t, inputs, &fakeTasks{})
	for name, arguments := range map[string]string{
		"unknown handle": `{"image":"@img9","mode":"region","x0":0,"y0":0,"x1":2,"y1":2}`,
		"empty region":   `{"image":"@img1","mode":"region","x0":5,"y0":5,"x1":5,"y1":6}`,
		"outside region": `{"image":"@img1","mode":"region","x0":-1,"y0":0,"x1":6,"y1":6}`,
		"unknown mode":   `{"image":"@img1","mode":"magic"}`,
		"missing mode":   `{"image":"@img1"}`,
		"no subject":     `{"image":"@img1","mode":"subject"}`,
	} {
		result, err := service.Execute(context.Background(), command(schema, arguments))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !result.NoSubmission || result.Error == "" || len(inputs.uploads) != 0 {
			t.Fatalf("%s: result = %+v uploads = %d", name, result, len(inputs.uploads))
		}
	}
	// A picture the owner does not hold is unreadable, never read.
	foreign := &fakeInputs{}
	service, _ = maskService(t, foreign, &fakeTasks{})
	result, err := service.Execute(context.Background(),
		command(schema, `{"image":"@img1","mode":"region","x0":0,"y0":0,"x1":2,"y1":2}`))
	if err != nil || !result.NoSubmission || len(foreign.uploads) != 0 {
		t.Fatalf("foreign result = %+v, %v", result, err)
	}
}

// #244 regression: a generated picture carries both a TaskID and an AssetID,
// and its asset is stored with the Task, not in the input-asset store. The
// mask tool must read it through the Task (#239/#244), the same order
// imagereview uses; trying the input store first made every generated picture
// unreadable, which is the main target of @selected.
func TestCreateSelectionMaskReadsGeneratedPictureThroughItsTask(t *testing.T) {
	inputs := &fakeInputs{}
	tasks := &fakeTasks{pictures: map[generation.AssetID][]byte{
		"generated-picture": picturePNG(t, 10, 10),
	}}
	service, schema := maskService(t, inputs, tasks)
	selected := chat.ToolCommand{
		OwnerID: "owner-a", SessionID: "session-a",
		Options: chat.TurnOptions{Selection: []chat.Attachment{{
			Handle: "@img1", Kind: chat.AttachmentKindImage,
			Role: chat.AttachmentRoleGenerated, TaskID: "generated-task", AssetID: "generated-picture",
		}}},
		Call: agents.ToolCall{ID: "call-mask", Name: schema.ImageMask.Tool,
			Arguments: []byte(`{"image":"@selected","mode":"region","x0":2,"y0":2,"x1":6,"y1":6}`)},
	}
	result, err := service.Execute(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if result.NoSubmission || len(inputs.uploads) != 1 || len(result.ImageURLs) != 1 {
		t.Fatalf("result = %+v uploads = %+v", result, inputs.uploads)
	}
	if len(inputs.opens) != 0 {
		t.Fatalf("generated picture was read from the input store: opens = %v", inputs.opens)
	}
	if len(tasks.assets) != 1 || tasks.owners[0] != "owner-a" ||
		tasks.taskIDs[0] != "generated-task" || tasks.assets[0] != "generated-picture" {
		t.Fatalf("task reads = owners %v tasks %v assets %v", tasks.owners, tasks.taskIDs, tasks.assets)
	}
	decoded, _, err := image.Decode(bytes.NewReader(inputs.bodies[0]))
	if err != nil {
		t.Fatalf("uploaded mask does not decode: %v", err)
	}
	if _, _, _, alpha := decoded.At(3, 3).RGBA(); alpha>>8 >= 128 {
		t.Fatal("selected pixel is opaque")
	}
	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha>>8 < 128 {
		t.Fatal("kept pixel is transparent")
	}
}

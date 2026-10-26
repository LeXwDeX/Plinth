package references_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestResolverAcceptsDataURIAndOwnedInputAssetRoutes(t *testing.T) {
	inputs := &fakeInputs{
		owned: map[generation.AssetID]generation.InputAsset{
			"owned-a": {ID: "owned-a", OwnerID: "owner-a"},
		},
	}
	resolver := newResolver(t, inputs, &fakeTasks{}, fakeLegacy{})
	ids, err := resolver.Resolve(
		context.Background(),
		"owner-a",
		[]string{
			"data:image/png;base64,Y29uZmlndXJlZA==",
			"/configured/assets/owned-a",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "uploaded-1" || ids[1] != "owned-a" {
		t.Fatalf("ids = %#v", ids)
	}
	if len(inputs.uploads) != 1 ||
		inputs.uploads[0].mediaType != "image/png" ||
		inputs.uploads[0].body != "configured" ||
		// 参数级扩展名校验要求上传文件名带媒体类型扩展名
		inputs.uploads[0].filename != "configured-reference.png" {
		t.Fatalf("uploads = %#v", inputs.uploads)
	}
}

func TestResolverMaterializesUnifiedAndHistoricalGeneratedAssets(t *testing.T) {
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"task-a": {
				ID:      "task-a",
				OwnerID: "owner-a",
				Assets: []generation.Asset{{
					ID:        "result-a",
					Role:      "configured-image-role",
					Ordinal:   1,
					MediaType: "image/webp",
				}},
			},
			"historical": {ID: "historical", OwnerID: "owner-a"},
		},
		contents: map[string]generation.AssetContent{
			"task-a/result-a": {
				Body:      io.NopCloser(strings.NewReader("task-image")),
				MediaType: "image/webp",
				Size:      10,
			},
		},
	}
	legacy := fakeLegacy{files: map[string]string{
		"historical_0.png": "historical-image",
	}}
	resolver := newResolver(t, inputs, tasks, legacy)
	ids, err := resolver.Resolve(
		context.Background(),
		"owner-a",
		[]string{
			"/configured/tasks/task-a/assets/result-a",
			"/configured/generated/historical_0.png",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "uploaded-1" || ids[1] != "uploaded-2" {
		t.Fatalf("ids = %#v", ids)
	}
	if inputs.uploads[0].body != "task-image" ||
		inputs.uploads[0].mediaType != "image/webp" ||
		inputs.uploads[1].body != "historical-image" ||
		inputs.uploads[1].mediaType != "image/png" {
		t.Fatalf("uploads = %#v", inputs.uploads)
	}
}

func TestResolverRejectsExternalAndUnconfiguredReferences(t *testing.T) {
	resolver := newResolver(
		t,
		&fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}},
		&fakeTasks{},
		fakeLegacy{},
	)
	for _, value := range []string{
		"https://attacker.example/image.png",
		"/configured/generated/../secret",
		"/not-configured/image.png",
	} {
		if _, err := resolver.Resolve(
			context.Background(),
			"owner-a",
			[]string{value},
		); !errors.Is(err, references.ErrUnsupportedReference) {
			t.Fatalf("%q error = %v", value, err)
		}
	}
}

// 任务存在但不属于当前用户时，不得回退到无归属校验的历史文件，
// 也不能把他人生成图复制为自己的输入资产。
func TestResolverRefusesForbiddenLegacyGeneratedFallback(t *testing.T) {
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
	tasks := &fakeTasks{forbidden: map[generation.TaskID]bool{"task-b": true}}
	legacy := fakeLegacy{files: map[string]string{
		"task-b_0.png": "someone-elses-image",
	}}
	resolver := newResolver(t, inputs, tasks, legacy)
	_, err := resolver.Resolve(
		context.Background(),
		"owner-a",
		[]string{"/configured/generated/task-b_0.png"},
	)
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) ||
		domainErr.Code != generation.ErrForbidden {
		t.Fatalf("error = %v", err)
	}
	if len(inputs.uploads) != 0 {
		t.Fatalf("uploads = %#v", inputs.uploads)
	}
}

func TestGeneratedReferenceRequiresOwnedTaskEvidence(t *testing.T) {
	storageErr := errors.New("task database unavailable")
	for _, test := range []struct {
		name     string
		filename string
		tasks    fakeTasks
		wantErr  error
	}{
		{
			name:     "unparseable legacy filename",
			filename: "private.png",
		},
		{
			name:     "missing task",
			filename: "missing_0.png",
		},
		{
			name:     "task store unavailable",
			filename: "private_0.png",
			tasks:    fakeTasks{getErr: storageErr},
			wantErr:  storageErr,
		},
		{
			name:     "task and existence stores unavailable",
			filename: "private_0.png",
			tasks:    fakeTasks{getErr: storageErr, existsErr: storageErr},
			wantErr:  storageErr,
		},
		{
			name:     "foreign task with existence store unavailable",
			filename: "private_0.png",
			tasks: fakeTasks{
				forbidden: map[generation.TaskID]bool{"private": true},
				existsErr: storageErr,
			},
		},
		{
			name:     "task fact has no owner",
			filename: "private_0.png",
			tasks: fakeTasks{tasks: map[generation.TaskID]generation.Task{
				"private": {ID: "private"},
			}},
		},
		{
			name:     "task fact has another owner",
			filename: "private_0.png",
			tasks: fakeTasks{tasks: map[generation.TaskID]generation.Task{
				"private": {ID: "private", OwnerID: "owner-b"},
			}},
		},
		{
			name:     "task fact has another ID",
			filename: "private_0.png",
			tasks: fakeTasks{tasks: map[generation.TaskID]generation.Task{
				"private": {ID: "another", OwnerID: "owner-a"},
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
			var opened []string
			resolver := newResolver(t, inputs, &test.tasks, fakeLegacy{
				files:  map[string]string{test.filename: "private image bytes"},
				opened: &opened,
			})
			ids, err := resolver.Resolve(t.Context(), "owner-a", []string{
				"/configured/generated/" + test.filename,
			})
			if len(ids) != 0 || !errors.Is(err, references.ErrUnsupportedReference) {
				t.Fatalf("ids=%v error=%v", ids, err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("lost storage failure: %v", err)
			}
			if len(opened) != 0 || len(inputs.uploads) != 0 || test.tasks.existsCalls != 0 {
				t.Fatalf("opened=%v uploads=%v existence calls=%d", opened, inputs.uploads, test.tasks.existsCalls)
			}
		})
	}
}

func TestGeneratedReferencePropagatesNativeAssetFailure(t *testing.T) {
	assetErr := errors.New("native asset unavailable")
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
	tasks := &fakeTasks{
		openErr: assetErr,
		tasks: map[generation.TaskID]generation.Task{
			"owned": {
				ID: "owned", OwnerID: "owner-a",
				Assets: []generation.Asset{{ID: "image", Role: "configured-image-role", Ordinal: 1}},
			},
		},
	}
	var opened []string
	resolver := newResolver(t, inputs, tasks, fakeLegacy{
		files:  map[string]string{"owned_0.png": "stale legacy bytes"},
		opened: &opened,
	})
	ids, err := resolver.Resolve(t.Context(), "owner-a", []string{"/configured/generated/owned_0.png"})
	if len(ids) != 0 || !errors.Is(err, assetErr) || len(opened) != 0 || len(inputs.uploads) != 0 {
		t.Fatalf("ids=%v error=%v opened=%v uploads=%v", ids, err, opened, inputs.uploads)
	}
}

func TestGeneratedReferenceUsesOwnedNativeAssetBeforeLegacyFile(t *testing.T) {
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"owned": {
				ID: "owned", OwnerID: "owner-a",
				Assets: []generation.Asset{{ID: "image", Role: "configured-image-role", Ordinal: 1, MediaType: "image/png"}},
			},
		},
		contents: map[string]generation.AssetContent{
			"owned/image": {Body: io.NopCloser(strings.NewReader("native image")), MediaType: "image/png"},
		},
	}
	var opened []string
	resolver := newResolver(t, inputs, tasks, fakeLegacy{
		files:  map[string]string{"owned_0.png": "stale legacy bytes"},
		opened: &opened,
	})
	ids, err := resolver.Resolve(t.Context(), "owner-a", []string{"/configured/generated/owned_0.png"})
	if err != nil || len(ids) != 1 || len(opened) != 0 || len(inputs.uploads) != 1 || inputs.uploads[0].body != "native image" {
		t.Fatalf("ids=%v error=%v opened=%v uploads=%v", ids, err, opened, inputs.uploads)
	}
}

func newResolver(
	t *testing.T,
	inputs *fakeInputs,
	tasks *fakeTasks,
	legacy fakeLegacy,
) *references.Resolver {
	t.Helper()
	resolver, err := references.New(references.Config{
		Inputs: inputs,
		Tasks:  tasks,
		Legacy: legacy,
		InputAssetRoutes: []string{
			"/configured/assets/{assetID}",
			"/configured/core-assets/{assetID}",
		},
		TaskAssetRoute:                     "/configured/tasks/{taskID}/assets/{assetID}",
		GeneratedImageRoute:                "/configured/generated/{filename}",
		UploadMaxBytes:                     1024,
		GeneratedReferenceMaxBytes:         testGeneratedReferenceMaxBytes,
		GeneratedReferenceTooLargeTemplate: "configured too large {size} over {limit}",
		DefaultMediaType:                   "application/octet-stream",
		DefaultAssetType:                   "configured-image",
		ReferenceFilename:                  "configured-reference",
		LegacyGeneratedImage: catalog.LegacyGeneratedImageRuntimeSchema{
			FilenameSeparator: "_",
			OrdinalBase:       0,
			AssetRoles:        []string{"configured-image-role"},
			ExtensionMediaTypes: map[string]string{
				"png": "image/png",
			},
			DefaultMediaType: "image/png",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

// testGeneratedReferenceMaxBytes is larger than the test upload limit (1024),
// as the Schema's generated_reference_max_bytes is larger than
// upload_max_bytes.
const testGeneratedReferenceMaxBytes = 4096

type recordedUpload struct {
	body      string
	filename  string
	mediaType string
	assetType string
	maxBytes  int64
}

type fakeInputs struct {
	owned   map[generation.AssetID]generation.InputAsset
	uploads []recordedUpload
}

func (inputs *fakeInputs) UploadAsset(
	_ context.Context,
	owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
	// Like the input asset service, read at most MaxBytes+1 and refuse more.
	body, err := io.ReadAll(io.LimitReader(upload.Body, upload.MaxBytes+1))
	if err != nil {
		return generation.InputAsset{}, err
	}
	if upload.MaxBytes <= 0 || int64(len(body)) > upload.MaxBytes {
		return generation.InputAsset{}, &generation.Error{
			Code: generation.ErrInvalidInput, Operation: "upload_input_asset",
			Detail: "上传文件为空、过大或不可读取",
		}
	}
	inputs.uploads = append(inputs.uploads, recordedUpload{
		body:      string(body),
		filename:  upload.OriginalFilename,
		mediaType: upload.MediaType,
		assetType: upload.AssetType,
		maxBytes:  upload.MaxBytes,
	})
	id := generation.AssetID("uploaded-" + string(rune('0'+len(inputs.uploads))))
	asset := generation.InputAsset{ID: id, OwnerID: owner}
	inputs.owned[id] = asset
	return asset, nil
}

func (inputs *fakeInputs) OpenOwned(
	_ context.Context,
	owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	asset, found := inputs.owned[id]
	if !found || asset.OwnerID != owner {
		return generation.InputAsset{}, nil, errors.New("not owned")
	}
	return asset, io.NopCloser(bytes.NewReader(nil)), nil
}

type fakeTasks struct {
	tasks       map[generation.TaskID]generation.Task
	contents    map[string]generation.AssetContent
	forbidden   map[generation.TaskID]bool
	getErr      error
	openErr     error
	existsErr   error
	existsCalls int
}

func (tasks *fakeTasks) Get(
	_ context.Context,
	_ generation.OwnerID,
	id generation.TaskID,
) (generation.Task, error) {
	if tasks.getErr != nil {
		return generation.Task{}, tasks.getErr
	}
	if tasks.forbidden[id] {
		return generation.Task{}, &generation.Error{
			Code: generation.ErrForbidden,
		}
	}
	task, found := tasks.tasks[id]
	if !found {
		return generation.Task{}, &generation.Error{Code: generation.ErrNotFound}
	}
	return task, nil
}

func (tasks *fakeTasks) Exists(
	_ context.Context,
	id generation.TaskID,
) (bool, error) {
	tasks.existsCalls++
	if tasks.existsErr != nil {
		return false, tasks.existsErr
	}
	if tasks.forbidden[id] {
		return true, nil
	}
	_, found := tasks.tasks[id]
	return found, nil
}

func (tasks *fakeTasks) OpenAsset(
	_ context.Context,
	_ generation.OwnerID,
	taskID generation.TaskID,
	assetID generation.AssetID,
) (generation.AssetContent, error) {
	if tasks.openErr != nil {
		return generation.AssetContent{}, tasks.openErr
	}
	content, found := tasks.contents[string(taskID)+"/"+string(assetID)]
	if !found {
		return generation.AssetContent{}, errors.New("not found")
	}
	return content, nil
}

type fakeLegacy struct {
	files  map[string]string
	opened *[]string
}

func (legacy fakeLegacy) Open(
	_ context.Context,
	filename string,
) (io.ReadCloser, int64, error) {
	if legacy.opened != nil {
		*legacy.opened = append(*legacy.opened, filename)
	}
	body, found := legacy.files[filename]
	if !found {
		return nil, 0, errors.New("not found")
	}
	return io.NopCloser(strings.NewReader(body)), int64(len(body)), nil
}

package generation_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

type referenceDeletionAdapter struct{ generation.ProviderPort }

func (referenceDeletionAdapter) DeleteProviderTask(context.Context, generation.DeleteProviderRequest) error {
	return nil
}

type retainedReferenceFile struct {
	body   []byte
	opened int
}

func (file *retainedReferenceFile) Open(context.Context, string) (io.ReadCloser, int64, error) {
	file.opened++
	return io.NopCloser(bytes.NewReader(file.body)), int64(len(file.body)), nil
}

type referenceInputRecorder struct {
	references.InputGateway
	uploads int
}

func (inputs *referenceInputRecorder) UploadAsset(ctx context.Context, owner generation.OwnerID, upload generation.InputAssetUpload) (generation.InputAsset, error) {
	inputs.uploads++
	return inputs.InputGateway.UploadAsset(ctx, owner, upload)
}

func TestRetainedForeignTaskReferenceNeverReassignsOwner(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t, referenceDeletionAdapter{mock.New()}, nil)
	accepted, err := h.submitApproved(t, ctx, generation.SubmitCommand{
		OwnerID: "owner-b", Capability: generation.TextToImage,
		OfferingID: "openai/gpt-image-2/text-to-image",
		Input:      []byte(`{"prompt":"private image"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := h.runUntilTerminal(t, "owner-b", accepted.TaskID)
	if task.State != generation.TaskSucceeded || len(task.Assets) != 1 {
		t.Fatalf("task=%#v", task)
	}
	content, err := h.module.OpenAsset(ctx, "owner-b", task.ID, task.Assets[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Body)
	closeErr := content.Body.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read=%v close=%v", err, closeErr)
	}
	legacy := &retainedReferenceFile{body: body}
	inputs := &referenceInputRecorder{InputGateway: h.inputs}
	resolver, err := references.New(references.Config{
		Inputs: inputs, Tasks: h.module, Legacy: legacy,
		InputAssetRoutes: []string{"/assets/{assetID}"},
		TaskAssetRoute:   "/tasks/{taskID}/assets/{assetID}", GeneratedImageRoute: "/generated/{filename}",
		UploadMaxBytes: 1 << 20, GeneratedReferenceMaxBytes: 1 << 20, GeneratedReferenceTooLargeTemplate: "too large {size} {limit}", DefaultMediaType: "image/png", DefaultAssetType: "image", ReferenceFilename: "reference.png",
		LegacyGeneratedImage: catalog.LegacyGeneratedImageRuntimeSchema{
			FilenameSeparator: "_", OrdinalBase: 0, AssetRoles: []string{"image"},
			ExtensionMediaTypes: map[string]string{"png": "image/png"}, DefaultMediaType: "image/png",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := "/generated/" + string(task.ID) + "_0.png"
	ids, err := resolver.Resolve(ctx, "owner-b", []string{ref})
	if err != nil || len(ids) != 1 || inputs.uploads != 1 || legacy.opened != 0 {
		t.Fatalf("owned native reference: ids=%v error=%v uploads=%d legacy opens=%d", ids, err, inputs.uploads, legacy.opened)
	}
	owned, assetBody, err := h.inputs.OpenOwned(ctx, "owner-b", ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := assetBody.Close(); err != nil {
		t.Fatal(err)
	}
	if owned.OwnerID != "owner-b" {
		t.Fatalf("copied input owner=%s", owned.OwnerID)
	}
	assertDenied := func(owner generation.OwnerID) {
		t.Helper()
		ids, err := resolver.Resolve(ctx, owner, []string{ref})
		if len(ids) != 0 || !errors.Is(err, references.ErrUnsupportedReference) || inputs.uploads != 1 || legacy.opened != 0 {
			t.Fatalf("owner=%s ids=%v error=%v uploads=%d legacy opens=%d", owner, ids, err, inputs.uploads, legacy.opened)
		}
	}
	assertDenied("owner-a")
	if err := h.module.Delete(ctx, generation.DeleteCommand{
		Scope: generation.OwnTasks("owner-b"), ActorID: "owner-b", TaskID: task.ID,
		ExpectedCapability: generation.TextToImage,
		Policy: generation.TaskDeletionPolicy{
			LocalRecordPolicy: "tombstone", UpstreamPolicy: "when-supported", UpstreamFailurePolicy: "abort",
			GeneratedAssetPolicy: "retain", AuditEventType: "task.deleted",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if tombstoned, err := h.repository.TaskTombstoned(ctx, task.ID); err != nil || !tombstoned {
		t.Fatalf("tombstone=%v error=%v", tombstoned, err)
	}
	if existed, err := h.module.Exists(ctx, task.ID); err != nil || !existed {
		t.Fatalf("retained task existence=%v error=%v", existed, err)
	}
	assertDenied("owner-a")
	assertDenied("owner-b")
}

type referenceHistoryReadFault struct {
	generation.Repository
	loadErr      error
	tombstoneErr error
}

func (repository referenceHistoryReadFault) Load(context.Context, generation.TaskID) (generation.Aggregate, error) {
	return generation.Aggregate{}, repository.loadErr
}

func (repository referenceHistoryReadFault) TaskTombstoned(context.Context, generation.TaskID) (bool, error) {
	return false, repository.tombstoneErr
}

func TestTaskExistenceDoesNotConvertStorageFailureToAbsence(t *testing.T) {
	h := newHarness(t, mock.New(), nil)
	if existed, err := h.module.Exists(t.Context(), "never-persisted"); err != nil || existed {
		t.Fatalf("missing task: exists=%v error=%v", existed, err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	storageErr := errors.New("history database unavailable")
	for _, test := range []struct {
		name string
		repo referenceHistoryReadFault
	}{
		{"task lookup", referenceHistoryReadFault{Repository: h.repository, loadErr: storageErr}},
		{"tombstone lookup", referenceHistoryReadFault{Repository: h.repository, loadErr: generation.RepositoryNotFound, tombstoneErr: storageErr}},
	} {
		t.Run(test.name, func(t *testing.T) {
			module, err := generation.New(generation.Dependencies{
				Repository: test.repo, Catalog: product, Providers: providers.NewRegistry(), Assets: h.assets,
			})
			if err != nil {
				t.Fatal(err)
			}
			existed, err := module.Exists(t.Context(), "private")
			if !existed || err == nil || !errors.Is(err, storageErr) {
				t.Fatalf("unknown existence: exists=%v error=%v", existed, err)
			}
		})
	}
}

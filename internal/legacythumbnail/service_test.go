package legacythumbnail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestBackfillAllowsOnlyExplicitAcknowledgedOriginalSourceLoss(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := taskCatalog.Get("meshy/meshy-7/text-to-3d")
	if !ok {
		t.Fatal("缺少测试 Offering")
	}
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	store, err := local.New(filepath.Join(t.TempDir(), "storage"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	for index, taskID := range []generation.TaskID{
		"acknowledged-loss",
		"unexpected-loss",
	} {
		importLegacyThumbnailTask(
			t,
			repository,
			offering,
			taskID,
			[]byte(`{"thumbnail_url":"https://assets.meshy.ai/`+
				string(taskID)+`.png"}`),
			now.Add(time.Duration(index)*time.Minute),
		)
	}
	service, err := New(Config{
		Repository:    repository,
		Assets:        store,
		Source:        &thumbnailSourceStub{err: assetdelivery.ErrSourceNotFound},
		ThumbnailRole: "thumbnail",
		SourceFields: map[generation.Capability][]string{
			generation.TextTo3D: {"thumbnail_url"},
		},
		AcknowledgedSourceLosses: map[generation.TaskID]string{
			"acknowledged-loss": "原 Next 缓存与上游均已不存在",
		},
		BatchSize:        10,
		MaximumAssetSize: 10 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := service.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 2 ||
		report.Eligible != 2 ||
		report.Migrated != 0 ||
		report.AcknowledgedUnavailable != 1 ||
		report.Unavailable != 1 ||
		len(report.Acknowledgements) != 1 ||
		report.Acknowledgements[0].TaskID != "acknowledged-loss" ||
		len(report.Issues) != 1 ||
		report.Issues[0].TaskID != "unexpected-loss" {
		t.Fatalf("来源缺失补迁移报告 = %#v", report)
	}
}

func TestBackfillMaterializesOnlyDeclaredMissingThumbnailAndIsIdempotent(
	t *testing.T,
) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	offering, ok := taskCatalog.Get("meshy/meshy-7/text-to-3d")
	if !ok {
		t.Fatal("缺少测试 Offering")
	}
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	store, err := local.New(filepath.Join(t.TempDir(), "storage"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	previewURL := "https://assets.meshy.ai/demo/tasks/with-preview/output/preview.png?expired=1"
	importLegacyThumbnailTask(
		t,
		repository,
		offering,
		"with-preview",
		[]byte(`{"thumbnail_url":"`+previewURL+`"}`),
		now,
	)
	importLegacyThumbnailTask(
		t,
		repository,
		offering,
		"without-preview",
		[]byte(`{"texture_urls":[{"base_color":"https://assets.meshy.ai/texture.png"}]}`),
		now.Add(time.Minute),
	)
	source := &thumbnailSourceStub{
		content: []byte{
			'R', 'I', 'F', 'F', 0x04, 0x00, 0x00, 0x00,
			'W', 'E', 'B', 'P', 'V', 'P', '8', ' ',
		},
		mediaType: "application/octet-stream",
	}
	service, err := New(Config{
		Repository:    repository,
		Assets:        store,
		Source:        source,
		ThumbnailRole: "thumbnail",
		SourceFields: map[generation.Capability][]string{
			generation.TextTo3D: {"thumbnail_url"},
		},
		BatchSize:        10,
		MaximumAssetSize: 10 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := service.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 2 ||
		report.Eligible != 1 ||
		report.Migrated != 1 ||
		report.AlreadyPresent != 0 ||
		report.Unavailable != 0 {
		t.Fatalf("首次补迁移报告 = %#v", report)
	}
	if len(source.requests) != 1 ||
		source.requests[0].TaskID != "with-preview" ||
		source.requests[0].URL != previewURL {
		t.Fatalf("截图来源请求 = %#v", source.requests)
	}
	task, err := repository.Get(t.Context(), "owner-a", "with-preview")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Assets) != 1 ||
		task.Assets[0].Role != "thumbnail" ||
		task.Assets[0].MediaType != "image/webp" {
		t.Fatalf("补迁移资产 = %#v", task.Assets)
	}
	body, err := store.Open(t.Context(), task.Assets[0])
	if err != nil {
		t.Fatal(err)
	}
	migrated, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("读取补迁移资产: read=%v close=%v", readErr, closeErr)
	}
	if !bytes.Equal(migrated, source.content) {
		t.Fatalf("补迁移字节 = %q", migrated)
	}

	second, err := service.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 2 ||
		second.Eligible != 1 ||
		second.Migrated != 0 ||
		second.AlreadyPresent != 1 ||
		second.Unavailable != 0 {
		t.Fatalf("重复补迁移报告 = %#v", second)
	}
	if len(source.requests) != 1 {
		t.Fatalf("重复执行不应重新下载: %#v", source.requests)
	}
	withoutPreview, err := repository.Get(
		t.Context(),
		"owner-a",
		"without-preview",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutPreview.Assets) != 0 {
		t.Fatalf(
			"原 Next 缺截图的任务不得生成预览: %#v",
			withoutPreview.Assets,
		)
	}
}

func importLegacyThumbnailTask(
	t *testing.T,
	repository *sqlite.Repository,
	offering generation.Offering,
	taskID generation.TaskID,
	opaque []byte,
	createdAt time.Time,
) {
	t.Helper()
	finishedAt := createdAt.Add(time.Minute)
	attemptID := generation.AttemptID(string(taskID) + "-attempt-1")
	upstreamJob := &generation.UpstreamJob{
		ProviderID: offering.ProviderID,
		RemoteID:   string(taskID),
	}
	err := repository.ImportGenerationTasks(
		t.Context(),
		[]sqlite.GenerationTaskImport{{
			Task: generation.Task{
				ID:               taskID,
				OwnerID:          "owner-a",
				Capability:       generation.TextTo3D,
				Offering:         offering,
				Input:            []byte(`{"prompt":"legacy"}`),
				RequestHash:      "request-" + string(taskID),
				State:            generation.TaskSucceeded,
				Progress:         100,
				CurrentAttemptID: attemptID,
				Version:          1,
				CreatedAt:        createdAt,
				UpdatedAt:        finishedAt,
				FinishedAt:       &finishedAt,
			},
			Attempts: []sqlite.GenerationAttemptImport{{
				ID:            attemptID,
				Ordinal:       1,
				Offering:      offering,
				State:         generation.AttemptUpstreamSucceeded,
				SubmissionKey: "submission-" + string(taskID),
				UpstreamJob:   upstreamJob,
				Opaque:        opaque,
				StartedAt:     createdAt,
				UpdatedAt:     finishedAt,
				FinishedAt:    &finishedAt,
			}},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
}

type thumbnailSourceStub struct {
	onFetch   func() error
	content   []byte
	mediaType string
	err       error
	requests  []assetdelivery.SourceRequest
}

func validThumbnailPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 1, 1))
	canvas.Set(0, 0, color.RGBA{G: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func (s *thumbnailSourceStub) Fetch(
	ctx context.Context,
	request assetdelivery.SourceRequest,
) (assetdelivery.SourceContent, error) {
	if err := ctx.Err(); err != nil {
		return assetdelivery.SourceContent{}, err
	}
	s.requests = append(s.requests, request)
	if s.onFetch != nil {
		if err := s.onFetch(); err != nil {
			return assetdelivery.SourceContent{}, err
		}
	}
	if s.err != nil {
		return assetdelivery.SourceContent{}, s.err
	}
	return assetdelivery.SourceContent{
		Body:      io.NopCloser(bytes.NewReader(s.content)),
		MediaType: s.mediaType,
		Size:      int64(len(s.content)),
	}, nil
}

// Inject a concurrent task edit after the source was read but before registration.
type conflictingThumbnailRepository struct {
	generation.Repository
	conflicts  int
	persistent bool
}

func (r *conflictingThumbnailRepository) Commit(ctx context.Context, c generation.Commit) error {
	if r.conflicts == 0 || r.persistent {
		r.conflicts++
		changed := c.Task
		changed.Version = c.ExpectedVersion + 1
		if err := r.Repository.Commit(ctx, generation.Commit{ExpectedVersion: c.ExpectedVersion, Task: changed}); err != nil {
			return err
		}
		return generation.RepositoryConflict
	}
	return r.Repository.Commit(ctx, c)
}
func TestBackfillRetriesUnrelatedVersionConflictOnce(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprint(persistent), func(t *testing.T) {
			catalog, _ := catalog.LoadDefault()
			offering, _ := catalog.Get("meshy/meshy-7/text-to-3d")
			repo, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			importLegacyThumbnailTask(t, repo, offering, "conflict", []byte(`{"thumbnail_url":"https://assets.meshy.ai/preview.png"}`), time.Now())
			assets, err := local.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source := &thumbnailSourceStub{content: validThumbnailPNG(t), mediaType: "image/png"}
			wrapped := &conflictingThumbnailRepository{Repository: repo, persistent: persistent}
			service, err := New(Config{Repository: wrapped, Assets: assets, Source: source, ThumbnailRole: "thumbnail", SourceFields: map[generation.Capability][]string{generation.TextTo3D: {"thumbnail_url"}}, BatchSize: 10, MaximumAssetSize: 1024})
			if err != nil {
				t.Fatal(err)
			}
			report := Report{}
			service.backfillTask(t.Context(), "conflict", &report)
			if persistent {
				if report.Unavailable != 1 || wrapped.conflicts != 2 {
					t.Fatalf("report=%+v conflicts=%d", report, wrapped.conflicts)
				}
			} else if report.Migrated != 1 || report.Unavailable != 0 {
				t.Fatalf("report=%+v", report)
			}
			if len(source.requests) != 1 {
				t.Fatalf("source refetched %d times", len(source.requests))
			}
		})
	}
}

func TestBackfillRetriesVersionChangedBySignedURLRefresh(t *testing.T) {
	taskCatalog, _ := catalog.LoadDefault()
	offering, _ := taskCatalog.Get("meshy/meshy-7/text-to-3d")
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	importLegacyThumbnailTask(t, repo, offering, "signed", []byte(`{"thumbnail_url":"https://assets.meshy.ai/preview.png?signature=old"}`), time.Now())
	assets, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()
	source := &thumbnailSourceStub{content: validThumbnailPNG(t), mediaType: "image/png", onFetch: func() error {
		aggregate, err := repo.Load(t.Context(), "signed")
		if err != nil {
			return err
		}
		changed := aggregate.Task
		changed.Version++
		aggregate.CurrentAttempt.Opaque = []byte(`{"thumbnail_url":"https://assets.meshy.ai/preview.png?signature=fresh"}`)
		return repo.Commit(t.Context(), generation.Commit{ExpectedVersion: aggregate.Task.Version, Task: changed, Attempt: aggregate.CurrentAttempt})
	}}
	service, err := New(Config{Repository: repo, Assets: assets, Source: source, ThumbnailRole: "thumbnail", SourceFields: map[generation.Capability][]string{generation.TextTo3D: {"thumbnail_url"}}, BatchSize: 10, MaximumAssetSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	report := Report{}
	service.backfillTask(t.Context(), "signed", &report)
	if report.Migrated != 1 || report.Unavailable != 0 || len(source.requests) != 1 {
		t.Fatalf("report=%+v requests=%d", report, len(source.requests))
	}
}

package administration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type memoryUserAssetsRepository struct {
	query  UserAssetsQuery
	result UserAssetsOverview
	found  bool
	err    error
}

func TestUserAssetsProjectionDropsHistoricalProviderURLsEverywhere(t *testing.T) {
	const providerURL = "https://cdn.provider.example/model.glb?token=secret"
	repository := &memoryUserAssetsRepository{
		found: true,
		result: UserAssetsOverview{
			Generations: UserAssetsPage[UserAssetGeneration]{
				Data: []UserAssetGeneration{{
					TaskID:       "task-1",
					ResultURLs:   []string{providerURL},
					ThumbnailURL: providerURL,
					Params:       json.RawMessage(`{"prompt":"chair","nested":{"url":"` + providerURL + `"},"encoded":"https%3A%2F%2Fcdn.provider.example%2Fprivate"}`),
					Assets: []GenerationAsset{
						{ID: "cached-asset", Role: "model"},
						{ID: "upstream-only", Role: "thumbnail", SourceURL: providerURL},
					},
				}},
			},
		},
	}
	module, err := NewUserAssetsModule(UserAssetsModuleConfig{
		Repository: repository, DefaultPage: 1, DefaultPageSize: 10,
		MaximumPageSize: 20, NativeAssetRoute: "/api/v1/tasks/{taskID}/assets/{assetID}",
		DownloadActions: []string{"download_asset"},
		ThumbnailRole:   "thumbnail",
	})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := module.QueryUserAssets(context.Background(), UserAssetsRequest{UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	projected := overview.Generations.Data[0]
	if len(projected.ResultURLs) != 1 || projected.ResultURLs[0] != "/api/v1/tasks/task-1/assets/cached-asset" || projected.ThumbnailURL != "" {
		t.Fatalf("unsafe resource projection: %+v", projected)
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider.example") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("historical provider locator leaked: %s", encoded)
	}
	var params map[string]any
	if err := json.Unmarshal(projected.Params, &params); err != nil {
		t.Fatal(err)
	}
	nested := params["nested"].(map[string]any)
	if params["prompt"] != "chair" || nested["url"] != "" || params["encoded"] != "" {
		t.Fatalf("unsafe params projection: %#v", params)
	}
}

func (repository *memoryUserAssetsRepository) QueryAdministrationUserAssets(
	_ context.Context,
	query UserAssetsQuery,
) (UserAssetsOverview, bool, error) {
	repository.query = query
	return repository.result, repository.found, repository.err
}

func TestUserAssetsModuleNormalizesOneOverviewQuery(t *testing.T) {
	repository := &memoryUserAssetsRepository{
		found: true,
		result: UserAssetsOverview{
			User: User{ID: "user-1", Username: "alice"},
			Summary: UserAssetsSummary{
				TotalUploads:      3,
				TotalGenerations:  4,
				TotalDownloads:    5,
				TotalStorageBytes: 2048,
			},
			Generations: UserAssetsPage[UserAssetGeneration]{
				Data: []UserAssetGeneration{{
					ID:     "record-1",
					TaskID: "task / 1",
					Assets: []GenerationAsset{
						{ID: "image / 1", Role: "image"},
						{ID: "thumb / 1", Role: "configured-thumb"},
					},
				}},
			},
		},
	}
	module, err := NewUserAssetsModule(UserAssetsModuleConfig{
		Repository:       repository,
		DefaultPage:      1,
		DefaultPageSize:  50,
		MaximumPageSize:  100,
		DownloadActions:  []string{"download-event", "access-event"},
		NativeAssetRoute: "/custom/tasks/{task}/assets/{asset}",
		ThumbnailRole:    "configured-thumb",
	})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	to := time.Date(2026, 7, 23, 23, 59, 59, 0, time.UTC)

	overview, err := module.QueryUserAssets(
		context.Background(),
		UserAssetsRequest{
			UserID:          "user-1",
			PageSize:        101,
			UploadsPage:     0,
			GenerationsPage: 2,
			DownloadsPage:   -1,
			From:            &from,
			To:              &to,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if repository.query.UserID != "user-1" ||
		repository.query.PageSize != 100 ||
		repository.query.UploadsPage != 1 ||
		repository.query.GenerationsPage != 2 ||
		repository.query.DownloadsPage != 1 ||
		repository.query.From != &from ||
		repository.query.To != &to ||
		len(repository.query.DownloadActions) != 2 ||
		repository.query.DownloadActions[0] != "download-event" ||
		repository.query.DownloadActions[1] != "access-event" {
		t.Fatalf("normalized query = %+v", repository.query)
	}
	if overview.Uploads.Page != 1 ||
		overview.Generations.Page != 2 ||
		overview.Downloads.Page != 1 ||
		overview.Uploads.PageSize != 100 ||
		overview.Generations.PageSize != 100 ||
		overview.Downloads.PageSize != 100 {
		t.Fatalf("page metadata = %+v", overview)
	}
	if overview.Uploads.Data == nil ||
		overview.Generations.Data == nil ||
		overview.Downloads.Data == nil {
		t.Fatal("public page data must serialize as arrays, not null")
	}
	generation := overview.Generations.Data[0]
	if len(generation.ResultURLs) != 2 ||
		generation.ResultURLs[0] !=
			"/custom/tasks/task%20%2F%201/assets/image%20%2F%201" ||
		generation.ThumbnailURL !=
			"/custom/tasks/task%20%2F%201/assets/thumb%20%2F%201" {
		t.Fatalf("presented generation = %+v", generation)
	}
}

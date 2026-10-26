package administration

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestGenerationServiceNormalizesQueryAndBuildsNativeAssetURLs(
	t *testing.T,
) {
	from := time.Date(
		2026,
		time.July,
		20,
		8,
		0,
		0,
		0,
		time.FixedZone("UTC+8", 8*60*60),
	)
	to := from.Add(24 * time.Hour)
	repository := &memoryGenerationRepository{
		records: []GenerationRecord{{
			ID:       "task-1",
			UserID:   " user-1 ",
			TaskType: "text-to-image",
			TaskID:   "task-1",
			Params:   json.RawMessage(`{"prompt":"cat"}`),
			Assets: []GenerationAsset{
				{ID: "image-1", Role: "image", MediaType: "image/png"},
				{
					ID:        "thumbnail-1",
					Role:      "thumbnail",
					MediaType: "image/png",
				},
			},
		}},
		total: 1,
	}
	service, err := NewGenerationService(GenerationServiceConfig{
		Repository:         repository,
		DefaultPage:        1,
		DefaultPageSize:    50,
		MaximumPageSize:    100,
		NativeAssetRoute:   "/api/v1/tasks/{taskID}/assets/{assetID}",
		ThumbnailAssetRole: "thumbnail",
	})
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.QueryGenerations(context.Background(), GenerationQuery{
		Page:     0,
		PageSize: 1000,
		UserID:   " user-1 ",
		TaskType: " text-to-image ",
		Status:   " SUCCEEDED ",
		From:     &from,
		To:       &to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.query.Page != 1 || repository.query.PageSize != 100 ||
		repository.query.UserID != "user-1" ||
		repository.query.TaskType != "text-to-image" ||
		repository.query.Status != "SUCCEEDED" {
		t.Fatalf("normalized query = %+v", repository.query)
	}
	if repository.query.From == nil ||
		repository.query.From.Location() != time.UTC ||
		repository.query.To == nil ||
		repository.query.To.Location() != time.UTC {
		t.Fatalf("query times are not UTC: %+v", repository.query)
	}
	wantURLs := []string{
		"/api/v1/tasks/task-1/assets/image-1",
		"/api/v1/tasks/task-1/assets/thumbnail-1",
	}
	if !reflect.DeepEqual(page.Records[0].ResultURLs, wantURLs) {
		t.Fatalf(
			"native result URLs = %#v, want %#v",
			page.Records[0].ResultURLs,
			wantURLs,
		)
	}
	if page.Records[0].ThumbnailURL != wantURLs[1] {
		t.Fatalf(
			"native thumbnail URL = %q",
			page.Records[0].ThumbnailURL,
		)
	}
}

func TestGenerationServiceReturnsDetailWithoutTransportKnowledge(
	t *testing.T,
) {
	repository := &memoryGenerationRepository{
		detailFound: true,
		detail: GenerationDetail{
			Ledger: GenerationRecord{
				ID:       "task-2",
				TaskID:   "task-2",
				TaskType: "text-to-image",
				Assets: []GenerationAsset{{
					ID:        "image-2",
					Role:      "image",
					MediaType: "image/png",
				}},
			},
			TaskCache: &GenerationCache{
				TaskID:     "task-2",
				TaskType:   "text-to-image",
				Status:     "SUCCEEDED",
				ProviderID: "openai",
				Data:       json.RawMessage(`{"configured":true}`),
			},
		},
	}
	service, err := NewGenerationService(GenerationServiceConfig{
		Repository:         repository,
		DefaultPage:        1,
		DefaultPageSize:    50,
		MaximumPageSize:    100,
		NativeAssetRoute:   "/tasks/{taskID}/assets/{assetID}",
		ThumbnailAssetRole: "thumbnail",
	})
	if err != nil {
		t.Fatal(err)
	}

	detail, found, err := service.GetGeneration(
		context.Background(),
		" task-2 ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found || repository.taskID != "task-2" {
		t.Fatalf("found=%v task id=%q", found, repository.taskID)
	}
	if got := detail.Ledger.ResultURLs; !reflect.DeepEqual(
		got,
		[]string{"/tasks/task-2/assets/image-2"},
	) {
		t.Fatalf("detail result URLs = %#v", got)
	}
	if string(detail.TaskCache.Data) != `{"configured":true}` {
		t.Fatalf("cache data = %s", detail.TaskCache.Data)
	}

	repository.detail.TaskCache.Data = json.RawMessage(`[]`)
	detail, found, err = service.GetGeneration(
		context.Background(),
		"task-2",
	)
	if err != nil || !found {
		t.Fatalf("normalize cache detail found=%v err=%v", found, err)
	}
	if string(detail.TaskCache.Data) != `{}` {
		t.Fatalf(
			"non-object cache data was not normalized: %s",
			detail.TaskCache.Data,
		)
	}
}

func TestGenerationServiceBuildsThumbnailFromTheMatchingNativeAsset(
	t *testing.T,
) {
	repository := &memoryGenerationRepository{
		records: []GenerationRecord{{
			ID:         "task-3",
			TaskID:     "task-3",
			TaskType:   "text-to-image",
			ResultURLs: []string{"/provider/original.png"},
			Assets: []GenerationAsset{{
				ID:        "thumbnail-3",
				Role:      "thumbnail",
				MediaType: "image/png",
			}},
		}},
		total: 1,
	}
	service, err := NewGenerationService(GenerationServiceConfig{
		Repository:         repository,
		DefaultPage:        1,
		DefaultPageSize:    50,
		MaximumPageSize:    100,
		NativeAssetRoute:   "/tasks/{taskID}/assets/{assetID}",
		ThumbnailAssetRole: "thumbnail",
	})
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.QueryGenerations(
		context.Background(),
		GenerationQuery{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if page.Records[0].ThumbnailURL !=
		"/tasks/task-3/assets/thumbnail-3" {
		t.Fatalf(
			"thumbnail URL = %q",
			page.Records[0].ThumbnailURL,
		)
	}
}

func TestGenerationServiceHidesHistoricalProviderLocators(t *testing.T) {
	const providerURL = "https://cdn.provider.example/model.glb?token=secret"
	repository := &memoryGenerationRepository{
		records: []GenerationRecord{{
			TaskID:       "historical-task",
			ResultURLs:   []string{providerURL},
			ThumbnailURL: providerURL,
			Params: json.RawMessage(`{"prompt":"cat","source":{"url":"` + providerURL + `"},` +
				`"encoded":"https%3A%2F%2Fcdn.provider.example%2Fprivate",` +
				`"nested_encoded":"https%253A%252F%252Fcdn.provider.example%252Fprivate"}`),
			Assets: []GenerationAsset{{ID: "legacy-1", Role: "model", SourceURL: providerURL}},
		}},
		total: 1,
	}
	service, err := NewGenerationService(GenerationServiceConfig{
		Repository: repository, DefaultPage: 1, DefaultPageSize: 10,
		MaximumPageSize: 10, NativeAssetRoute: "/tasks/{taskID}/assets/{assetID}",
		ThumbnailAssetRole: "thumbnail",
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.QueryGenerations(context.Background(), GenerationQuery{})
	if err != nil {
		t.Fatal(err)
	}
	record := page.Records[0]
	if len(record.ResultURLs) != 0 || record.ThumbnailURL != "" {
		t.Fatalf("historical URL exposed: %#v", record)
	}
	var params map[string]any
	if err := json.Unmarshal(record.Params, &params); err != nil {
		t.Fatal(err)
	}
	source := params["source"].(map[string]any)
	if params["prompt"] != "cat" || source["url"] != "" || params["encoded"] != "" || params["nested_encoded"] != "" {
		t.Fatalf("unsafe public params = %#v", params)
	}
}

type memoryGenerationRepository struct {
	query       GenerationQuery
	records     []GenerationRecord
	total       int
	detail      GenerationDetail
	detailFound bool
	taskID      string
}

func (repository *memoryGenerationRepository) QueryAdministrationGenerations(
	_ context.Context,
	query GenerationQuery,
) ([]GenerationRecord, int, error) {
	repository.query = query
	return repository.records, repository.total, nil
}

func (repository *memoryGenerationRepository) GetAdministrationGeneration(
	_ context.Context,
	taskID string,
) (GenerationDetail, bool, error) {
	repository.taskID = taskID
	return repository.detail, repository.detailFound, nil
}

package legacyassetproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestProviderRefresherUsesSchemaOfferingAndPersistsFreshSignedURLs(t *testing.T) {
	testSignedURLRefresh(t, false)
}
func TestProviderRefresherRebasesConflictWithoutOverwritingConcurrentOpaque(t *testing.T) {
	testSignedURLRefresh(t, true)
}
func testSignedURLRefresh(t *testing.T, conflict bool) {
	t.Helper()
	staleModel := "https://cdn.assets.test/models/a.glb?Signature=old"
	staleThumb := "https://cdn.assets.test/models/a.png?Signature=old"
	freshModel := "https://cdn.assets.test/models/a.glb?Signature=new"
	freshThumb := "https://cdn.assets.test/models/a.png?Signature=new"
	repository := &refresherRepository{
		conflictOnce: conflict, record: LegacyAssetTask{
			TaskID:     "remote-task-1",
			TaskType:   "image-to-3d",
			Status:     "SUCCEEDED",
			ProviderID: "future-provider",
			Data: `{"model_urls":{"glb":"` + staleModel +
				`"},"items":[{"preview":"` + staleThumb + `"}]}`,
		},
		found: true,
	}
	offering := generation.Offering{
		ID:           "future-image-to-3d",
		ProviderID:   "future-provider",
		AdapterKey:   "future.v1",
		Capability:   generation.ImageTo3D,
		Enabled:      true,
		AssetHosts:   []string{"cdn.assets.test"},
		ModelID:      "future-model",
		ProviderName: "Future",
	}
	provider := &refresherProvider{
		observation: generation.Observation{
			Phase: generation.UpstreamCompleted,
			Artifacts: []generation.ArtifactClaim{
				{Locator: freshModel},
				{Locator: freshThumb},
			},
		},
	}
	refresher, err := NewProviderRefresher(ProviderRefresherConfig{
		Repository: repository,
		Catalog: refresherCatalog{
			offerings: []generation.Offering{
				{
					ID:         "other-provider",
					ProviderID: "other",
					Capability: generation.ImageTo3D,
					Enabled:    true,
				},
				offering,
			},
		},
		Providers: refresherRegistry{
			offeringID: offering.ID,
			provider:   provider,
		},
		OfferingMappings: []OfferingMapping{{
			LegacyProviderID: "future-provider",
			Capability:       generation.ImageTo3D,
			OfferingID:       offering.ID,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	refreshed, err := refresher.Refresh(
		context.Background(),
		"remote-task-1",
		staleModel,
	)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed != freshModel {
		t.Fatalf("refreshed URL = %q, want %q", refreshed, freshModel)
	}
	if provider.request.Offering.ID != offering.ID ||
		provider.request.Job.RemoteID != "remote-task-1" ||
		provider.request.Job.ProviderID != "future-provider" ||
		provider.request.TaskID != generation.TaskID("remote-task-1") {
		t.Fatalf("Observe request = %#v", provider.request)
	}
	var persisted map[string]any
	if err := json.Unmarshal(
		[]byte(repository.updatedData),
		&persisted,
	); err != nil {
		t.Fatalf("persisted JSON: %v", err)
	}
	if conflict && (persisted["concurrent_note"] != "preserved" || repository.updates != 2) {
		t.Fatalf("conflict lost concurrent data: %v updates=%d", persisted, repository.updates)
	}
	models := persisted["model_urls"].(map[string]any)
	items := persisted["items"].([]any)
	preview := items[0].(map[string]any)["preview"]
	if models["glb"] != freshModel || preview != freshThumb {
		t.Fatalf("persisted data = %#v", persisted)
	}
}

func TestProviderRefresherUsesNativeAttemptAndSchemaDeclaredLegacyAlias(
	t *testing.T,
) {
	stale := "https://cdn.assets.test/models/a.png?Signature=old"
	fresh := "https://cdn.assets.test/models/a.png?Signature=new"
	repository := &refresherRepository{
		record: LegacyAssetTask{
			TaskID:     "task-native",
			TaskType:   "image-to-3d",
			Status:     "SUCCEEDED",
			ProviderID: "Tripo/Tripo-H3.1",
			Data:       `{"thumbnail_url":"` + stale + `"}`,
			AttemptID:  "attempt-native",
			Offering: generation.Offering{
				ID:         "legacy/Tripo/Tripo-H3.1/image-to-3d",
				Capability: generation.ImageTo3D,
			},
			UpstreamJob: &generation.UpstreamJob{
				ProviderID: "Tripo/Tripo-H3.1",
				RemoteID:   "remote-native",
			},
		},
		found: true,
	}
	offering := generation.Offering{
		ID:         "tripo/tripo-v3.1/image-to-3d",
		ProviderID: "tripo",
		ModelID:    "tripo-v3.1",
		AdapterKey: "tripo.v3",
		Capability: generation.ImageTo3D,
		Enabled:    true,
	}
	provider := &refresherProvider{
		observation: generation.Observation{
			Phase: generation.UpstreamCompleted,
			Artifacts: []generation.ArtifactClaim{{
				Locator: fresh,
			}},
		},
	}
	refresher, err := NewProviderRefresher(ProviderRefresherConfig{
		Repository: repository,
		Catalog: refresherCatalog{
			offerings: []generation.Offering{offering},
		},
		Providers: refresherRegistry{
			offeringID: offering.ID,
			provider:   provider,
		},
		OfferingMappings: []OfferingMapping{{
			LegacyProviderID: "Tripo/Tripo-H3.1",
			Capability:       generation.ImageTo3D,
			OfferingID:       offering.ID,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	refreshed, err := refresher.Refresh(
		context.Background(),
		"task-native",
		stale,
	)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed != fresh {
		t.Fatalf("refreshed URL = %q, want %q", refreshed, fresh)
	}
	if provider.request.Offering.ID != offering.ID ||
		provider.request.AttemptID != "attempt-native" ||
		provider.request.Job.RemoteID != "remote-native" ||
		provider.request.Job.ProviderID != offering.ProviderID {
		t.Fatalf("Observe request = %#v", provider.request)
	}
}

func TestProviderRefresherRejectsIncompleteOrUnmatchedRefresh(
	t *testing.T,
) {
	tests := []struct {
		name        string
		observation generation.Observation
		staleURL    string
	}{
		{
			name: "upstream still running",
			observation: generation.Observation{
				Phase: generation.UpstreamRunning,
			},
			staleURL: "https://cdn.assets.test/a.glb?old",
		},
		{
			name: "pathname not returned",
			observation: generation.Observation{
				Phase: generation.UpstreamCompleted,
				Artifacts: []generation.ArtifactClaim{
					{Locator: "https://cdn.assets.test/b.glb?new"},
				},
			},
			staleURL: "https://cdn.assets.test/a.glb?old",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &refresherRepository{
				record: LegacyAssetTask{
					TaskID:     "task-1",
					TaskType:   "text-to-3d",
					ProviderID: "provider-1",
					Data:       `{"url":"` + test.staleURL + `"}`,
				},
				found: true,
			}
			offering := generation.Offering{
				ID:         "offering-1",
				ProviderID: "provider-1",
				AdapterKey: "provider.v1",
				Capability: generation.TextTo3D,
				Enabled:    true,
			}
			refresher, err := NewProviderRefresher(
				ProviderRefresherConfig{
					Repository: repository,
					Catalog: refresherCatalog{
						offerings: []generation.Offering{offering},
					},
					Providers: refresherRegistry{
						offeringID: offering.ID,
						provider: &refresherProvider{
							observation: test.observation,
						},
					},
					OfferingMappings: []OfferingMapping{{
						LegacyProviderID: "provider-1",
						Capability:       generation.TextTo3D,
						OfferingID:       offering.ID,
					}},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := refresher.Refresh(
				context.Background(),
				"task-1",
				test.staleURL,
			); err == nil {
				t.Fatal("Refresh error = nil")
			}
			if repository.updatedData != "" {
				t.Fatalf(
					"unexpected persisted data %q",
					repository.updatedData,
				)
			}
		})
	}
}

func TestProviderRefresherDoesNotHardcodeProviderOrCapability(t *testing.T) {
	repository := &refresherRepository{
		record: LegacyAssetTask{
			TaskID:     "task-1",
			TaskType:   "future-capability",
			ProviderID: "future-provider",
			Data:       `{}`,
		},
		found: true,
	}
	refresher, err := NewProviderRefresher(ProviderRefresherConfig{
		Repository: repository,
		Catalog:    refresherCatalog{},
		Providers:  refresherRegistry{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = refresher.Refresh(
		context.Background(),
		"task-1",
		"https://cdn.assets.test/a.bin",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "capability") {
		t.Fatalf("Refresh error = %v", err)
	}
}

type refresherRepository struct {
	record       LegacyAssetTask
	found        bool
	err          error
	updatedData  string
	conflictOnce bool
	updates      int
}

func (repository *refresherRepository) GetLegacyAssetTask(
	context.Context,
	string,
) (LegacyAssetTask, bool, error) {
	return repository.record, repository.found, repository.err
}

func (repository *refresherRepository) UpdateLegacyAssetTaskData(
	_ context.Context,
	_ LegacyAssetTask,
	data string,
) error {
	repository.updates++
	if repository.conflictOnce && repository.updates == 1 {
		repository.record.Version++
		var values map[string]any
		if err := json.Unmarshal([]byte(repository.record.Data), &values); err != nil {
			return err
		}
		values["concurrent_note"] = "preserved"
		encoded, _ := json.Marshal(values)
		repository.record.Data = string(encoded)
		return generation.RepositoryConflict
	}
	repository.updatedData = data
	return repository.err
}

type refresherCatalog struct {
	offerings []generation.Offering
}

func (catalog refresherCatalog) Get(
	id generation.OfferingID,
) (generation.Offering, bool) {
	for _, offering := range catalog.offerings {
		if offering.ID == id {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

type refresherRegistry struct {
	offeringID generation.OfferingID
	provider   generation.ProviderPort
}

func (registry refresherRegistry) Provider(
	offering generation.Offering,
) (generation.ProviderPort, bool) {
	if offering.ID != registry.offeringID || registry.provider == nil {
		return nil, false
	}
	return registry.provider, true
}

type refresherProvider struct {
	request     generation.ObserveRequest
	observation generation.Observation
	err         error
}

func (*refresherProvider) Start(
	context.Context,
	generation.StartRequest,
) (generation.Observation, error) {
	return generation.Observation{}, errors.New("unexpected Start")
}

func (provider *refresherProvider) Observe(
	_ context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	provider.request = request
	return provider.observation, provider.err
}

func (*refresherProvider) OpenAsset(
	context.Context,
	generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	return nil, generation.AssetMetadata{}, errors.New(
		"unexpected OpenAsset",
	)
}

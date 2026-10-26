package legacyassetproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type LegacyAssetTask struct {
	Version     int64
	TaskID      string
	TaskType    string
	Status      string
	ProviderID  string
	Data        string
	AttemptID   generation.AttemptID
	Offering    generation.Offering
	UpstreamJob *generation.UpstreamJob
}

type LegacyAssetTaskRepository interface {
	GetLegacyAssetTask(
		context.Context,
		string,
	) (LegacyAssetTask, bool, error)
	UpdateLegacyAssetTaskData(
		context.Context,
		LegacyAssetTask,
		string,
	) error
}

type OfferingCatalog interface {
	Get(generation.OfferingID) (generation.Offering, bool)
}

type OfferingMapping struct {
	LegacyProviderID string
	Capability       generation.Capability
	OfferingID       generation.OfferingID
}

type ProviderRefresherConfig struct {
	Repository       LegacyAssetTaskRepository
	Catalog          OfferingCatalog
	Providers        generation.ProviderRegistry
	OfferingMappings []OfferingMapping
}

type ProviderRefresher struct {
	repository      LegacyAssetTaskRepository
	providers       generation.ProviderRegistry
	legacyOfferings map[legacyOfferingKey]generation.Offering
}

type legacyOfferingKey struct {
	providerID string
	capability generation.Capability
}

func NewProviderRefresher(
	config ProviderRefresherConfig,
) (*ProviderRefresher, error) {
	if config.Repository == nil ||
		config.Catalog == nil ||
		config.Providers == nil {
		return nil, errors.New(
			"legacyassetproxy: incomplete refresher dependencies",
		)
	}
	legacyOfferings := make(
		map[legacyOfferingKey]generation.Offering,
		len(config.OfferingMappings),
	)
	for index, mapping := range config.OfferingMappings {
		key := legacyOfferingKey{
			providerID: strings.TrimSpace(mapping.LegacyProviderID),
			capability: mapping.Capability,
		}
		if key.providerID == "" ||
			!key.capability.Valid() ||
			strings.TrimSpace(string(mapping.OfferingID)) == "" {
			return nil, fmt.Errorf(
				"legacyassetproxy: invalid offering mapping %d",
				index,
			)
		}
		if _, exists := legacyOfferings[key]; exists {
			return nil, fmt.Errorf(
				"legacyassetproxy: duplicate offering mapping for %q/%q",
				key.providerID,
				key.capability,
			)
		}
		offering, found := catalogOffering(
			config.Catalog,
			key.capability,
			mapping.OfferingID,
		)
		// Historical tasks keep their original Offering identity after the
		// Offering is disabled for new submissions.
		if !found {
			return nil, fmt.Errorf(
				"legacyassetproxy: offering mapping %q/%q references unavailable offering %q",
				key.providerID,
				key.capability,
				mapping.OfferingID,
			)
		}
		legacyOfferings[key] = offering
	}
	return &ProviderRefresher{
		repository:      config.Repository,
		providers:       config.Providers,
		legacyOfferings: legacyOfferings,
	}, nil
}

func (refresher *ProviderRefresher) Refresh(
	ctx context.Context,
	taskID string,
	staleURL string,
) (string, error) {
	record, found, err := refresher.repository.GetLegacyAssetTask(
		ctx,
		strings.TrimSpace(taskID),
	)
	if err != nil {
		return "", fmt.Errorf(
			"legacyassetproxy: read legacy task: %w",
			err,
		)
	}
	if !found {
		return "", errors.New("legacyassetproxy: legacy task not found")
	}
	capability := generation.Capability(
		strings.TrimSpace(record.TaskType),
	)
	if !capability.Valid() {
		return "", fmt.Errorf(
			"legacyassetproxy: unsupported legacy capability %q",
			record.TaskType,
		)
	}
	offering, provider, found := refresher.providerFor(
		capability,
		strings.TrimSpace(record.ProviderID),
		record.Offering,
	)
	if !found {
		return "", fmt.Errorf(
			"legacyassetproxy: provider %q has no runtime adapter for capability %q",
			record.ProviderID,
			capability,
		)
	}
	localTaskID := strings.TrimSpace(record.TaskID)
	if localTaskID == "" {
		localTaskID = strings.TrimSpace(taskID)
	}
	remoteID := localTaskID
	if record.UpstreamJob != nil &&
		strings.TrimSpace(record.UpstreamJob.RemoteID) != "" {
		remoteID = strings.TrimSpace(record.UpstreamJob.RemoteID)
	}
	attemptID := record.AttemptID
	if strings.TrimSpace(string(attemptID)) == "" {
		attemptID = generation.AttemptID(
			"legacy-asset-refresh:" + localTaskID,
		)
	}
	observation, err := provider.Observe(
		ctx,
		generation.ObserveRequest{
			TaskID:    generation.TaskID(localTaskID),
			AttemptID: attemptID,
			Offering:  offering,
			Job: generation.UpstreamJob{
				ProviderID: offering.ProviderID,
				RemoteID:   remoteID,
			},
			Opaque: json.RawMessage(record.Data),
		},
	)
	if err != nil {
		return "", fmt.Errorf(
			"legacyassetproxy: observe provider task: %w",
			err,
		)
	}
	if observation.Phase != generation.UpstreamCompleted {
		return "", fmt.Errorf(
			"legacyassetproxy: provider refresh incomplete: %s",
			observation.Phase,
		)
	}
	freshByPath := artifactURLsByPath(observation.Artifacts)
	stalePath, valid := absoluteURLPath(staleURL)
	if !valid {
		return "", errors.New(
			"legacyassetproxy: stale asset URL is invalid",
		)
	}
	refreshedURL := freshByPath[stalePath]
	if refreshedURL == "" {
		return "", errors.New(
			"legacyassetproxy: refreshed assets do not contain source pathname",
		)
	}
	updatedData, err := replaceSignedURLs(
		record.Data,
		freshByPath,
	)
	if err != nil {
		return "", fmt.Errorf(
			"legacyassetproxy: update legacy task data: %w",
			err,
		)
	}
	err = refresher.repository.UpdateLegacyAssetTaskData(ctx, record, updatedData)
	if errors.Is(err, generation.RepositoryConflict) {
		latest, found, loadErr := refresher.repository.GetLegacyAssetTask(ctx, localTaskID)
		if loadErr != nil {
			return "", loadErr
		}
		if !found || latest.AttemptID != record.AttemptID {
			return "", generation.RepositoryConflict
		}
		updatedData, err = replaceSignedURLs(latest.Data, freshByPath)
		if err == nil {
			err = refresher.repository.UpdateLegacyAssetTaskData(ctx, latest, updatedData)
		}
	}
	if err != nil {
		return "", fmt.Errorf("legacyassetproxy: persist legacy task data: %w", err)
	}

	return refreshedURL, nil
}

func (refresher *ProviderRefresher) providerFor(
	capability generation.Capability,
	providerID string,
	stored generation.Offering,
) (
	generation.Offering,
	generation.ProviderPort,
	bool,
) {
	if stored.Capability == capability &&
		strings.TrimSpace(stored.AdapterKey) != "" {
		provider, available := refresher.providers.Provider(stored)
		if available {
			return stored, provider, true
		}
	}
	offering, exists := refresher.legacyOfferings[legacyOfferingKey{
		providerID: strings.TrimSpace(providerID),
		capability: capability,
	}]
	if !exists {
		return generation.Offering{}, nil, false
	}
	provider, available := refresher.providers.Provider(offering)
	if available {
		return offering, provider, true
	}
	return generation.Offering{}, nil, false
}

func catalogOffering(
	catalog OfferingCatalog,
	capability generation.Capability,
	offeringID generation.OfferingID,
) (generation.Offering, bool) {
	offering, found := catalog.Get(offeringID)
	if found && offering.Capability == capability {
		return offering, true
	}
	return generation.Offering{}, false
}

func artifactURLsByPath(
	artifacts []generation.ArtifactClaim,
) map[string]string {
	result := make(map[string]string)
	for _, artifact := range artifacts {
		addURLByPath(result, artifact.Locator)
		if len(artifact.Metadata) == 0 {
			continue
		}
		var metadata any
		if json.Unmarshal(artifact.Metadata, &metadata) == nil {
			collectURLsByPath(result, metadata)
		}
	}
	return result
}

func collectURLsByPath(target map[string]string, value any) {
	switch typed := value.(type) {
	case string:
		addURLByPath(target, typed)
	case []any:
		for _, item := range typed {
			collectURLsByPath(target, item)
		}
	case map[string]any:
		for _, item := range typed {
			collectURLsByPath(target, item)
		}
	}
}

func addURLByPath(target map[string]string, value string) {
	path, valid := absoluteURLPath(value)
	if !valid {
		return
	}
	if _, exists := target[path]; !exists {
		target[path] = value
	}
}

func absoluteURLPath(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		parsed.Hostname() == "" ||
		parsed.Path == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	return parsed.EscapedPath(), true
}

func replaceSignedURLs(
	raw string,
	freshByPath map[string]string,
) (string, error) {
	var document any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return "", err
	}
	document = replaceURLValues(document, freshByPath)
	updated, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	return string(updated), nil
}

func replaceURLValues(
	value any,
	freshByPath map[string]string,
) any {
	switch typed := value.(type) {
	case string:
		path, valid := absoluteURLPath(typed)
		if valid {
			if refreshed := freshByPath[path]; refreshed != "" {
				return refreshed
			}
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = replaceURLValues(item, freshByPath)
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = replaceURLValues(item, freshByPath)
		}
		return typed
	default:
		return value
	}
}

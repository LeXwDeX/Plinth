package administration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrBackfillStatusFailed = errors.New("backfill legacy status failed")

type LegacyPendingUsage struct {
	ID         string
	TaskID     string
	ProviderID string
	Status     string
}

type LegacyTaskCache struct {
	Status string
	Data   string
}

type LegacyUsageStatusUpdate struct {
	ID              string
	Status          string
	FinishedAt      time.Time
	ResultURLs      []string
	WriteResultURLs bool
}

type BackfillStatusRepository interface {
	ListPendingLegacyUsage(
		context.Context,
		string,
	) ([]LegacyPendingUsage, error)
	GetLegacyTaskCache(
		context.Context,
		string,
	) (LegacyTaskCache, bool, error)
	UpdateLegacyUsageStatus(
		context.Context,
		LegacyUsageStatusUpdate,
	) error
}

type BackfillResultFields struct {
	ModelURLs    string
	ThumbnailURL string
	ImageURLs    string
	TextureURLs  string
	BaseColor    string
	VideoURL     string
}

type BackfillStatusModuleConfig struct {
	Repository      BackfillStatusRepository
	PendingStatus   string
	SucceededStatus string
	SyncProviderIDs []string
	ResultFields    BackfillResultFields
	Now             func() time.Time
}

type BackfillStatusResult struct {
	Total      int
	SyncFixed  int
	AsyncFixed int
	Unchanged  int
}

type BackfillStatusModule struct {
	repository      BackfillStatusRepository
	pendingStatus   string
	succeededStatus string
	syncProviders   map[string]struct{}
	resultFields    BackfillResultFields
	now             func() time.Time
}

func NewBackfillStatusModule(
	config BackfillStatusModuleConfig,
) (*BackfillStatusModule, error) {
	if config.Repository == nil {
		return nil, errors.New("backfill status repository 不能为空")
	}
	pending := strings.TrimSpace(config.PendingStatus)
	succeeded := strings.TrimSpace(config.SucceededStatus)
	if pending == "" || succeeded == "" || pending == succeeded {
		return nil, errors.New("backfill status states 配置无效")
	}
	if len(config.SyncProviderIDs) == 0 {
		return nil, errors.New("backfill status sync providers 不能为空")
	}
	syncProviders := make(map[string]struct{}, len(config.SyncProviderIDs))
	for _, providerID := range config.SyncProviderIDs {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" {
			return nil, errors.New("backfill status sync provider 不能为空")
		}
		if _, exists := syncProviders[providerID]; exists {
			return nil, fmt.Errorf(
				"backfill status sync provider 重复: %q",
				providerID,
			)
		}
		syncProviders[providerID] = struct{}{}
	}
	resultFields := map[string]string{
		"model_urls":    config.ResultFields.ModelURLs,
		"thumbnail_url": config.ResultFields.ThumbnailURL,
		"image_urls":    config.ResultFields.ImageURLs,
		"texture_urls":  config.ResultFields.TextureURLs,
		"base_color":    config.ResultFields.BaseColor,
		"video_url":     config.ResultFields.VideoURL,
	}
	seenFields := make(map[string]struct{}, len(resultFields))
	for name, field := range resultFields {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("backfill result field %s 不能为空", name)
		}
		if _, exists := seenFields[field]; exists {
			return nil, fmt.Errorf("backfill result field 重复: %q", field)
		}
		seenFields[field] = struct{}{}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &BackfillStatusModule{
		repository:      config.Repository,
		pendingStatus:   pending,
		succeededStatus: succeeded,
		syncProviders:   syncProviders,
		resultFields:    config.ResultFields,
		now:             now,
	}, nil
}

func (m *BackfillStatusModule) Backfill(
	ctx context.Context,
) (BackfillStatusResult, error) {
	records, err := m.repository.ListPendingLegacyUsage(
		ctx,
		m.pendingStatus,
	)
	if err != nil {
		return BackfillStatusResult{}, fmt.Errorf(
			"%w: list pending usage: %v",
			ErrBackfillStatusFailed,
			err,
		)
	}
	result := BackfillStatusResult{Total: len(records)}
	for _, record := range records {
		if _, synchronous := m.syncProviders[record.ProviderID]; synchronous {
			if err := m.repository.UpdateLegacyUsageStatus(
				ctx,
				LegacyUsageStatusUpdate{
					ID:         record.ID,
					Status:     m.succeededStatus,
					FinishedAt: m.now(),
				},
			); err != nil {
				return BackfillStatusResult{}, fmt.Errorf(
					"%w: update sync usage %q: %v",
					ErrBackfillStatusFailed,
					record.ID,
					err,
				)
			}
			result.SyncFixed++
			continue
		}
		cache, found, err := m.repository.GetLegacyTaskCache(
			ctx,
			record.TaskID,
		)
		if err != nil {
			return BackfillStatusResult{}, fmt.Errorf(
				"%w: get task cache %q: %v",
				ErrBackfillStatusFailed,
				record.TaskID,
				err,
			)
		}
		if !found || strings.TrimSpace(cache.Status) == "" ||
			cache.Status == m.pendingStatus {
			result.Unchanged++
			continue
		}
		data, err := decodeBackfillTaskData(cache.Data)
		if err != nil {
			return BackfillStatusResult{}, fmt.Errorf(
				"%w: decode task cache %q: %v",
				ErrBackfillStatusFailed,
				record.TaskID,
				err,
			)
		}
		update := LegacyUsageStatusUpdate{
			ID:         record.ID,
			Status:     cache.Status,
			FinishedAt: m.now(),
		}
		if cache.Status == m.succeededStatus {
			update.ResultURLs, err = extractBackfillResultURLs(
				data,
				m.resultFields,
			)
			if err != nil {
				return BackfillStatusResult{}, fmt.Errorf(
					"%w: extract task cache %q: %v",
					ErrBackfillStatusFailed,
					record.TaskID,
					err,
				)
			}
			update.WriteResultURLs = len(update.ResultURLs) > 0
		}
		if err := m.repository.UpdateLegacyUsageStatus(ctx, update); err != nil {
			return BackfillStatusResult{}, fmt.Errorf(
				"%w: update async usage %q: %v",
				ErrBackfillStatusFailed,
				record.ID,
				err,
			)
		}
		result.AsyncFixed++
	}
	return result, nil
}

func decodeBackfillTaskData(data string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(data) == "" {
		data = "{}"
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = map[string]json.RawMessage{}
	}
	return result, nil
}

func extractBackfillResultURLs(
	data map[string]json.RawMessage,
	fields BackfillResultFields,
) ([]string, error) {
	result := make([]string, 0)
	if raw := data[fields.ModelURLs]; len(raw) > 0 {
		values, err := orderedStringValues(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
	}
	if value, err := decodedNonemptyString(data[fields.ThumbnailURL]); err != nil {
		return nil, err
	} else if value != "" {
		result = append(result, value)
	}
	if raw := data[fields.ImageURLs]; len(raw) > 0 {
		values, err := orderedStringArray(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
	}
	if raw := data[fields.TextureURLs]; len(raw) > 0 {
		values, err := orderedTextureValues(raw, fields.BaseColor)
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
	}
	if value, err := decodedNonemptyString(data[fields.VideoURL]); err != nil {
		return nil, err
	} else if value != "" {
		result = append(result, value)
	}
	return result, nil
}

func orderedStringValues(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '[' {
		return orderedStringArray(trimmed)
	}
	if trimmed[0] != '{' {
		return nil, errors.New("model urls 必须为数组或对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	result := make([]string, 0)
	for decoder.More() {
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		decoded, err := decodedNonemptyString(value)
		if err != nil {
			return nil, err
		}
		if decoded != "" {
			result = append(result, decoded)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return result, nil
}

func orderedStringArray(raw json.RawMessage) ([]string, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(values))
	for _, rawValue := range values {
		value, err := decodedNonemptyString(rawValue)
		if err != nil {
			return nil, err
		}
		if value != "" {
			result = append(result, value)
		}
	}
	return result, nil
}

func orderedTextureValues(
	raw json.RawMessage,
	baseColorField string,
) ([]string, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(values))
	for _, rawValue := range values {
		if value, err := decodedNonemptyString(rawValue); err == nil {
			if value != "" {
				result = append(result, value)
			}
			continue
		}
		var texture map[string]json.RawMessage
		if err := json.Unmarshal(rawValue, &texture); err != nil {
			return nil, err
		}
		value, err := decodedNonemptyString(texture[baseColorField])
		if err != nil {
			return nil, err
		}
		if value != "" {
			result = append(result, value)
		}
	}
	return result, nil
}

func decodedNonemptyString(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 ||
		bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

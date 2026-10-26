package administration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type GenerationQuery struct {
	Page     int
	PageSize int
	UserID   string
	TaskType string
	Status   string
	From     *time.Time
	To       *time.Time
}

type GenerationAsset struct {
	ID                string
	Role              string
	Variant           string
	MediaType         string
	Ordinal           int
	SourceURL         string
	PreferredFilename string
}

// GenerationOffering names the Offering a task was frozen for. Fee item labels
// of its frozen quote are read through the current declaration of it.
type GenerationOffering struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	Capability string `json:"capability"`
}

type GenerationRecord struct {
	NativeQuote      *schema.NativeQuote
	Offering         GenerationOffering
	ID               string
	UserID           string
	UserName         string
	UserEmail        string
	TaskType         string
	TaskID           string
	EstimatedCredits int
	TaskStatus       string
	Params           json.RawMessage
	ResultURLs       []string
	ThumbnailURL     string
	CreatedAt        time.Time
	FinishedAt       *time.Time
	Assets           []GenerationAsset
}

type GenerationCache struct {
	TaskID     string
	TaskType   string
	Status     string
	ProviderID string
	Data       json.RawMessage
	CachedAt   time.Time
	UpdatedAt  time.Time
}

type GenerationDetail struct {
	Ledger    GenerationRecord
	TaskCache *GenerationCache
}

type GenerationPage struct {
	Records  []GenerationRecord
	Total    int
	Page     int
	PageSize int
}

type GenerationRepository interface {
	QueryAdministrationGenerations(
		context.Context,
		GenerationQuery,
	) ([]GenerationRecord, int, error)
	GetAdministrationGeneration(
		context.Context,
		string,
	) (GenerationDetail, bool, error)
}

type LegacyGenerationAssetProjector interface {
	ProjectLegacyGenerationAssets(
		string,
		json.RawMessage,
		[]string,
	) []GenerationAsset
}

type GenerationServiceConfig struct {
	Repository           GenerationRepository
	DefaultPage          int
	DefaultPageSize      int
	MaximumPageSize      int
	NativeAssetRoute     string
	ThumbnailAssetRole   string
	LegacyAssetProjector LegacyGenerationAssetProjector
}

type GenerationService struct {
	repository           GenerationRepository
	defaultPage          int
	defaultPageSize      int
	maximumPageSize      int
	nativeAssetRoute     string
	taskIDToken          string
	assetIDToken         string
	thumbnailAssetRole   string
	legacyAssetProjector LegacyGenerationAssetProjector
}

func NewGenerationService(
	config GenerationServiceConfig,
) (*GenerationService, error) {
	if config.Repository == nil {
		return nil, errors.New(
			"administration: 缺少 GenerationRepository",
		)
	}
	if config.DefaultPage <= 0 ||
		config.DefaultPageSize <= 0 ||
		config.MaximumPageSize < config.DefaultPageSize {
		return nil, errors.New(
			"administration: generation 分页配置无效",
		)
	}
	for name, value := range map[string]string{
		"native asset route":   config.NativeAssetRoute,
		"thumbnail asset role": config.ThumbnailAssetRole,
	} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf(
				"administration: %s 不能为空",
				name,
			)
		}
	}
	parameters := generationRouteParameterPattern.FindAllString(
		config.NativeAssetRoute,
		-1,
	)
	if len(parameters) != 2 {
		return nil, errors.New(
			"administration: native asset route 必须声明任务与资产参数",
		)
	}
	return &GenerationService{
		repository:           config.Repository,
		defaultPage:          config.DefaultPage,
		defaultPageSize:      config.DefaultPageSize,
		maximumPageSize:      config.MaximumPageSize,
		nativeAssetRoute:     config.NativeAssetRoute,
		taskIDToken:          parameters[0],
		assetIDToken:         parameters[1],
		thumbnailAssetRole:   config.ThumbnailAssetRole,
		legacyAssetProjector: config.LegacyAssetProjector,
	}, nil
}

var generationRouteParameterPattern = regexp.MustCompile(`\{[^{}]+\}`)

func (service *GenerationService) QueryGenerations(
	ctx context.Context,
	query GenerationQuery,
) (GenerationPage, error) {
	query = service.normalizeQuery(query)
	records, total, err := service.repository.
		QueryAdministrationGenerations(ctx, query)
	if err != nil {
		return GenerationPage{}, fmt.Errorf(
			"administration: query generations: %w",
			err,
		)
	}
	if records == nil {
		records = []GenerationRecord{}
	}
	for index := range records {
		records[index] = service.presentRecord(records[index])
	}
	return GenerationPage{
		Records:  records,
		Total:    total,
		Page:     query.Page,
		PageSize: query.PageSize,
	}, nil
}

func (service *GenerationService) GetGeneration(
	ctx context.Context,
	taskID string,
) (GenerationDetail, bool, error) {
	detail, found, err := service.getGenerationDetail(ctx, taskID)
	if err != nil || !found {
		return GenerationDetail{}, found, err
	}
	detail.Ledger = service.presentRecord(detail.Ledger)
	return detail, true, nil
}

// GetGenerationForDownload returns private legacy source metadata to the
// server-side download reader. That reader only resolves approved local
// generated files/caches and never turns a browser request into a live
// upstream fetch. Keep this method off the HTTP projection path.
func (service *GenerationService) GetGenerationForDownload(
	ctx context.Context,
	taskID string,
) (GenerationDetail, bool, error) {
	return service.getGenerationDetail(ctx, taskID)
}

func (service *GenerationService) getGenerationDetail(
	ctx context.Context,
	taskID string,
) (GenerationDetail, bool, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return GenerationDetail{}, false, nil
	}
	detail, found, err := service.repository.
		GetAdministrationGeneration(ctx, taskID)
	if err != nil {
		return GenerationDetail{}, false, fmt.Errorf(
			"administration: get generation: %w",
			err,
		)
	}
	if !found {
		return GenerationDetail{}, false, nil
	}
	if len(detail.Ledger.Assets) == 0 &&
		service.legacyAssetProjector != nil {
		providerData := json.RawMessage(`{}`)
		if detail.TaskCache != nil && len(detail.TaskCache.Data) > 0 {
			providerData = detail.TaskCache.Data
		}
		detail.Ledger.Assets = service.legacyAssetProjector.
			ProjectLegacyGenerationAssets(
				detail.Ledger.TaskType,
				providerData,
				detail.Ledger.ResultURLs,
			)
	}
	if detail.TaskCache != nil {
		detail.TaskCache.TaskID = strings.TrimSpace(
			detail.TaskCache.TaskID,
		)
		detail.TaskCache.TaskType = strings.TrimSpace(
			detail.TaskCache.TaskType,
		)
		detail.TaskCache.Status = strings.TrimSpace(
			detail.TaskCache.Status,
		)
		detail.TaskCache.ProviderID = strings.TrimSpace(
			detail.TaskCache.ProviderID,
		)
		detail.TaskCache.Data = normalizedJSONObject(
			detail.TaskCache.Data,
		)
	}
	return detail, true, nil
}

func normalizedJSONObject(value json.RawMessage) json.RawMessage {
	var object map[string]json.RawMessage
	if len(value) == 0 ||
		json.Unmarshal(value, &object) != nil ||
		object == nil {
		return json.RawMessage(`{}`)
	}
	return value
}

func (service *GenerationService) normalizeQuery(
	query GenerationQuery,
) GenerationQuery {
	query.UserID = strings.TrimSpace(query.UserID)
	query.TaskType = strings.TrimSpace(query.TaskType)
	query.Status = strings.TrimSpace(query.Status)
	if query.Page <= 0 {
		query.Page = service.defaultPage
	}
	if query.PageSize <= 0 {
		query.PageSize = service.defaultPageSize
	}
	if query.PageSize > service.maximumPageSize {
		query.PageSize = service.maximumPageSize
	}
	query.From = utcTimePointer(query.From)
	query.To = utcTimePointer(query.To)
	return query
}

func (service *GenerationService) presentRecord(
	record GenerationRecord,
) GenerationRecord {
	return presentPublicGenerationRecord(
		record,
		service.nativeAssetURL,
		service.thumbnailAssetRole,
	)
}

// presentPublicGenerationRecord is the common safe projection used by
// administration generation views. Historical ledger URLs and params are
// untrusted; only locally stored assets without an upstream source locator are
// emitted as downloadable same-origin URLs.
func presentPublicGenerationRecord(
	record GenerationRecord,
	nativeAssetURL func(string, string) string,
	thumbnailAssetRole string,
) GenerationRecord {
	record.ID = strings.TrimSpace(record.ID)
	record.UserID = strings.TrimSpace(record.UserID)
	record.UserName = strings.TrimSpace(record.UserName)
	record.UserEmail = strings.TrimSpace(record.UserEmail)
	record.TaskType = strings.TrimSpace(record.TaskType)
	record.TaskID = strings.TrimSpace(record.TaskID)
	record.TaskStatus = strings.TrimSpace(record.TaskStatus)
	// The ledger may contain historical upstream URLs. Public generation views
	// are built only from assets already stored by this service.
	record.ThumbnailURL = ""
	if len(record.Params) == 0 {
		record.Params = json.RawMessage(`{}`)
	}
	record.Params = publicGenerationParams(record.Params)
	if record.ResultURLs == nil {
		record.ResultURLs = []string{}
	}
	record.ResultURLs = make([]string, 0, len(record.Assets))
	for index := range record.Assets {
		asset := &record.Assets[index]
		hasSourceURL := strings.TrimSpace(asset.SourceURL) != ""
		// SourceURL is private ingestion metadata. It may point at an upstream
		// signed URL (including when an asset is rejected from ResultURLs), so it
		// must never be copied into the browser-facing record.
		asset.SourceURL = ""
		if hasSourceURL || strings.TrimSpace(asset.ID) == "" {
			continue
		}
		assetURL := nativeAssetURL(record.TaskID, asset.ID)
		record.ResultURLs = append(record.ResultURLs, assetURL)
		if asset.Role == thumbnailAssetRole &&
			record.ThumbnailURL == "" {
			record.ThumbnailURL = assetURL
		}
	}
	return record
}

// Ledger parameters can contain historical signed provider URLs. Keep the
// useful non-URL parameters while removing any external locator before a
// generation record is returned to a browser.
func publicGenerationParams(raw json.RawMessage) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(redactGenerationExternalAddresses(value))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func redactGenerationExternalAddresses(value any) any {
	switch current := value.(type) {
	case string:
		if hasGenerationExternalAddress(current) {
			return ""
		}
		return current
	case []any:
		for index, item := range current {
			current[index] = redactGenerationExternalAddresses(item)
		}
		return current
	case map[string]any:
		for key, item := range current {
			if hasGenerationExternalAddress(key) {
				delete(current, key)
				continue
			}
			current[key] = redactGenerationExternalAddresses(item)
		}
		return current
	default:
		return value
	}
}

var generationExternalAddress = regexp.MustCompile(`(?i)(https?://|//[a-z0-9][a-z0-9.-]*\.[a-z]{2,}(?:[:/?#]|$))`)

func hasGenerationExternalAddress(value string) bool {
	for step := 0; step < 4; step++ {
		if generationExternalAddress.MatchString(value) {
			return true
		}
		decoded := html.UnescapeString(value)
		decoded = strings.ReplaceAll(decoded, `\/`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002f`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u002F`, `/`)
		decoded = strings.ReplaceAll(decoded, `\u003a`, `:`)
		decoded = strings.ReplaceAll(decoded, `\u003A`, `:`)
		if unescaped, err := url.QueryUnescape(decoded); err == nil {
			decoded = unescaped
		}
		if decoded == value {
			return false
		}
		value = decoded
	}
	return generationExternalAddress.MatchString(value)
}

func (service *GenerationService) nativeAssetURL(
	taskID string,
	assetID string,
) string {
	path := strings.ReplaceAll(
		service.nativeAssetRoute,
		service.taskIDToken,
		url.PathEscape(strings.TrimSpace(taskID)),
	)
	return strings.ReplaceAll(
		path,
		service.assetIDToken,
		url.PathEscape(strings.TrimSpace(assetID)),
	)
}

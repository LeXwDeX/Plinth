package administration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"net/url"
	"strings"
	"time"
)

var (
	ErrUserAssetsNotFound = errors.New(
		"administration: user assets owner not found",
	)
	ErrUserAssetsFailed = errors.New(
		"administration: user assets query failed",
	)
)

type UserAssetsRequest struct {
	UserID          string
	PageSize        int
	UploadsPage     int
	GenerationsPage int
	DownloadsPage   int
	From            *time.Time
	To              *time.Time
}

type UserAssetsQuery struct {
	UserID          string
	PageSize        int
	UploadsPage     int
	GenerationsPage int
	DownloadsPage   int
	From            *time.Time
	To              *time.Time
	DownloadActions []string
}

type UserAssetUpload struct {
	ID               string
	AssetType        string
	OriginalFilename string
	StoredFilename   string
	MimeType         string
	FileSize         int64
	StoragePath      string
	CreatedAt        time.Time
}

type UserAssetGeneration struct {
	NativeQuote      *schema.NativeQuote
	Offering         GenerationOffering
	ID               string
	TaskType         string
	TaskID           string
	TaskStatus       string
	ResultURLs       []string
	Params           json.RawMessage
	EstimatedCredits int
	ThumbnailURL     string
	CreatedAt        time.Time
	FinishedAt       *time.Time
	Assets           []GenerationAsset
}

type UserAssetDownload struct {
	ID           string
	Action       string
	ResourceType string
	ResourceID   string
	Detail       string
	CreatedAt    time.Time
}

type UserAssetsPage[T any] struct {
	Data     []T
	Total    int
	Page     int
	PageSize int
}

type UserAssetsSummary struct {
	TotalUploads      int
	TotalGenerations  int
	TotalDownloads    int
	TotalStorageBytes int64
}

type UserAssetsOverview struct {
	User        User
	Summary     UserAssetsSummary
	Uploads     UserAssetsPage[UserAssetUpload]
	Generations UserAssetsPage[UserAssetGeneration]
	Downloads   UserAssetsPage[UserAssetDownload]
}

type UserAssetsRepository interface {
	QueryAdministrationUserAssets(
		context.Context,
		UserAssetsQuery,
	) (UserAssetsOverview, bool, error)
}

type UserAssetsModuleConfig struct {
	Repository       UserAssetsRepository
	DefaultPage      int
	DefaultPageSize  int
	MaximumPageSize  int
	DownloadActions  []string
	NativeAssetRoute string
	ThumbnailRole    string
}

type UserAssetsModule struct {
	repository       UserAssetsRepository
	defaultPage      int
	defaultPageSize  int
	maximumPageSize  int
	downloadActions  []string
	nativeAssetRoute string
	taskIDToken      string
	assetIDToken     string
	thumbnailRole    string
}

func NewUserAssetsModule(
	config UserAssetsModuleConfig,
) (*UserAssetsModule, error) {
	if config.Repository == nil {
		return nil, errors.New(
			"administration: user assets repository is missing",
		)
	}
	if config.DefaultPage <= 0 ||
		config.DefaultPageSize <= 0 ||
		config.MaximumPageSize < config.DefaultPageSize ||
		len(config.DownloadActions) == 0 ||
		strings.TrimSpace(config.ThumbnailRole) == "" {
		return nil, errors.New(
			"administration: user assets pagination is invalid",
		)
	}
	parameters := generationRouteParameterPattern.FindAllString(
		config.NativeAssetRoute,
		-1,
	)
	if len(parameters) != 2 {
		return nil, errors.New(
			"administration: user assets native route must declare task and asset parameters",
		)
	}
	actions := make([]string, 0, len(config.DownloadActions))
	actionSet := make(map[string]struct{}, len(config.DownloadActions))
	for _, action := range config.DownloadActions {
		action = strings.TrimSpace(action)
		if action == "" {
			return nil, errors.New(
				"administration: user assets action is empty",
			)
		}
		if _, exists := actionSet[action]; exists {
			return nil, fmt.Errorf(
				"administration: duplicate user assets action %q",
				action,
			)
		}
		actionSet[action] = struct{}{}
		actions = append(actions, action)
	}
	return &UserAssetsModule{
		repository:       config.Repository,
		defaultPage:      config.DefaultPage,
		defaultPageSize:  config.DefaultPageSize,
		maximumPageSize:  config.MaximumPageSize,
		downloadActions:  actions,
		nativeAssetRoute: config.NativeAssetRoute,
		taskIDToken:      parameters[0],
		assetIDToken:     parameters[1],
		thumbnailRole:    config.ThumbnailRole,
	}, nil
}

func (module *UserAssetsModule) QueryUserAssets(
	ctx context.Context,
	request UserAssetsRequest,
) (UserAssetsOverview, error) {
	query := module.normalizeQuery(request)
	overview, found, err := module.repository.
		QueryAdministrationUserAssets(ctx, query)
	if err != nil {
		return UserAssetsOverview{}, fmt.Errorf(
			"%w: %v",
			ErrUserAssetsFailed,
			err,
		)
	}
	if !found {
		return UserAssetsOverview{}, ErrUserAssetsNotFound
	}
	overview.Uploads.Page = query.UploadsPage
	overview.Uploads.PageSize = query.PageSize
	overview.Generations.Page = query.GenerationsPage
	overview.Generations.PageSize = query.PageSize
	overview.Downloads.Page = query.DownloadsPage
	overview.Downloads.PageSize = query.PageSize
	if overview.Uploads.Data == nil {
		overview.Uploads.Data = []UserAssetUpload{}
	}
	if overview.Generations.Data == nil {
		overview.Generations.Data = []UserAssetGeneration{}
	}
	for index := range overview.Generations.Data {
		overview.Generations.Data[index] = module.presentGeneration(
			overview.Generations.Data[index],
		)
	}
	if overview.Downloads.Data == nil {
		overview.Downloads.Data = []UserAssetDownload{}
	}
	return overview, nil
}

func (module *UserAssetsModule) presentGeneration(
	generation UserAssetGeneration,
) UserAssetGeneration {
	projected := presentPublicGenerationRecord(GenerationRecord{
		ID:           generation.ID,
		TaskType:     generation.TaskType,
		TaskID:       generation.TaskID,
		TaskStatus:   generation.TaskStatus,
		Params:       generation.Params,
		ResultURLs:   generation.ResultURLs,
		ThumbnailURL: generation.ThumbnailURL,
		Assets:       generation.Assets,
	}, module.nativeAssetURL, module.thumbnailRole)
	generation.Params = projected.Params
	generation.ResultURLs = projected.ResultURLs
	generation.ThumbnailURL = projected.ThumbnailURL
	return generation
}

func (module *UserAssetsModule) nativeAssetURL(
	taskID string,
	assetID string,
) string {
	path := strings.ReplaceAll(
		module.nativeAssetRoute,
		module.taskIDToken,
		url.PathEscape(strings.TrimSpace(taskID)),
	)
	return strings.ReplaceAll(
		path,
		module.assetIDToken,
		url.PathEscape(strings.TrimSpace(assetID)),
	)
}

func (module *UserAssetsModule) normalizeQuery(
	request UserAssetsRequest,
) UserAssetsQuery {
	pageSize := request.PageSize
	if pageSize <= 0 {
		pageSize = module.defaultPageSize
	}
	if pageSize > module.maximumPageSize {
		pageSize = module.maximumPageSize
	}
	return UserAssetsQuery{
		UserID:          strings.TrimSpace(request.UserID),
		PageSize:        pageSize,
		UploadsPage:     module.normalizePage(request.UploadsPage),
		GenerationsPage: module.normalizePage(request.GenerationsPage),
		DownloadsPage:   module.normalizePage(request.DownloadsPage),
		From:            request.From,
		To:              request.To,
		DownloadActions: append([]string(nil), module.downloadActions...),
	}
}

func (module *UserAssetsModule) normalizePage(page int) int {
	if page <= 0 {
		return module.defaultPage
	}
	return page
}

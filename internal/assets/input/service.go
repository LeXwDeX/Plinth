package input

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

type Service struct {
	uploadValidator    UploadValidator
	repository         generation.InputAssetRepository
	blobs              *local.Store
	publicURL          *url.URL
	signingKey         []byte
	providerAssetRoute routing.One
	now                func() time.Time
	messages           Messages
	fitting            Fitting
}

type ownedInputAssetLister interface {
	ListOwnedInputAssets(
		context.Context,
		generation.OwnerID,
		int,
		int,
	) ([]generation.InputAsset, int, error)
}

type UploadValidator func(context.Context, *os.File, generation.InputAssetUpload) error

type Config struct {
	UploadValidator    UploadValidator
	PublicBaseURL      string
	SigningKey         string
	ProviderAssetRoute string
	Now                func() time.Time
	// Messages and Fitting come from the Product Schema
	// (runtime.input_assets.messages and image_fitting, #276).
	Messages Messages
	Fitting  Fitting
}

func New(
	repository generation.InputAssetRepository,
	blobs *local.Store,
) (*Service, error) {
	return NewWithConfig(repository, blobs, Config{})
}

func NewWithConfig(
	repository generation.InputAssetRepository,
	blobs *local.Store,
	config Config,
) (*Service, error) {
	if repository == nil {
		return nil, errors.New("input-assets: 缺少 Repository")
	}
	if blobs == nil {
		return nil, errors.New("input-assets: 缺少 Blob store")
	}
	service := &Service{
		repository:      repository,
		uploadValidator: config.UploadValidator,
		blobs:           blobs,
		now:             time.Now,
		messages:        config.Messages,
		fitting:         config.Fitting,
	}
	if config.Now != nil {
		service.now = config.Now
	}
	publicBaseURL := strings.TrimSpace(config.PublicBaseURL)
	signingKey := strings.TrimSpace(config.SigningKey)
	if publicBaseURL == "" && signingKey == "" {
		return service, nil
	}
	if publicBaseURL == "" || signingKey == "" {
		return nil, errors.New("input-assets: PublicBaseURL 与 SigningKey 必须同时配置")
	}
	if len(signingKey) < 32 {
		return nil, errors.New("input-assets: SigningKey 至少需要 32 字节")
	}
	parsed, err := url.Parse(strings.TrimRight(publicBaseURL, "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("input-assets: PublicBaseURL 无效")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("input-assets: PublicBaseURL 仅支持 HTTP(S)")
	}
	providerAssetRoute, err := routing.ParseOne(
		config.ProviderAssetRoute,
		"token",
	)
	if err != nil {
		return nil, fmt.Errorf(
			"input-assets: ProviderAssetRoute 无效: %w",
			err,
		)
	}
	service.publicURL = parsed
	service.signingKey = []byte(signingKey)
	service.providerAssetRoute = providerAssetRoute
	return service, nil
}

func (s *Service) Upload(
	ctx context.Context,
	owner generation.OwnerID,
	body io.Reader,
	mediaType string,
	maxBytes int64,
) (generation.InputAsset, error) {
	return s.UploadAsset(ctx, owner, generation.InputAssetUpload{
		Body:      body,
		MediaType: mediaType,
		MaxBytes:  maxBytes,
	})
}

func (s *Service) UploadAsset(
	ctx context.Context,
	owner generation.OwnerID,
	upload generation.InputAssetUpload,
) (generation.InputAsset, error) {
	if owner == "" || upload.Body == nil || upload.MaxBytes <= 0 {
		return generation.InputAsset{}, &generation.Error{
			Code:      generation.ErrInvalidInput,
			Operation: "upload_input_asset",
			Detail:    "上传资产缺少所有者、内容或大小限制",
		}
	}
	var blob local.Blob
	var err, validationErr error
	if s.uploadValidator == nil {
		blob, err = s.blobs.PutBlob(ctx, upload.Body, upload.MediaType, upload.MaxBytes)
	} else {
		blob, err = s.blobs.PutValidatedBlob(ctx, upload.Body, upload.MediaType, upload.MaxBytes, func(file *os.File) error {
			validationErr = s.uploadValidator(ctx, file, upload)
			return validationErr
		})
	}
	if err != nil {
		// Only the trusted startup validator may choose a public domain error.
		// Ordinary storage errors retain the established invalid-input boundary.
		var domainError *generation.Error
		if validationErr != nil && errors.As(validationErr, &domainError) {
			return generation.InputAsset{}, domainError
		}
		return generation.InputAsset{}, &generation.Error{
			Code:      generation.ErrInvalidInput,
			Operation: "upload_input_asset",
			Detail:    "上传文件为空、过大或不可读取",
			Cause:     err,
		}
	}
	asset := generation.InputAsset{
		ID:               randomAssetID(),
		OwnerID:          owner,
		StorageKey:       blob.StorageKey,
		OriginalFilename: upload.OriginalFilename,
		AssetType:        upload.AssetType,
		MediaType:        blob.MediaType,
		Size:             blob.Size,
		SHA256:           blob.SHA256,
		CreatedAt:        s.now().UTC(),
	}
	if err := s.repository.CreateInputAsset(ctx, asset); err != nil {
		return generation.InputAsset{}, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "upload_input_asset",
			Detail:    "上传资产暂不能登记",
			Cause:     err,
		}
	}
	return asset, nil
}

func (s *Service) Resolve(
	ctx context.Context,
	owner generation.OwnerID,
	offering generation.Offering,
	input json.RawMessage,
) (map[string][]generation.InputAsset, error) {
	var values map[string]any
	if err := json.Unmarshal(input, &values); err != nil {
		return nil, invalidReference(err)
	}
	parameterIDs := make(map[string][]generation.AssetID)
	unique := make(map[generation.AssetID]struct{})
	for _, parameter := range offering.Parameters {
		var ids []generation.AssetID
		switch parameter.Type {
		case "asset":
			if raw, exists := values[parameter.Name]; exists {
				text, ok := raw.(string)
				if !ok || strings.TrimSpace(text) == "" {
					return nil, invalidReference(nil)
				}
				ids = []generation.AssetID{generation.AssetID(text)}
			}
		case "asset-array":
			if raw, exists := values[parameter.Name]; exists {
				items, ok := raw.([]any)
				if !ok {
					return nil, invalidReference(nil)
				}
				for _, item := range items {
					text, ok := item.(string)
					if !ok || strings.TrimSpace(text) == "" {
						return nil, invalidReference(nil)
					}
					ids = append(ids, generation.AssetID(text))
				}
			}
		default:
			continue
		}
		parameterIDs[parameter.Name] = ids
		for _, id := range ids {
			unique[id] = struct{}{}
		}
	}
	allIDs := make([]generation.AssetID, 0, len(unique))
	for id := range unique {
		allIDs = append(allIDs, id)
	}
	stored, err := s.repository.LoadInputAssets(ctx, owner, allIDs)
	if err != nil {
		return nil, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "resolve_input_assets",
			Detail:    "输入资产暂不可读取",
			Cause:     err,
		}
	}
	resolved := make(map[string][]generation.InputAsset, len(parameterIDs))
	for parameter, ids := range parameterIDs {
		resolved[parameter] = make([]generation.InputAsset, 0, len(ids))
		schema := parameterDefinition(offering.Parameters, parameter)
		for _, id := range ids {
			asset, exists := stored[id]
			if !exists {
				return nil, invalidReference(nil)
			}
			// Dimensions first: re-encoding cannot fix them, so they are the
			// reason to report even when the file is also too large.
			if err := s.checkImageInput(ctx, schema, asset); err != nil {
				return nil, err
			}
			if schema.MaxBytes > 0 && asset.Size > schema.MaxBytes {
				return nil, s.refusal(s.messages.ParameterTooLarge,
					fmt.Sprintf("输入资产参数 %q 超过大小上限", parameter),
					sizeReplacements(schema, asset.Size, schema.MaxBytes)...)
			}
			if !inputMediaAllowed(schema.MediaTypes, asset.MediaType) {
				return nil, &generation.Error{
					Code:      generation.ErrInvalidInput,
					Operation: "resolve_input_assets",
					Detail:    fmt.Sprintf("输入资产参数 %q 的文件类型不受支持", parameter),
				}
			}
			if !inputExtensionAllowed(
				schema.Extensions,
				asset.OriginalFilename,
			) {
				return nil, &generation.Error{
					Code:      generation.ErrInvalidInput,
					Operation: "resolve_input_assets",
					Detail:    fmt.Sprintf("输入资产参数 %q 的文件扩展名不受支持", parameter),
				}
			}
			resolved[parameter] = append(resolved[parameter], asset)
		}
		if err := s.checkTotalEncoded(schema, resolved[parameter]); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func parameterDefinition(
	parameters []generation.Parameter,
	name string,
) generation.Parameter {
	for _, parameter := range parameters {
		if parameter.Name == name {
			return parameter
		}
	}
	return generation.Parameter{}
}

func inputMediaAllowed(allowed []string, actual string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == actual ||
			(strings.HasSuffix(candidate, "/*") &&
				strings.HasPrefix(actual, strings.TrimSuffix(candidate, "*"))) {
			return true
		}
	}
	return false
}

func inputExtensionAllowed(allowed []string, filename string) bool {
	if len(allowed) == 0 {
		return true
	}
	if strings.TrimSpace(filename) == "" {
		return true
	}
	actual := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))
	for _, candidate := range allowed {
		normalized := strings.ToLower(strings.TrimSpace(candidate))
		if normalized != "" && !strings.HasPrefix(normalized, ".") {
			normalized = "." + normalized
		}
		if normalized == actual {
			return true
		}
	}
	return false
}

func (s *Service) Open(
	ctx context.Context,
	asset generation.InputAsset,
) (io.ReadCloser, error) {
	return s.blobs.OpenBlob(ctx, asset.StorageKey)
}

func (s *Service) OpenOwned(
	ctx context.Context,
	owner generation.OwnerID,
	id generation.AssetID,
) (generation.InputAsset, io.ReadCloser, error) {
	return s.OpenAuthorized(ctx, owner, id, false)
}

// ListOwnedInputAssets exposes only the narrow owner-filtered metadata query
// needed by the account resource view. Implementations return storage keys for
// internal use; the HTTP projection deliberately omits them.
func (s *Service) ListOwnedInputAssets(
	ctx context.Context,
	owner generation.OwnerID,
	page int,
	pageSize int,
) ([]generation.InputAsset, int, error) {
	lister, ok := s.repository.(ownedInputAssetLister)
	if !ok {
		return nil, 0, errors.New("owned input asset listing is unavailable")
	}
	return lister.ListOwnedInputAssets(ctx, owner, page, pageSize)
}

func (s *Service) OpenAuthorized(
	ctx context.Context,
	owner generation.OwnerID,
	id generation.AssetID,
	readAll bool,
) (generation.InputAsset, io.ReadCloser, error) {
	asset, err := s.repository.GetInputAsset(ctx, id)
	if err != nil {
		var domainErr *generation.Error
		if errors.As(err, &domainErr) {
			return generation.InputAsset{}, nil, err
		}
		return generation.InputAsset{}, nil, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "open_authorized_input_asset",
			Detail:    "输入资产暂不可读取",
			Cause:     err,
		}
	}
	if !readAll && asset.OwnerID != owner {
		return generation.InputAsset{}, nil, &generation.Error{
			// A foreign asset is reported exactly like a missing one so
			// the response cannot reveal whether the ID exists (AUTH-09).
			Code:      generation.ErrNotFound,
			Operation: "get_input_asset",
		}
	}
	content, err := s.Open(ctx, asset)
	if err != nil {
		return generation.InputAsset{}, nil, &generation.Error{
			Code:      generation.ErrUnavailable,
			Operation: "open_authorized_input_asset",
			Detail:    "输入资产暂不可读取",
			Cause:     err,
		}
	}
	return asset, content, nil
}

func (s *Service) GatewayConfigured() bool {
	return s.publicURL != nil && len(s.signingKey) > 0
}

type signedPayload struct {
	AssetID generation.AssetID `json:"asset_id"`
	OwnerID generation.OwnerID `json:"owner_id"`
	Expires int64              `json:"expires"`
}

func (s *Service) ExternalURL(
	_ context.Context,
	asset generation.InputAsset,
	validFor time.Duration,
) (string, error) {
	if !s.GatewayConfigured() {
		return "", errors.New("Provider 资产网关未配置")
	}
	if asset.ID == "" || asset.OwnerID == "" {
		return "", errors.New("Provider 资产缺少身份")
	}
	if validFor <= 0 {
		validFor = 30 * time.Minute
	}
	if validFor > 24*time.Hour {
		validFor = 24 * time.Hour
	}
	payload, err := json.Marshal(signedPayload{
		AssetID: asset.ID,
		OwnerID: asset.OwnerID,
		Expires: s.now().Add(validFor).Unix(),
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := encoded + "." + base64.RawURLEncoding.EncodeToString(s.signature(encoded))
	relative := &url.URL{Path: s.providerAssetRoute.Render(token)}
	return s.publicURL.ResolveReference(relative).String(), nil
}

func (s *Service) OpenSigned(
	ctx context.Context,
	token string,
) (generation.InputAsset, io.ReadCloser, error) {
	if !s.GatewayConfigured() {
		return generation.InputAsset{}, nil, errors.New("Provider 资产网关未配置")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return generation.InputAsset{}, nil, errors.New("Provider 资产令牌无效")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(provided, s.signature(parts[0])) {
		return generation.InputAsset{}, nil, errors.New("Provider 资产令牌无效")
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return generation.InputAsset{}, nil, errors.New("Provider 资产令牌无效")
	}
	var payload signedPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil ||
		payload.AssetID == "" ||
		payload.OwnerID == "" ||
		payload.Expires <= s.now().Unix() {
		return generation.InputAsset{}, nil, errors.New("Provider 资产令牌无效或已过期")
	}
	assets, err := s.repository.LoadInputAssets(
		ctx,
		payload.OwnerID,
		[]generation.AssetID{payload.AssetID},
	)
	if err != nil {
		return generation.InputAsset{}, nil, err
	}
	asset, exists := assets[payload.AssetID]
	if !exists {
		return generation.InputAsset{}, nil, errors.New("Provider 资产不存在")
	}
	body, err := s.Open(ctx, asset)
	if err != nil {
		return generation.InputAsset{}, nil, err
	}
	return asset, body, nil
}

func (s *Service) signature(payload string) []byte {
	mac := hmac.New(sha256.New, s.signingKey)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func invalidReference(cause error) error {
	return &generation.Error{
		Code:      generation.ErrInvalidInput,
		Operation: "resolve_input_assets",
		Detail:    "输入资产不存在或不属于当前用户",
		Cause:     cause,
	}
}

func randomAssetID() generation.AssetID {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("生成输入资产 ID: %v", err))
	}
	return generation.AssetID("asset_" + hex.EncodeToString(buffer))
}

var _ generation.InputAssetResolver = (*Service)(nil)
var _ generation.InputAssetPublisher = (*Service)(nil)

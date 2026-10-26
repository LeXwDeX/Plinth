package matting

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers/uploadname"
)

type Config struct {
	BaseURL        string
	APIKey         string
	AuthHeader     string
	AuthPrefix     string
	RequestTimeout time.Duration
	HTTPClient     *http.Client
	InputAssets    generation.InputAssetResolver
	// ScratchDir holds private upload copies (spooled multipart bodies).
	// Empty means os.TempDir(); production passes the storage volume's tmp
	// directory because the container's /tmp is a small tmpfs (#321 A).
	ScratchDir string
}

type Adapter struct {
	client      *httpjson.Client
	inputAssets generation.InputAssetResolver
}

type wireProtocol struct {
	CreatePath   string                `json:"create_path"`
	ObservePath  string                `json:"observe_path"`
	DownloadPath string                `json:"download_path"`
	DownloadAuth wireAuthProtocol      `json:"download_auth"`
	Multipart    wireMultipartProtocol `json:"multipart"`
	Response     wireResponseProtocol  `json:"response"`
	Statuses     wireStatusProtocol    `json:"statuses"`
	Artifact     wireArtifactProtocol  `json:"artifact"`
	Messages     map[string]string     `json:"messages"`
}

// wireAuthProtocol 声明下载鉴权头；缺省回退 Authorization: Bearer（与
// 历史行为一致，历史快照未声明时保持不变）。
type wireAuthProtocol struct {
	Header string `json:"header"`
	Prefix string `json:"prefix"`
}

func (auth wireAuthProtocol) headerName() string {
	if strings.TrimSpace(auth.Header) == "" {
		return "Authorization"
	}
	return auth.Header
}

func (auth wireAuthProtocol) prefix() string {
	if auth.Prefix == "" {
		return "Bearer "
	}
	return auth.Prefix
}

type wireMultipartProtocol struct {
	PipelineField    string `json:"pipeline_field"`
	FileField        string `json:"file_field"`
	FilenameTemplate string `json:"filename_template"`
}

type wireResponseProtocol struct {
	BatchIDPath       string `json:"batch_id_path"`
	BatchStatusPath   string `json:"batch_status_path"`
	TasksPath         string `json:"tasks_path"`
	TaskIDPath        string `json:"task_id_path"`
	TaskStatusPath    string `json:"task_status_path"`
	DownloadablePath  string `json:"downloadable_path"`
	DownloadPathPath  string `json:"download_path_path"`
	SourceNamePath    string `json:"source_name_path"`
	ErrorMessagePath  string `json:"error_message_path"`
	InvalidReasonPath string `json:"invalid_reason_path"`
}

type providerRecord struct {
	Identifier        string `json:"identifier"`
	Status            string `json:"status"`
	Downloadable      bool   `json:"downloadable"`
	SourceName        string `json:"source_name"`
	ErrorMessage      string `json:"error_message"`
	InvalidReason     string `json:"invalid_reason"`
	SuccessfulOrdinal int    `json:"successful_ordinal,omitempty"`
}

type wireStatusProtocol struct {
	Accepted        []string `json:"accepted"`
	Running         []string `json:"running"`
	Completed       []string `json:"completed"`
	Failed          []string `json:"failed"`
	Canceled        []string `json:"canceled"`
	FinishedTasks   []string `json:"finished_tasks"`
	SuccessfulTasks []string `json:"successful_tasks"`
}

type wireArtifactProtocol struct {
	Role      string `json:"role"`
	MediaType string `json:"media_type"`
}

func New(config Config) (*Adapter, error) {
	client, err := httpjson.NewWithConfig(httpjson.Config{
		BaseURL:        config.BaseURL,
		APIKey:         config.APIKey,
		AuthHeader:     config.AuthHeader,
		AuthPrefix:     config.AuthPrefix,
		RequestTimeout: config.RequestTimeout,
		HTTPClient:     config.HTTPClient,
		SpoolDir:       config.ScratchDir,
	})
	if err != nil {
		return nil, fmt.Errorf("matting: %w", err)
	}
	return &Adapter{client: client, inputAssets: config.InputAssets}, nil
}

func (a *Adapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	protocol, err := protocolFor(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	if request.Offering.Capability != generation.BackgroundRemove {
		return generation.Observation{}, failure(
			generation.FailureInvalidInput,
			false,
			false,
			protocol.Messages["invalid_capability"],
			nil,
		)
	}
	assetParameter, err := mattingAssetParameter(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	assets := request.InputAssets[assetParameter.Name]
	if a.inputAssets == nil ||
		len(assets) < assetParameter.MinItems ||
		len(assets) > assetParameter.MaxItems {
		return generation.Observation{}, failure(
			generation.FailureInvalidInput,
			false,
			false,
			protocol.Messages["invalid_assets"],
			nil,
		)
	}
	// 文件名扩展名按已核验的媒体类型在派发许可前确定；无可靠映射即拒绝。
	extensions := make([]string, len(assets))
	for index, asset := range assets {
		extension, err := uploadname.Extension(asset.MediaType, assetParameter.Extensions)
		if err != nil {
			return generation.Observation{}, failure(
				generation.FailureInvalidInput,
				false,
				false,
				protocol.Messages["invalid_assets"]+"："+err.Error(),
				err,
			)
		}
		extensions[index] = extension
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return generation.Observation{}, failure(
			generation.FailureInvalidInput,
			false,
			false,
			protocol.Messages["invalid_input"],
			err,
		)
	}
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	contentType := form.FormDataContentType()
	go func() {
		writeErr := writeMultipart(
			ctx,
			form,
			a.inputAssets,
			request.Offering,
			input,
			assets,
			extensions,
			protocol.Multipart,
		)
		if closeErr := form.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = writer.CloseWithError(writeErr)
	}()
	// 非幂等付费批处理创建提交前先持久化私有派发意图并取得发送许可
	// （W4 §4.5）。multipart 体在本地上读取证失败时已有"未提交"语义，
	// 检查点失败（含 ErrDispatchPending）必须放弃发送（0 次上游调用）。
	dispatchPermit, permitErr := privateDispatchPermit(ctx, request.SubmissionKey, dispatchIntentStage{
		Stage:   "background_remove.create",
		Details: map[string]any{"create_path": protocol.CreatePath},
	})
	if permitErr != nil {
		return generation.Observation{}, permitErr
	}
	var response map[string]any
	if err := a.client.Do(
		ctx,
		http.MethodPost,
		protocol.CreatePath,
		reader,
		contentType,
		request.SubmissionKey,
		&response,
		true,
	); err != nil {
		return generation.Observation{}, err
	}
	batchID := stringAt(response, protocol.Response.BatchIDPath)
	if batchID == "" {
		return generation.Observation{}, failure(
			generation.FailureSubmissionUnknown,
			false,
			true,
			protocol.Messages["missing_batch_id"],
			nil,
		)
	}
	job := &generation.UpstreamJob{
		ProviderID: request.Offering.ProviderID,
		RemoteID:   batchID,
	}
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       job,
		PollAfter: request.Offering.PollInterval,
		DedupeKey: batchID + ":accepted",
		Dispatch:  dispatchPermit,
	}, nil
}

// dispatchIntentStage 描述一次私有派发意图的阶段与协调标识。
type dispatchIntentStage struct {
	Stage         string         `json:"stage"`
	SubmissionKey string         `json:"submission_key"`
	Details       map[string]any `json:"details,omitempty"`
}

func privateDispatchPermit(
	ctx context.Context,
	submissionKey string,
	stage dispatchIntentStage,
) (*generation.DispatchReceipt, error) {
	stage.SubmissionKey = submissionKey
	encoded, err := json.Marshal(stage)
	if err != nil {
		return nil, failure(generation.FailureInternal, false, false, fmt.Sprintf("编码私有派发意图失败: %v", err), nil)
	}
	return generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: encoded})
}

func writeMultipart(
	ctx context.Context,
	form *multipart.Writer,
	inputAssets generation.InputAssetResolver,
	offering generation.Offering,
	input map[string]any,
	assets []generation.InputAsset,
	extensions []string,
	protocol wireMultipartProtocol,
) error {
	pipeline := map[string]any{}
	for _, parameter := range offering.Parameters {
		switch parameter.Type {
		case "asset", "asset-array", "task":
			continue
		}
		value, exists := input[parameter.Name]
		if !exists {
			continue
		}
		wireName := parameter.WireName
		if wireName == "" {
			wireName = parameter.Name
		}
		pipeline[wireName] = value
	}
	encoded, err := json.Marshal(pipeline)
	if err != nil {
		return err
	}
	if err := form.WriteField(protocol.PipelineField, string(encoded)); err != nil {
		return err
	}
	for index, asset := range assets {
		body, err := inputAssets.Open(ctx, asset)
		if err != nil {
			return err
		}
		header := make(textproto.MIMEHeader)
		header.Set(
			"Content-Disposition",
			fmt.Sprintf(
				`form-data; name="%s"; filename="%s"`,
				protocol.FileField,
				strings.NewReplacer(
					"{ordinal}", fmt.Sprintf("%d", index+1),
					"{extension}", extensions[index],
				).Replace(protocol.FilenameTemplate),
			),
		)
		header.Set("Content-Type", asset.MediaType)
		part, err := form.CreatePart(header)
		if err == nil {
			_, err = io.Copy(part, body)
		}
		closeErr := body.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (a *Adapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	protocol, err := protocolFor(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	var response map[string]any
	if err := a.client.DoJSON(
		ctx,
		http.MethodGet,
		strings.Replace(
			protocol.ObservePath,
			"{remote_id}",
			url.PathEscape(request.Job.RemoteID),
			1,
		),
		nil,
		string(request.AttemptID),
		&response,
		false,
	); err != nil {
		return generation.Observation{}, err
	}
	return wireDocument(response).observation(protocol, request)
}

type wireDocument map[string]any

func (value wireDocument) observation(
	protocol wireProtocol,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	rawStatus := stringAt(value, protocol.Response.BatchStatusPath)
	status := normalizeStatus(rawStatus)
	progress := value.progress(protocol)
	opaque, err := json.Marshal(value.providerRecords(protocol))
	if err != nil {
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			false,
			protocol.Messages["invalid_input"],
			err,
		)
	}
	opaqueDigest := sha256.Sum256(opaque)
	observation := generation.Observation{
		Job:       &request.Job,
		Progress:  progress,
		PollAfter: request.Offering.PollInterval,
		DedupeKey: fmt.Sprintf(
			"%s:%s:%d:%x",
			request.Job.RemoteID,
			status,
			progress,
			opaqueDigest[:8],
		),
		Opaque: opaque,
	}
	switch {
	case statusIn(status, protocol.Statuses.Accepted):
		observation.Phase = generation.UpstreamAccepted
	case statusIn(status, protocol.Statuses.Running):
		observation.Phase = generation.UpstreamRunning
	case statusIn(status, protocol.Statuses.Completed):
		observation.Phase = generation.UpstreamCompleted
		observation.Progress = 100
		observation.Artifacts = value.claims(protocol, request.AttemptID)
		if len(observation.Artifacts) == 0 {
			observation.Phase = generation.UpstreamFailed
			observation.Failure = &generation.Failure{
				Class:      generation.FailureRejected,
				SafeDetail: protocol.Messages["no_artifacts"],
			}
		}
	case statusIn(status, protocol.Statuses.Failed):
		observation.Phase = generation.UpstreamFailed
		observation.Failure = &generation.Failure{
			Class:      generation.FailureRejected,
			SafeDetail: value.failureDetail(protocol),
		}
	case statusIn(status, protocol.Statuses.Canceled):
		observation.Phase = generation.UpstreamCanceled
		observation.Failure = &generation.Failure{
			Class:      generation.FailureRejected,
			SafeDetail: protocol.Messages["canceled"],
		}
	default:
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			false,
			messageWith(
				protocol.Messages["unknown_status"],
				"status",
				rawStatus,
			),
			nil,
		)
	}
	return observation, nil
}

func (value wireDocument) providerRecords(
	protocol wireProtocol,
) []providerRecord {
	tasks := mapsAt(value, protocol.Response.TasksPath)
	records := make([]providerRecord, 0, len(tasks))
	successfulOrdinal := 0
	for _, task := range tasks {
		status := normalizeStatus(
			stringAt(task, protocol.Response.TaskStatusPath),
		)
		downloadable := boolAt(task, protocol.Response.DownloadablePath)
		identifier := strings.TrimSpace(
			stringAt(task, protocol.Response.TaskIDPath),
		)
		record := providerRecord{
			Identifier:   identifier,
			Status:       publicRecordStatus(status, protocol),
			Downloadable: downloadable,
			SourceName: strings.TrimSpace(
				stringAt(task, protocol.Response.SourceNamePath),
			),
			ErrorMessage: strings.TrimSpace(
				stringAt(task, protocol.Response.ErrorMessagePath),
			),
			InvalidReason: strings.TrimSpace(
				stringAt(task, protocol.Response.InvalidReasonPath),
			),
		}
		if statusIn(status, protocol.Statuses.SuccessfulTasks) &&
			downloadable &&
			identifier != "" {
			successfulOrdinal++
			record.SuccessfulOrdinal = successfulOrdinal
		}
		records = append(records, record)
	}
	return records
}

func publicRecordStatus(status string, protocol wireProtocol) string {
	switch {
	case statusIn(status, protocol.Statuses.SuccessfulTasks):
		return string(generation.TaskSucceeded)
	case statusIn(status, protocol.Statuses.FinishedTasks):
		return string(generation.TaskFailed)
	default:
		return status
	}
}

func (value wireDocument) progress(protocol wireProtocol) int {
	tasks := mapsAt(value, protocol.Response.TasksPath)
	if len(tasks) == 0 {
		return 0
	}
	finished := 0
	for _, task := range tasks {
		if statusIn(
			normalizeStatus(stringAt(task, protocol.Response.TaskStatusPath)),
			protocol.Statuses.FinishedTasks,
		) {
			finished++
		}
	}
	progress := (finished*100 + len(tasks)/2) / len(tasks)
	if progress >= 100 &&
		!statusIn(
			normalizeStatus(stringAt(value, protocol.Response.BatchStatusPath)),
			protocol.Statuses.Completed,
		) {
		return 99
	}
	return progress
}

func (value wireDocument) claims(
	protocol wireProtocol,
	attemptID generation.AttemptID,
) []generation.ArtifactClaim {
	tasks := mapsAt(value, protocol.Response.TasksPath)
	claims := make([]generation.ArtifactClaim, 0, len(tasks))
	for _, task := range tasks {
		taskID := stringAt(task, protocol.Response.TaskIDPath)
		if !statusIn(
			normalizeStatus(stringAt(task, protocol.Response.TaskStatusPath)),
			protocol.Statuses.SuccessfulTasks,
		) ||
			!boolAt(task, protocol.Response.DownloadablePath) ||
			taskID == "" {
			continue
		}
		locator := strings.Replace(
			protocol.DownloadPath,
			"{task_id}",
			url.PathEscape(taskID),
			1,
		)
		if reported := strings.TrimSpace(
			stringAt(task, protocol.Response.DownloadPathPath),
		); reported != "" {
			if strings.HasPrefix(reported, "/") &&
				!strings.HasPrefix(protocol.DownloadPath, "/") {
				reported = strings.TrimPrefix(reported, "/")
			}
			if validDownloadLocator(reported, protocol.DownloadPath) {
				locator = reported
			}
		}
		claims = append(claims, generation.ArtifactClaim{
			ID:        fmt.Sprintf("%s:artifact:%d", attemptID, len(claims)+1),
			Role:      protocol.Artifact.Role,
			Locator:   locator,
			MediaType: protocol.Artifact.MediaType,
			Variant: strings.TrimSpace(
				stringAt(task, protocol.Response.SourceNamePath),
			),
		})
	}
	return claims
}

func (value wireDocument) failureDetail(protocol wireProtocol) string {
	for _, task := range mapsAt(value, protocol.Response.TasksPath) {
		if detail := strings.TrimSpace(
			stringAt(task, protocol.Response.ErrorMessagePath),
		); detail != "" {
			return detail
		}
		if detail := strings.TrimSpace(
			stringAt(task, protocol.Response.InvalidReasonPath),
		); detail != "" {
			return detail
		}
	}
	return protocol.Messages["failed"]
}

func (a *Adapter) OpenAsset(
	ctx context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	protocol, err := protocolFor(request.Offering)
	if err != nil {
		return nil, generation.AssetMetadata{}, err
	}
	locator := strings.TrimSpace(request.Claim.Locator)
	if !validDownloadLocator(locator, protocol.DownloadPath) {
		return nil, generation.AssetMetadata{}, failure(
			generation.FailureProtocol,
			false,
			false,
			protocol.Messages["invalid_locator"],
			nil,
		)
	}
	relative, _ := url.Parse(locator)
	endpoint := a.client.BaseURL.ResolveReference(relative)
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return nil, generation.AssetMetadata{}, failure(
			generation.FailureInternal,
			false,
			false,
			protocol.Messages["download_request_failed"],
			err,
		)
	}
	httpRequest.Header.Set(protocol.DownloadAuth.headerName(), protocol.DownloadAuth.prefix()+a.client.APIKey)
	response, err := a.client.HTTPClient.Do(httpRequest)
	if err != nil {
		return nil, generation.AssetMetadata{}, failure(
			generation.FailureTransport,
			true,
			false,
			protocol.Messages["download_failed"],
			err,
		)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		class := generation.FailureRejected
		retryable := false
		if response.StatusCode == http.StatusUnauthorized ||
			response.StatusCode == http.StatusForbidden {
			class = generation.FailureAuthentication
		} else if response.StatusCode == http.StatusTooManyRequests {
			class = generation.FailureRateLimited
			retryable = true
		} else if response.StatusCode >= 500 {
			class = generation.FailureUpstreamUnavailable
			retryable = true
		}
		return nil, generation.AssetMetadata{}, failure(
			class,
			retryable,
			false,
			messageWith(
				protocol.Messages["download_http_status"],
				"status",
				fmt.Sprintf("%d", response.StatusCode),
			),
			nil,
		)
	}
	return response.Body, generation.AssetMetadata{
		MediaType: response.Header.Get("Content-Type"),
		Size:      response.ContentLength,
	}, nil
}

func validDownloadLocator(locator string, template string) bool {
	prefix, suffix, ok := splitTemplate(template, "{task_id}")
	if !ok || !strings.HasPrefix(locator, prefix) ||
		!strings.HasSuffix(locator, suffix) {
		return false
	}
	identifier := strings.TrimSuffix(
		strings.TrimPrefix(locator, prefix),
		suffix,
	)
	decoded, err := url.PathUnescape(identifier)
	parsed, parseErr := url.Parse(locator)
	return err == nil &&
		parseErr == nil &&
		parsed.Scheme == "" &&
		parsed.Host == "" &&
		parsed.RawQuery == "" &&
		parsed.Fragment == "" &&
		decoded != "" &&
		decoded != "." &&
		decoded != ".." &&
		!strings.ContainsAny(decoded, `/\`) &&
		path.Clean(locator) == locator
}

// ValidateOffering 在应用启动时验证 Matting Offering 的完整协议 Schema。
// 运行期不提供硬编码回退：配置不完整时应用必须拒绝启动或拒绝该调用。
func ValidateOffering(offering generation.Offering) error {
	var protocol wireProtocol
	if err := json.Unmarshal(offering.ProviderOptions, &protocol); err != nil {
		return fmt.Errorf(
			"matting offering %q provider_options: %w",
			offering.ID,
			err,
		)
	}
	if err := validateRoute(protocol.CreatePath, "create_path", ""); err != nil {
		return offeringProtocolError(offering, err)
	}
	if err := validateRoute(
		protocol.ObservePath,
		"observe_path",
		"{remote_id}",
	); err != nil {
		return offeringProtocolError(offering, err)
	}
	if err := validateRoute(
		protocol.DownloadPath,
		"download_path",
		"{task_id}",
	); err != nil {
		return offeringProtocolError(offering, err)
	}
	for name, value := range map[string]string{
		"multipart.pipeline_field":     protocol.Multipart.PipelineField,
		"multipart.file_field":         protocol.Multipart.FileField,
		"multipart.filename_template":  protocol.Multipart.FilenameTemplate,
		"response.batch_id_path":       protocol.Response.BatchIDPath,
		"response.batch_status_path":   protocol.Response.BatchStatusPath,
		"response.tasks_path":          protocol.Response.TasksPath,
		"response.task_id_path":        protocol.Response.TaskIDPath,
		"response.task_status_path":    protocol.Response.TaskStatusPath,
		"response.downloadable_path":   protocol.Response.DownloadablePath,
		"response.download_path_path":  protocol.Response.DownloadPathPath,
		"response.source_name_path":    protocol.Response.SourceNamePath,
		"response.error_message_path":  protocol.Response.ErrorMessagePath,
		"response.invalid_reason_path": protocol.Response.InvalidReasonPath,
		"artifact.role":                protocol.Artifact.Role,
		"artifact.media_type":          protocol.Artifact.MediaType,
	} {
		if strings.TrimSpace(value) == "" {
			return offeringProtocolError(
				offering,
				fmt.Errorf("%s is required", name),
			)
		}
	}
	if !strings.Contains(
		protocol.Multipart.FilenameTemplate,
		"{ordinal}",
	) || !strings.Contains(
		protocol.Multipart.FilenameTemplate,
		"{extension}",
	) {
		return offeringProtocolError(
			offering,
			fmt.Errorf(
				"multipart.filename_template requires {ordinal} and {extension}",
			),
		)
	}
	statusSets := map[string][]string{
		"statuses.accepted":         protocol.Statuses.Accepted,
		"statuses.running":          protocol.Statuses.Running,
		"statuses.completed":        protocol.Statuses.Completed,
		"statuses.failed":           protocol.Statuses.Failed,
		"statuses.canceled":         protocol.Statuses.Canceled,
		"statuses.finished_tasks":   protocol.Statuses.FinishedTasks,
		"statuses.successful_tasks": protocol.Statuses.SuccessfulTasks,
	}
	phaseOwners := map[string]string{}
	for name, values := range statusSets {
		if len(values) == 0 {
			return offeringProtocolError(
				offering,
				fmt.Errorf("%s is required", name),
			)
		}
		seen := map[string]struct{}{}
		for _, value := range values {
			normalized := normalizeStatus(value)
			if normalized == "" {
				return offeringProtocolError(
					offering,
					fmt.Errorf("%s contains an empty status", name),
				)
			}
			if _, exists := seen[normalized]; exists {
				return offeringProtocolError(
					offering,
					fmt.Errorf("%s contains duplicate status %q", name, value),
				)
			}
			seen[normalized] = struct{}{}
			switch name {
			case "statuses.accepted",
				"statuses.running",
				"statuses.completed",
				"statuses.failed",
				"statuses.canceled":
				if owner, exists := phaseOwners[normalized]; exists {
					return offeringProtocolError(
						offering,
						fmt.Errorf(
							"status %q belongs to both %s and %s",
							value,
							owner,
							name,
						),
					)
				}
				phaseOwners[normalized] = name
			}
		}
	}
	for _, name := range []string{
		"invalid_capability",
		"invalid_assets",
		"invalid_input",
		"missing_batch_id",
		"no_artifacts",
		"failed",
		"canceled",
		"unknown_status",
		"invalid_locator",
		"download_request_failed",
		"download_failed",
		"download_http_status",
	} {
		if strings.TrimSpace(protocol.Messages[name]) == "" {
			return offeringProtocolError(
				offering,
				fmt.Errorf("messages.%s is required", name),
			)
		}
	}
	if _, err := mattingAssetParameter(offering); err != nil {
		return offeringProtocolError(offering, err)
	}
	return nil
}

func protocolFor(offering generation.Offering) (wireProtocol, error) {
	if err := ValidateOffering(offering); err != nil {
		return wireProtocol{}, failure(
			generation.FailureProtocol,
			false,
			false,
			err.Error(),
			err,
		)
	}
	var protocol wireProtocol
	_ = json.Unmarshal(offering.ProviderOptions, &protocol)
	return protocol, nil
}

func mattingAssetParameter(
	offering generation.Offering,
) (generation.Parameter, error) {
	var found *generation.Parameter
	for index := range offering.Parameters {
		parameter := &offering.Parameters[index]
		if parameter.Semantic != generation.SemanticReferenceImage ||
			(parameter.Type != "asset" && parameter.Type != "asset-array") {
			continue
		}
		if found != nil {
			return generation.Parameter{}, fmt.Errorf(
				"multiple reference-image asset parameters",
			)
		}
		found = parameter
	}
	if found == nil {
		return generation.Parameter{}, fmt.Errorf(
			"reference-image asset parameter is required",
		)
	}
	result := *found
	switch result.Type {
	case "asset":
		result.MinItems = 1
		result.MaxItems = 1
	case "asset-array":
		if result.MinItems < 1 ||
			result.MaxItems < result.MinItems {
			return generation.Parameter{}, fmt.Errorf(
				"reference-image asset-array requires valid min_items/max_items",
			)
		}
	}
	return result, nil
}

func offeringProtocolError(offering generation.Offering, err error) error {
	return fmt.Errorf("matting offering %q protocol: %w", offering.ID, err)
}

func validateRoute(value string, name string, placeholder string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if placeholder != "" {
		if _, _, ok := splitTemplate(value, placeholder); !ok {
			return fmt.Errorf("%s requires exactly one %s", name, placeholder)
		}
	}
	withoutPlaceholder := strings.Replace(value, placeholder, "identifier", 1)
	parsed, err := url.Parse(withoutPlaceholder)
	if err != nil ||
		parsed.Scheme != "" ||
		parsed.Host != "" ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		path.Clean(withoutPlaceholder) != withoutPlaceholder {
		return fmt.Errorf("%s must be a clean relative path", name)
	}
	return nil
}

func splitTemplate(value string, placeholder string) (string, string, bool) {
	if strings.Count(value, placeholder) != 1 {
		return "", "", false
	}
	before, after, _ := strings.Cut(value, placeholder)
	return before, after, true
}

func pathValue(root map[string]any, dottedPath string) (any, bool) {
	var current any = root
	for _, segment := range strings.Split(dottedPath, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func stringAt(root map[string]any, dottedPath string) string {
	value, ok := pathValue(root, dottedPath)
	if !ok {
		return ""
	}
	result, _ := value.(string)
	return strings.TrimSpace(result)
}

func boolAt(root map[string]any, dottedPath string) bool {
	value, ok := pathValue(root, dottedPath)
	if !ok {
		return false
	}
	result, _ := value.(bool)
	return result
}

func mapsAt(root map[string]any, dottedPath string) []map[string]any {
	value, ok := pathValue(root, dottedPath)
	if !ok {
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(values))
	for _, item := range values {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func normalizeStatus(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func statusIn(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if value == normalizeStatus(candidate) {
			return true
		}
	}
	return false
}

func messageWith(template string, name string, value string) string {
	return strings.ReplaceAll(template, "{"+name+"}", value)
}

func failure(
	class generation.FailureClass,
	retryable bool,
	ambiguous bool,
	detail string,
	cause error,
) error {
	return &generation.ProviderError{
		Failure: &generation.Failure{
			Class:      class,
			Retryable:  retryable,
			Ambiguous:  ambiguous,
			SafeDetail: detail,
		},
		Cause: cause,
	}
}

var _ generation.ProviderPort = (*Adapter)(nil)

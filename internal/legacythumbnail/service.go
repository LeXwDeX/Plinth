package legacythumbnail

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type Source interface {
	Fetch(
		context.Context,
		assetdelivery.SourceRequest,
	) (assetdelivery.SourceContent, error)
}

type Config struct {
	Repository               generation.Repository
	Assets                   generation.AssetStore
	Source                   Source
	ResolveSource            func(string) string
	ThumbnailRole            string
	SourceFields             map[generation.Capability][]string
	AcknowledgedSourceLosses map[generation.TaskID]string
	BatchSize                int
	MaximumAssetSize         int64
}

type Issue struct {
	TaskID generation.TaskID
	Detail string
}

type Report struct {
	Scanned                 int
	Eligible                int
	Migrated                int
	AlreadyPresent          int
	AcknowledgedUnavailable int
	Unavailable             int
	Acknowledgements        []Issue
	Issues                  []Issue
}

type Service struct {
	repository               generation.Repository
	assets                   generation.AssetStore
	source                   Source
	resolveSource            func(string) string
	thumbnailRole            string
	sourceFields             map[generation.Capability][]string
	acknowledgedSourceLosses map[generation.TaskID]string
	batchSize                int
	maximumAssetSize         int64
}

func New(config Config) (*Service, error) {
	if config.Repository == nil {
		return nil, errors.New("legacythumbnail: 缺少 Repository")
	}
	if config.Assets == nil {
		return nil, errors.New("legacythumbnail: 缺少 AssetStore")
	}
	if config.Source == nil {
		return nil, errors.New("legacythumbnail: 缺少 Source")
	}
	role := strings.TrimSpace(config.ThumbnailRole)
	if role == "" {
		return nil, errors.New("legacythumbnail: 缺少 thumbnail role")
	}
	if config.BatchSize <= 0 || config.BatchSize > 100 {
		return nil, errors.New("legacythumbnail: batch size 必须在 1..100")
	}
	if config.MaximumAssetSize <= 0 {
		return nil, errors.New("legacythumbnail: maximum asset size 必须大于 0")
	}
	sourceFields := make(
		map[generation.Capability][]string,
		len(config.SourceFields),
	)
	for capability, fields := range config.SourceFields {
		if !capability.Valid() {
			return nil, fmt.Errorf(
				"legacythumbnail: capability %q 无效",
				capability,
			)
		}
		for _, field := range fields {
			field = strings.TrimSpace(field)
			if field == "" {
				return nil, fmt.Errorf(
					"legacythumbnail: capability %q 含空来源字段",
					capability,
				)
			}
			sourceFields[capability] = append(
				sourceFields[capability],
				field,
			)
		}
	}
	if len(sourceFields) == 0 {
		return nil, errors.New("legacythumbnail: 缺少 Schema 来源字段")
	}
	acknowledgedSourceLosses := make(
		map[generation.TaskID]string,
		len(config.AcknowledgedSourceLosses),
	)
	for taskID, reason := range config.AcknowledgedSourceLosses {
		taskID = generation.TaskID(strings.TrimSpace(string(taskID)))
		reason = strings.TrimSpace(reason)
		if taskID == "" || reason == "" {
			return nil, errors.New(
				"legacythumbnail: 来源缺失确认的 task ID 和原因不能为空",
			)
		}
		acknowledgedSourceLosses[taskID] = reason
	}
	resolveSource := config.ResolveSource
	if resolveSource == nil {
		resolveSource = func(source string) string { return source }
	}
	return &Service{
		repository:               config.Repository,
		assets:                   config.Assets,
		source:                   config.Source,
		resolveSource:            resolveSource,
		thumbnailRole:            role,
		sourceFields:             sourceFields,
		acknowledgedSourceLosses: acknowledgedSourceLosses,
		batchSize:                config.BatchSize,
		maximumAssetSize:         config.MaximumAssetSize,
	}, nil
}

func (s *Service) Run(ctx context.Context) (Report, error) {
	var report Report
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		page, err := s.repository.List(
			ctx,
			generation.AllTasks(),
			generation.ListQuery{
				States: []generation.TaskState{
					generation.TaskSucceeded,
				},
				Offset: offset,
				Limit:  s.batchSize,
			},
		)
		if err != nil {
			return report, fmt.Errorf("列出成功任务: %w", err)
		}
		if len(page.Tasks) == 0 {
			break
		}
		for _, listed := range page.Tasks {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			report.Scanned++
			s.backfillTask(ctx, listed.ID, &report)
		}
		offset += len(page.Tasks)
		if offset >= page.Total {
			break
		}
	}
	return report, nil
}

func (s *Service) backfillTask(
	ctx context.Context,
	taskID generation.TaskID,
	report *Report,
) {
	aggregate, err := s.repository.Load(ctx, taskID)
	if err != nil {
		report.addUnavailable(taskID, "读取任务失败")
		return
	}
	if aggregate.CurrentAttempt == nil {
		return
	}
	sourceURL := s.declaredSource(
		aggregate.Task.Capability,
		aggregate.CurrentAttempt.Opaque,
	)
	if sourceURL == "" {
		return
	}
	report.Eligible++
	if hasRole(aggregate.Task.Assets, s.thumbnailRole) {
		report.AlreadyPresent++
		return
	}
	sourceURL = strings.TrimSpace(s.resolveSource(sourceURL))
	if sourceURL == "" {
		s.addSourceUnavailable(report, taskID, "截图来源无法解析")
		return
	}
	content, err := s.source.Fetch(ctx, assetdelivery.SourceRequest{
		URL:    sourceURL,
		TaskID: string(taskID),
	})
	if err != nil {
		s.addSourceUnavailable(report, taskID, "截图来源不可读取")
		return
	}
	if content.Body == nil {
		s.addSourceUnavailable(report, taskID, "截图来源返回空内容")
		return
	}
	body := bufio.NewReader(content.Body)
	mediaType := strings.ToLower(strings.TrimSpace(content.MediaType))
	if separator := strings.IndexByte(mediaType, ';'); separator >= 0 {
		mediaType = strings.TrimSpace(mediaType[:separator])
	}
	if !strings.HasPrefix(mediaType, "image/") {
		prefix, peekErr := body.Peek(512)
		if peekErr != nil && !errors.Is(peekErr, io.EOF) {
			_ = content.Body.Close()
			s.addSourceUnavailable(report, taskID, "截图内容无法检测")
			return
		}
		detected := strings.ToLower(http.DetectContentType(prefix))
		if separator := strings.IndexByte(detected, ';'); separator >= 0 {
			detected = strings.TrimSpace(detected[:separator])
		}
		if !strings.HasPrefix(detected, "image/") {
			_ = content.Body.Close()
			s.addSourceUnavailable(report, taskID, "截图来源不是图片")
			return
		}
		mediaType = detected
	}
	asset, putErr := s.assets.Put(ctx, generation.AssetWrite{
		AssetID: generation.AssetID(
			string(taskID) + "-legacy-" + s.thumbnailRole,
		),
		OwnerID:   aggregate.Task.OwnerID,
		TaskID:    taskID,
		AttemptID: aggregate.CurrentAttempt.ID,
		Role:      s.thumbnailRole,
		Ordinal:   nextRoleOrdinal(aggregate.Task.Assets, s.thumbnailRole),
		Required:  false,
		MediaType: mediaType,
		MaxBytes:  s.maximumAssetSize,
		Body:      body,
	})
	closeErr := content.Body.Close()
	if putErr != nil || closeErr != nil {
		report.addUnavailable(taskID, "截图写入平台存储失败")
		return
	}
	expectedVersion := aggregate.Task.Version
	updatedTask := aggregate.Task
	updatedTask.Version++
	err = s.repository.Commit(ctx, generation.Commit{
		ExpectedVersion: expectedVersion,
		Task:            updatedTask,
		NewAssets:       []generation.Asset{asset},
	})
	if errors.Is(err, generation.RepositoryConflict) {
		latest, loadErr := s.repository.Load(ctx, taskID)
		if loadErr == nil && hasRole(latest.Task.Assets, s.thumbnailRole) {
			report.AlreadyPresent++
			return
		}
		// Reuse the materialized content only while the same attempt and source
		// still own it. An unrelated version update gets one immediate rebase.
		if loadErr == nil && latest.CurrentAttempt != nil && latest.CurrentAttempt.ID == aggregate.CurrentAttempt.ID && sameThumbnailSource(s.declaredSource(latest.Task.Capability, latest.CurrentAttempt.Opaque), s.declaredSource(aggregate.Task.Capability, aggregate.CurrentAttempt.Opaque)) {
			asset.Ordinal = nextRoleOrdinal(latest.Task.Assets, s.thumbnailRole)
			updatedTask = latest.Task
			updatedTask.Version++
			err = s.repository.Commit(ctx, generation.Commit{ExpectedVersion: latest.Task.Version, Task: updatedTask, NewAssets: []generation.Asset{asset}})
		}
	}
	if errors.Is(err, generation.RepositoryConflict) {
		latest, loadErr := s.repository.Load(ctx, taskID)
		if loadErr == nil && hasRole(latest.Task.Assets, s.thumbnailRole) {
			report.AlreadyPresent++
			return
		}
	}
	if err != nil {
		report.addUnavailable(taskID, "截图登记到任务失败")
		return
	}
	report.Migrated++
}

func (s *Service) declaredSource(
	capability generation.Capability,
	opaque json.RawMessage,
) string {
	fields := s.sourceFields[capability]
	if len(fields) == 0 || !json.Valid(opaque) {
		return ""
	}
	var document map[string]any
	if json.Unmarshal(opaque, &document) != nil {
		return ""
	}
	for _, field := range fields {
		if source, ok := document[field].(string); ok {
			if source = strings.TrimSpace(source); source != "" {
				return source
			}
		}
	}
	return ""
}

func (s *Service) addSourceUnavailable(
	report *Report,
	taskID generation.TaskID,
	detail string,
) {
	reason, acknowledged := s.acknowledgedSourceLosses[taskID]
	if !acknowledged {
		report.addUnavailable(taskID, detail)
		return
	}
	report.AcknowledgedUnavailable++
	report.Acknowledgements = append(
		report.Acknowledgements,
		Issue{
			TaskID: taskID,
			Detail: detail + "；" + reason,
		},
	)
}

func (r *Report) addUnavailable(
	taskID generation.TaskID,
	detail string,
) {
	r.Unavailable++
	r.Issues = append(r.Issues, Issue{
		TaskID: taskID,
		Detail: detail,
	})
}

func hasRole(assets []generation.Asset, role string) bool {
	for _, asset := range assets {
		if asset.Role == role {
			return true
		}
	}
	return false
}

func nextRoleOrdinal(assets []generation.Asset, role string) int {
	ordinal := 1
	for _, asset := range assets {
		if asset.Role == role && asset.Ordinal >= ordinal {
			ordinal = asset.Ordinal + 1
		}
	}
	return ordinal
}

// Provider refreshes identify artifacts by URL path and replace expiring query
// signatures. A refreshed signature still points to the same source image.
func sameThumbnailSource(left, right string) bool {
	if left == right {
		return true
	}
	a, err := url.Parse(left)
	if err != nil {
		return false
	}
	b, err := url.Parse(right)
	if err != nil {
		return false
	}
	return a.IsAbs() && b.IsAbs() && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host) && a.EscapedPath() == b.EscapedPath()
}

package etl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	sourceLossAcknowledgementFormat = "art-ai-go-source-loss-ack-v1"
	maximumSourceLossManifestBytes  = 8 << 20
)

type sourceLossAcknowledgement struct {
	Format                 string                                `json:"format"`
	LegacyDatabaseSHA256   string                                `json:"legacy_database_sha256"`
	Approved               bool                                  `json:"approved"`
	ApprovedBy             string                                `json:"approved_by"`
	ApprovedAtUTC          string                                `json:"approved_at_utc"`
	ApprovalReason         string                                `json:"approval_reason"`
	ApprovalEvidenceSHA256 string                                `json:"approval_evidence_sha256,omitempty"`
	Tasks                  []sourceLossTaskAcknowledgement       `json:"tasks"`
	InputAssets            []sourceLossInputAssetAcknowledgement `json:"input_assets,omitempty"`
}

type sourceLossTaskAcknowledgement struct {
	TaskID      string `json:"task_id"`
	Capability  string `json:"capability"`
	OfferingID  string `json:"offering_id"`
	ProviderID  string `json:"provider_id"`
	ModelID     string `json:"model_id"`
	IssueCount  int    `json:"issue_count"`
	IssueSHA256 string `json:"issue_sha256"`
	Reason      string `json:"reason"`
}

type sourceLossInputAssetAcknowledgement struct {
	AssetID      string `json:"asset_id"`
	OwnerID      string `json:"owner_id"`
	AssetType    string `json:"asset_type"`
	MediaType    string `json:"media_type"`
	FileSize     int64  `json:"file_size"`
	StoredSHA256 string `json:"stored_sha256"`
	StoragePath  string `json:"storage_path"`
	IssueSHA256  string `json:"issue_sha256"`
	Reason       string `json:"reason"`
}

type loadedSourceLossAcknowledgement struct {
	document sourceLossAcknowledgement
	sha256   string
}

func loadSourceLossAcknowledgement(
	filePath string,
	expectedDatabaseSHA256 string,
) (loadedSourceLossAcknowledgement, error) {
	document, digest, err := readSourceLossAcknowledgement(filePath)
	if err != nil {
		return loadedSourceLossAcknowledgement{},
			fmt.Errorf("读取源损失确认清单: %w", err)
	}
	if err := validateSourceLossAcknowledgement(
		document,
		expectedDatabaseSHA256,
	); err != nil {
		return loadedSourceLossAcknowledgement{}, err
	}
	return loadedSourceLossAcknowledgement{
		document: document,
		sha256:   digest,
	}, nil
}

func readSourceLossAcknowledgement(
	filePath string,
) (sourceLossAcknowledgement, string, error) {
	body, err := readPrivateRegularFile(
		filePath,
		maximumSourceLossManifestBytes,
	)
	if err != nil {
		return sourceLossAcknowledgement{}, "", err
	}
	var document sourceLossAcknowledgement
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return sourceLossAcknowledgement{}, "",
			fmt.Errorf("解析源损失确认清单: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return sourceLossAcknowledgement{}, "", err
	}
	digest := sha256.Sum256(body)
	return document, hex.EncodeToString(digest[:]), nil
}

func validateSourceLossAcknowledgement(
	document sourceLossAcknowledgement,
	expectedDatabaseSHA256 string,
) error {
	if document.Format != sourceLossAcknowledgementFormat {
		return fmt.Errorf(
			"源损失确认清单格式无效: %q",
			document.Format,
		)
	}
	databaseSHA256 := strings.ToLower(strings.TrimSpace(
		document.LegacyDatabaseSHA256,
	))
	if !validSHA256(databaseSHA256) ||
		databaseSHA256 != expectedDatabaseSHA256 {
		return errors.New("源损失确认清单与旧数据库指纹不一致")
	}
	if !document.Approved {
		return errors.New("源损失候选清单尚未获得明确批准")
	}
	if invalidApprovalText(document.ApprovedBy) ||
		invalidApprovalText(document.ApprovalReason) {
		return errors.New("源损失确认清单缺少批准人或批准原因")
	}
	if document.ApprovalEvidenceSHA256 != "" &&
		!validSHA256(strings.ToLower(strings.TrimSpace(
			document.ApprovalEvidenceSHA256,
		))) {
		return errors.New("源损失确认清单的批准证据指纹无效")
	}
	approvedAt := strings.TrimSpace(document.ApprovedAtUTC)
	if !strings.HasSuffix(approvedAt, "Z") {
		return errors.New("源损失确认时间必须是 UTC RFC3339")
	}
	if _, err := time.Parse(time.RFC3339, approvedAt); err != nil {
		return errors.New("源损失确认时间必须是 UTC RFC3339")
	}
	if len(document.Tasks) == 0 && len(document.InputAssets) == 0 {
		return errors.New("源损失确认清单没有任务或输入资产")
	}
	if len(document.Tasks) > 0 {
		if err := validateSourceLossTasks(document.Tasks, true); err != nil {
			return err
		}
	}
	return validateSourceLossInputAssets(document.InputAssets, true)
}

func validateSourceLossInputAssets(
	assets []sourceLossInputAssetAcknowledgement,
	requireReason bool,
) error {
	previousAssetID := ""
	for index, asset := range assets {
		assetID := strings.TrimSpace(asset.AssetID)
		storedSHA256 := strings.ToLower(strings.TrimSpace(asset.StoredSHA256))
		if assetID == "" ||
			asset.FileSize < 0 ||
			(storedSHA256 != "" && !validSHA256(storedSHA256)) ||
			!validSHA256(strings.ToLower(strings.TrimSpace(
				asset.IssueSHA256,
			))) {
			return fmt.Errorf(
				"源损失确认清单 input_assets[%d] 字段无效",
				index,
			)
		}
		if requireReason && invalidApprovalText(asset.Reason) {
			return fmt.Errorf(
				"源损失确认清单 input_assets[%d] 缺少批准原因",
				index,
			)
		}
		if previousAssetID != "" && assetID <= previousAssetID {
			return errors.New(
				"源损失确认清单输入资产必须按 asset_id 严格升序且不得重复",
			)
		}
		previousAssetID = assetID
	}
	return nil
}

func validateSourceLossTasks(
	tasks []sourceLossTaskAcknowledgement,
	requireReason bool,
) error {
	if len(tasks) == 0 {
		return errors.New("源损失确认清单没有任务")
	}
	previousTaskID := ""
	for index, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" ||
			strings.TrimSpace(task.Capability) == "" ||
			task.IssueCount <= 0 ||
			!validSHA256(strings.ToLower(strings.TrimSpace(
				task.IssueSHA256,
			))) {
			return fmt.Errorf(
				"源损失确认清单 tasks[%d] 字段无效",
				index,
			)
		}
		if requireReason && invalidApprovalText(task.Reason) {
			return fmt.Errorf(
				"源损失确认清单 tasks[%d] 缺少批准原因",
				index,
			)
		}
		if previousTaskID != "" && taskID <= previousTaskID {
			return errors.New(
				"源损失确认清单任务必须按 task_id 严格升序且不得重复",
			)
		}
		previousTaskID = taskID
	}
	return nil
}

func applySourceLossAcknowledgement(
	inputReport *inputAssetMigrationReport,
	outputReport *taskOutputMigrationReport,
	loaded loadedSourceLossAcknowledgement,
) error {
	actualByTaskID := make(
		map[string]taskOutputSourceLoss,
		len(outputReport.sourceLosses),
	)
	for _, loss := range outputReport.sourceLosses {
		if _, exists := actualByTaskID[loss.TaskID]; exists {
			return fmt.Errorf(
				"源损失确认清单与实际问题不一致: task_id=%q 重复",
				loss.TaskID,
			)
		}
		actualByTaskID[loss.TaskID] = loss
	}
	acknowledgedIssues := 0
	for _, expected := range loaded.document.Tasks {
		actual, exists := actualByTaskID[expected.TaskID]
		if !exists ||
			actual.Capability != expected.Capability ||
			actual.OfferingID != expected.OfferingID ||
			actual.ProviderID != expected.ProviderID ||
			actual.ModelID != expected.ModelID ||
			actual.IssueCount != expected.IssueCount ||
			actual.IssueSHA256 != expected.IssueSHA256 {
			return fmt.Errorf(
				"源损失确认清单与实际问题不一致: task_id=%q",
				expected.TaskID,
			)
		}
		acknowledgedIssues += actual.IssueCount
	}
	actualByAssetID := make(
		map[string]inputAssetSourceLoss,
		len(inputReport.sourceLosses),
	)
	for _, loss := range inputReport.sourceLosses {
		if _, exists := actualByAssetID[loss.AssetID]; exists {
			return fmt.Errorf(
				"源损失确认清单与实际问题不一致: asset_id=%q 重复",
				loss.AssetID,
			)
		}
		actualByAssetID[loss.AssetID] = loss
	}
	for _, expected := range loaded.document.InputAssets {
		actual, exists := actualByAssetID[expected.AssetID]
		if !exists ||
			actual.OwnerID != expected.OwnerID ||
			actual.AssetType != expected.AssetType ||
			actual.MediaType != expected.MediaType ||
			actual.FileSize != expected.FileSize ||
			actual.StoredSHA256 != expected.StoredSHA256 ||
			actual.StoragePath != expected.StoragePath ||
			actual.IssueSHA256 != expected.IssueSHA256 {
			return fmt.Errorf(
				"源损失确认清单与实际问题不一致: asset_id=%q",
				expected.AssetID,
			)
		}
	}
	inputReport.acknowledgedAssets = len(loaded.document.InputAssets)
	inputReport.acknowledgementSHA = loaded.sha256
	outputReport.acknowledgedTasks = len(loaded.document.Tasks)
	outputReport.acknowledgedIssues = acknowledgedIssues
	outputReport.acknowledgementSHA = loaded.sha256
	return nil
}

func writeSourceLossTemplate(
	filePath string,
	databaseSHA256 string,
	taskLosses []taskOutputSourceLoss,
	inputAssetLosses []inputAssetSourceLoss,
) error {
	if _, err := os.Lstat(filePath); err == nil {
		return errors.New("源损失候选清单已存在，拒绝覆盖")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查源损失候选清单: %w", err)
	}
	sortedLosses := append([]taskOutputSourceLoss(nil), taskLosses...)
	sort.Slice(sortedLosses, func(left, right int) bool {
		return sortedLosses[left].TaskID < sortedLosses[right].TaskID
	})
	tasks := make(
		[]sourceLossTaskAcknowledgement,
		0,
		len(sortedLosses),
	)
	for _, loss := range sortedLosses {
		tasks = append(tasks, sourceLossTaskAcknowledgement{
			TaskID:      loss.TaskID,
			Capability:  loss.Capability,
			OfferingID:  loss.OfferingID,
			ProviderID:  loss.ProviderID,
			ModelID:     loss.ModelID,
			IssueCount:  loss.IssueCount,
			IssueSHA256: loss.IssueSHA256,
		})
	}
	sortedInputAssetLosses := append(
		[]inputAssetSourceLoss(nil),
		inputAssetLosses...,
	)
	sort.Slice(sortedInputAssetLosses, func(left, right int) bool {
		return sortedInputAssetLosses[left].AssetID <
			sortedInputAssetLosses[right].AssetID
	})
	inputAssets := make(
		[]sourceLossInputAssetAcknowledgement,
		0,
		len(sortedInputAssetLosses),
	)
	for _, loss := range sortedInputAssetLosses {
		inputAssets = append(inputAssets, sourceLossInputAssetAcknowledgement{
			AssetID:      loss.AssetID,
			OwnerID:      loss.OwnerID,
			AssetType:    loss.AssetType,
			MediaType:    loss.MediaType,
			FileSize:     loss.FileSize,
			StoredSHA256: loss.StoredSHA256,
			StoragePath:  loss.StoragePath,
			IssueSHA256:  loss.IssueSHA256,
		})
	}
	document := sourceLossAcknowledgement{
		Format:               sourceLossAcknowledgementFormat,
		LegacyDatabaseSHA256: databaseSHA256,
		Tasks:                tasks,
		InputAssets:          inputAssets,
	}
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("编码源损失候选清单: %w", err)
	}
	body = append(body, '\n')
	if err := writePrivateFile(filePath, body); err != nil {
		return err
	}
	return nil
}

func legacyDatabaseSHA256(filePath string) (string, error) {
	body, _, err := openVerifiedRegularFile(filePath)
	if err != nil {
		return "", fmt.Errorf("打开旧数据库计算指纹: %w", err)
	}
	defer func() { _ = body.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, body); err != nil {
		return "", fmt.Errorf("计算旧数据库指纹: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func readPrivateRegularFile(filePath string, maximumBytes int64) ([]byte, error) {
	body, size, err := openVerifiedRegularFile(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	if bodyInfo, err := body.Stat(); err != nil {
		return nil, errors.New("无法检查确认清单权限")
	} else if bodyInfo.Mode().Perm()&0o077 != 0 ||
		bodyInfo.Mode().Perm()&0o400 == 0 {
		return nil, errors.New("确认清单权限必须仅允许 owner 读取")
	}
	if size <= 0 || size > maximumBytes {
		return nil, errors.New("确认清单大小无效")
	}
	content, err := io.ReadAll(io.LimitReader(body, maximumBytes+1))
	if err != nil {
		return nil, errors.New("确认清单无法读取")
	}
	if int64(len(content)) != size {
		return nil, errors.New("确认清单读取期间发生变化")
	}
	return content, nil
}

func openVerifiedRegularFile(filePath string) (*os.File, int64, error) {
	before, err := os.Lstat(filePath)
	if err != nil || !before.Mode().IsRegular() {
		return nil, 0, errors.New("文件不存在或不是普通文件")
	}
	body, err := os.Open(filePath)
	if err != nil {
		return nil, 0, errors.New("文件不可读")
	}
	after, err := body.Stat()
	if err != nil ||
		!after.Mode().IsRegular() ||
		!os.SameFile(before, after) {
		_ = body.Close()
		return nil, 0, errors.New("文件读取期间发生变化")
	}
	return body, after.Size(), nil
}

func writePrivateFile(filePath string, body []byte) error {
	directory := filepath.Dir(filePath)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("创建源损失候选清单目录: %w", err)
	}
	file, err := os.CreateTemp(directory, ".source-loss-*")
	if err != nil {
		return fmt.Errorf("创建源损失候选清单临时文件: %w", err)
	}
	tempPath := file.Name()
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("设置源损失候选清单权限: %w", err)
	}
	if _, err := file.Write(body); err != nil {
		return fmt.Errorf("写入源损失候选清单: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步源损失候选清单: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭源损失候选清单: %w", err)
	}
	if err := os.Link(tempPath, filePath); err != nil {
		return fmt.Errorf("提交源损失候选清单: %w", err)
	}
	_ = os.Remove(tempPath)
	committed = true
	return nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("源损失确认清单包含多余 JSON 内容")
	}
	return nil
}

func invalidApprovalText(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" ||
		strings.HasPrefix(strings.ToUpper(value), "TODO")
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

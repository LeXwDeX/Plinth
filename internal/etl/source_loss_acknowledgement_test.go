package etl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceLossAcknowledgementRejectsBroadPermissionsAndSymlink(
	t *testing.T,
) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	if err := os.WriteFile(databasePath, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	databaseSHA256, err := legacyDatabaseSHA256(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	document := validSourceLossTestDocument(databaseSHA256)
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), "ack.json")
	if err := os.WriteFile(manifestPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestPath, 0o644); err != nil {
		t.Fatal(err)
	}
	manifestInfo, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifestInfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("宽权限确认清单权限不符合测试前提: got %04o, want %04o", got, 0o644)
	}
	if _, err := loadSourceLossAcknowledgement(
		manifestPath,
		databaseSHA256,
	); err == nil || !strings.Contains(err.Error(), "权限") {
		t.Fatalf("宽权限确认清单未被拒绝: %v", err)
	}
	if err := os.Chmod(manifestPath, 0o600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(t.TempDir(), "ack-link.json")
	if err := os.Symlink(manifestPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSourceLossAcknowledgement(
		symlinkPath,
		databaseSHA256,
	); err == nil || !strings.Contains(err.Error(), "普通文件") {
		t.Fatalf("符号链接确认清单未被拒绝: %v", err)
	}
}

func TestSourceLossAcknowledgementRejectsUnknownFieldsAndDatabaseMismatch(
	t *testing.T,
) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	if err := os.WriteFile(databasePath, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	databaseSHA256, err := legacyDatabaseSHA256(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	document := validSourceLossTestDocument(databaseSHA256)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var withUnknown map[string]any
	if err := json.Unmarshal(raw, &withUnknown); err != nil {
		t.Fatal(err)
	}
	withUnknown["unexpected"] = true
	raw, err = json.Marshal(withUnknown)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), "ack.json")
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSourceLossAcknowledgement(
		manifestPath,
		databaseSHA256,
	); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("未知字段未被拒绝: %v", err)
	}

	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSourceLossAcknowledgement(
		manifestPath,
		strings.Repeat("0", 64),
	); err == nil || !strings.Contains(err.Error(), "数据库指纹") {
		t.Fatalf("数据库指纹错配未被拒绝: %v", err)
	}
}

func TestSourceLossAcknowledgementAcceptsLargeExplicitTaskSet(t *testing.T) {
	databaseSHA256 := strings.Repeat("b", 64)
	document := validSourceLossTestDocument(databaseSHA256)
	document.Tasks = make([]sourceLossTaskAcknowledgement, 6000)
	for index := range document.Tasks {
		document.Tasks[index] = sourceLossTaskAcknowledgement{
			TaskID:      fmt.Sprintf("task-%06d", index),
			Capability:  "text-to-image",
			OfferingID:  "legacy/meshy/text-to-image",
			ProviderID:  "meshy",
			IssueCount:  1,
			IssueSHA256: strings.Repeat("a", 64),
			Reason:      "旧缓存与审计副本均不存在",
		}
	}
	manifestPath := filepath.Join(t.TempDir(), "large-ack.json")
	writeSourceLossTestDocument(t, manifestPath, document)
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 1<<20 {
		t.Fatalf("测试清单未超过旧 1 MiB 限制: %d", info.Size())
	}
	if _, err := loadSourceLossAcknowledgement(
		manifestPath,
		databaseSHA256,
	); err != nil {
		t.Fatalf("有效大清单不应被拒绝: %v", err)
	}
}

func TestSourceLossTemplateRefusesToOverwriteExistingFile(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "source-loss.json")
	losses := []taskOutputSourceLoss{{
		TaskID:      "task-a",
		Capability:  "text-to-image",
		OfferingID:  "offering-a",
		ProviderID:  "provider-a",
		ModelID:     "model-a",
		IssueCount:  1,
		IssueSHA256: strings.Repeat("a", 64),
	}}
	if err := writeSourceLossTemplate(
		templatePath,
		strings.Repeat("b", 64),
		losses,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSourceLossTemplate(
		templatePath,
		strings.Repeat("c", 64),
		nil,
		nil,
	); err == nil || !strings.Contains(err.Error(), "拒绝覆盖") {
		t.Fatalf("已有候选清单未被保护: %v", err)
	}
	after, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("重复生成改变了已有候选清单")
	}
}

func TestSourceLossTemplateIncludesSortedInputAssets(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "source-loss.json")
	losses := []inputAssetSourceLoss{
		validInputAssetSourceLoss("asset-b"),
		validInputAssetSourceLoss("asset-a"),
	}
	if err := writeSourceLossTemplate(
		templatePath,
		strings.Repeat("b", 64),
		nil,
		losses,
	); err != nil {
		t.Fatal(err)
	}
	document, _, err := readSourceLossAcknowledgement(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.InputAssets) != 2 ||
		document.InputAssets[0].AssetID != "asset-a" ||
		document.InputAssets[1].AssetID != "asset-b" {
		t.Fatalf("输入资产候选未按 asset_id 排序: %#v", document.InputAssets)
	}
	for _, asset := range document.InputAssets {
		if asset.Reason != "" {
			t.Fatalf("候选清单预填了批准原因: %#v", asset)
		}
	}
}

func TestApplySourceLossAcknowledgementMatchesInputAssetFingerprint(
	t *testing.T,
) {
	loss := validInputAssetSourceLoss("asset-a")
	document := validSourceLossTestDocument(strings.Repeat("b", 64))
	document.InputAssets = []sourceLossInputAssetAcknowledgement{
		inputAssetAcknowledgement(loss, "原字节在冻结源中不存在"),
	}
	loaded := loadedSourceLossAcknowledgement{
		document: document,
		sha256:   strings.Repeat("c", 64),
	}
	inputReport := inputAssetMigrationReport{
		unavailable:  []string{"asset-a: missing", "asset-z: parse failed"},
		sourceLosses: []inputAssetSourceLoss{loss},
	}
	outputReport := taskOutputMigrationReport{
		sourceLosses: []taskOutputSourceLoss{validTaskOutputSourceLoss()},
	}
	if err := applySourceLossAcknowledgement(
		&inputReport,
		&outputReport,
		loaded,
	); err != nil {
		t.Fatal(err)
	}
	if inputReport.acknowledgedAssets != 1 ||
		inputReport.unacknowledgedIssueCount() != 1 ||
		inputReport.acknowledgementSHA != loaded.sha256 {
		t.Fatalf("输入资产批准未正确应用: %#v", inputReport)
	}

	document.InputAssets[0].IssueSHA256 = strings.Repeat("d", 64)
	loaded.document = document
	if err := applySourceLossAcknowledgement(
		&inputReport,
		&outputReport,
		loaded,
	); err == nil || !strings.Contains(err.Error(), `asset_id="asset-a"`) {
		t.Fatalf("输入资产问题指纹篡改未被拒绝: %v", err)
	}
}

func TestRebindSourceLossAcknowledgementRequiresAnExactCandidate(t *testing.T) {
	root := t.TempDir()
	approvedPath := filepath.Join(root, "approved.json")
	candidatePath := filepath.Join(root, "candidate.json")
	outputPath := filepath.Join(root, "final-approved.json")
	approved := validSourceLossTestDocument(strings.Repeat("a", 64))
	approved.InputAssets = []sourceLossInputAssetAcknowledgement{
		inputAssetAcknowledgement(
			validInputAssetSourceLoss("asset-a"),
			"原字节在冻结源中不存在",
		),
	}
	writeSourceLossTestDocument(t, approvedPath, approved)

	candidate := approved
	candidate.Tasks = append(
		[]sourceLossTaskAcknowledgement(nil),
		approved.Tasks...,
	)
	candidate.InputAssets = append(
		[]sourceLossInputAssetAcknowledgement(nil),
		approved.InputAssets...,
	)
	candidate.LegacyDatabaseSHA256 = strings.Repeat("b", 64)
	candidate.Approved = false
	candidate.ApprovedBy = ""
	candidate.ApprovedAtUTC = ""
	candidate.ApprovalReason = ""
	candidate.ApprovalEvidenceSHA256 = ""
	for index := range candidate.Tasks {
		candidate.Tasks[index].Reason = ""
	}
	for index := range candidate.InputAssets {
		candidate.InputAssets[index].Reason = ""
	}
	writeSourceLossTestDocument(t, candidatePath, candidate)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main([]string{
		"rebind-source-loss-acknowledgement",
		"--approved", approvedPath,
		"--candidate", candidatePath,
		"--output", outputPath,
	}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf(
			"重新绑定失败: exit=%d stdout=%q stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
	loaded, err := loadSourceLossAcknowledgement(
		outputPath,
		candidate.LegacyDatabaseSHA256,
	)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.document.ApprovedBy != approved.ApprovedBy ||
		loaded.document.ApprovalReason != approved.ApprovalReason ||
		loaded.document.Tasks[0].Reason != approved.Tasks[0].Reason ||
		loaded.document.InputAssets[0].Reason !=
			approved.InputAssets[0].Reason {
		t.Fatalf("批准信息未原样继承: %#v", loaded.document)
	}
	approvedBody, err := os.ReadFile(approvedPath)
	if err != nil {
		t.Fatal(err)
	}
	evidenceDigest := sha256.Sum256(approvedBody)
	if loaded.document.ApprovalEvidenceSHA256 !=
		hex.EncodeToString(evidenceDigest[:]) {
		t.Fatalf(
			"批准证据指纹=%q",
			loaded.document.ApprovalEvidenceSHA256,
		)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("最终确认清单权限=%o want=600", info.Mode().Perm())
	}

	tamperedPath := filepath.Join(root, "tampered.json")
	tamperedOutput := filepath.Join(root, "tampered-approved.json")
	candidate.Tasks[0].IssueSHA256 = strings.Repeat("c", 64)
	writeSourceLossTestDocument(t, tamperedPath, candidate)
	stdout.Reset()
	stderr.Reset()
	exitCode = Main([]string{
		"rebind-source-loss-acknowledgement",
		"--approved", approvedPath,
		"--candidate", tamperedPath,
		"--output", tamperedOutput,
	}, &stdout, &stderr)
	if exitCode == 0 ||
		!strings.Contains(stderr.String(), "逐项完全一致") {
		t.Fatalf(
			"篡改候选未被拒绝: exit=%d stderr=%q",
			exitCode,
			stderr.String(),
		)
	}
	if _, err := os.Stat(tamperedOutput); !os.IsNotExist(err) {
		t.Fatalf("失败时生成了输出: %v", err)
	}

	tamperedAssetPath := filepath.Join(root, "tampered-asset.json")
	tamperedAssetOutput := filepath.Join(root, "tampered-asset-approved.json")
	candidate.Tasks[0].IssueSHA256 = approved.Tasks[0].IssueSHA256
	candidate.InputAssets[0].StoragePath = "different/path"
	writeSourceLossTestDocument(t, tamperedAssetPath, candidate)
	stdout.Reset()
	stderr.Reset()
	exitCode = Main([]string{
		"rebind-source-loss-acknowledgement",
		"--approved", approvedPath,
		"--candidate", tamperedAssetPath,
		"--output", tamperedAssetOutput,
	}, &stdout, &stderr)
	if exitCode == 0 ||
		!strings.Contains(stderr.String(), "逐项完全一致") {
		t.Fatalf(
			"输入资产篡改候选未被拒绝: exit=%d stderr=%q",
			exitCode,
			stderr.String(),
		)
	}
}

func TestRebindSourceLossAcknowledgementRejectsApprovedCandidate(
	t *testing.T,
) {
	root := t.TempDir()
	approvedPath := filepath.Join(root, "approved.json")
	candidatePath := filepath.Join(root, "candidate.json")
	outputPath := filepath.Join(root, "output.json")
	document := validSourceLossTestDocument(strings.Repeat("a", 64))
	writeSourceLossTestDocument(t, approvedPath, document)
	document.LegacyDatabaseSHA256 = strings.Repeat("b", 64)
	writeSourceLossTestDocument(t, candidatePath, document)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Main([]string{
		"rebind-source-loss-acknowledgement",
		"--approved", approvedPath,
		"--candidate", candidatePath,
		"--output", outputPath,
	}, &stdout, &stderr)
	if exitCode == 0 ||
		!strings.Contains(stderr.String(), "候选清单必须保持未批准") {
		t.Fatalf(
			"已批准候选未被拒绝: exit=%d stderr=%q",
			exitCode,
			stderr.String(),
		)
	}
}

func writeSourceLossTestDocument(
	t *testing.T,
	filePath string,
	document sourceLossAcknowledgement,
) {
	t.Helper()
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	if err := os.WriteFile(filePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func validSourceLossTestDocument(
	databaseSHA256 string,
) sourceLossAcknowledgement {
	return sourceLossAcknowledgement{
		Format:               sourceLossAcknowledgementFormat,
		LegacyDatabaseSHA256: databaseSHA256,
		Approved:             true,
		ApprovedBy:           "migration-owner",
		ApprovedAtUTC:        "2026-07-29T00:00:00Z",
		ApprovalReason:       "确认旧产物字节不可恢复",
		Tasks: []sourceLossTaskAcknowledgement{{
			TaskID:      "task-a",
			Capability:  "text-to-image",
			OfferingID:  "offering-a",
			ProviderID:  "provider-a",
			ModelID:     "model-a",
			IssueCount:  1,
			IssueSHA256: strings.Repeat("a", 64),
			Reason:      "旧缓存与审计副本均不存在",
		}},
	}
}

func validTaskOutputSourceLoss() taskOutputSourceLoss {
	return taskOutputSourceLoss{
		TaskID:      "task-a",
		Capability:  "text-to-image",
		OfferingID:  "offering-a",
		ProviderID:  "provider-a",
		ModelID:     "model-a",
		IssueCount:  1,
		IssueSHA256: strings.Repeat("a", 64),
	}
}

func validInputAssetSourceLoss(assetID string) inputAssetSourceLoss {
	return newInputAssetSourceLoss(
		assetID,
		"owner-a",
		"image",
		"image/png",
		123,
		strings.Repeat("e", 64),
		"missing/asset.png",
		assetID+": 旧输入资产缺失且未找到 SHA-256 一致副本",
	)
}

func inputAssetAcknowledgement(
	loss inputAssetSourceLoss,
	reason string,
) sourceLossInputAssetAcknowledgement {
	return sourceLossInputAssetAcknowledgement{
		AssetID:      loss.AssetID,
		OwnerID:      loss.OwnerID,
		AssetType:    loss.AssetType,
		MediaType:    loss.MediaType,
		FileSize:     loss.FileSize,
		StoredSHA256: loss.StoredSHA256,
		StoragePath:  loss.StoragePath,
		IssueSHA256:  loss.IssueSHA256,
		Reason:       reason,
	}
}

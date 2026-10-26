package etl

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func runRebindSourceLossAcknowledgement(
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	flags := flag.NewFlagSet(
		"rebind-source-loss-acknowledgement",
		flag.ContinueOnError,
	)
	flags.SetOutput(stderr)
	approvedPath := flags.String(
		"approved",
		"",
		"已经用户明确批准的源损失确认清单",
	)
	candidatePath := flags.String(
		"candidate",
		"",
		"最终停写快照生成的未批准候选清单",
	)
	outputPath := flags.String(
		"output",
		"",
		"绑定最终数据库指纹的新确认清单",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*approvedPath) == "" ||
		strings.TrimSpace(*candidatePath) == "" ||
		strings.TrimSpace(*outputPath) == "" {
		fmt.Fprintln(
			stderr,
			"重新绑定必须指定 --approved、--candidate 与 --output",
		)
		return 2
	}
	if samePath(*approvedPath, *candidatePath) ||
		samePath(*approvedPath, *outputPath) ||
		samePath(*candidatePath, *outputPath) {
		fmt.Fprintln(stderr, "重新绑定的输入与输出路径必须互不相同")
		return 2
	}
	if _, err := os.Lstat(*outputPath); err == nil {
		fmt.Fprintln(stderr, "最终源损失确认清单已存在，拒绝覆盖")
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "检查最终确认清单失败: %v\n", err)
		return 1
	}

	approved, approvedSHA256, err := readSourceLossAcknowledgement(
		*approvedPath,
	)
	if err != nil {
		fmt.Fprintf(stderr, "读取已批准源损失清单失败: %v\n", err)
		return 1
	}
	if err := validateSourceLossAcknowledgement(
		approved,
		approved.LegacyDatabaseSHA256,
	); err != nil {
		fmt.Fprintf(stderr, "已批准源损失清单无效: %v\n", err)
		return 1
	}
	candidate, _, err := readSourceLossAcknowledgement(*candidatePath)
	if err != nil {
		fmt.Fprintf(stderr, "读取最终候选清单失败: %v\n", err)
		return 1
	}
	if err := validateSourceLossCandidate(candidate); err != nil {
		fmt.Fprintf(stderr, "最终候选清单无效: %v\n", err)
		return 1
	}
	if !sameSourceLossTasks(approved.Tasks, candidate.Tasks) ||
		!sameSourceLossInputAssets(
			approved.InputAssets,
			candidate.InputAssets,
		) {
		fmt.Fprintln(
			stderr,
			"最终候选与已批准问题集必须逐项完全一致",
		)
		return 1
	}

	finalDocument := candidate
	finalDocument.Approved = true
	finalDocument.ApprovedBy = approved.ApprovedBy
	finalDocument.ApprovedAtUTC = approved.ApprovedAtUTC
	finalDocument.ApprovalReason = approved.ApprovalReason
	finalDocument.ApprovalEvidenceSHA256 = approvedSHA256
	for index := range finalDocument.Tasks {
		finalDocument.Tasks[index].Reason = approved.Tasks[index].Reason
	}
	for index := range finalDocument.InputAssets {
		finalDocument.InputAssets[index].Reason =
			approved.InputAssets[index].Reason
	}
	body, err := json.MarshalIndent(finalDocument, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "编码最终确认清单失败: %v\n", err)
		return 1
	}
	body = append(body, '\n')
	if err := writePrivateFile(*outputPath, body); err != nil {
		fmt.Fprintf(stderr, "写入最终确认清单失败: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "最终源损失确认清单已绑定: %s\n", *outputPath)
	return 0
}

func validateSourceLossCandidate(
	document sourceLossAcknowledgement,
) error {
	if document.Format != sourceLossAcknowledgementFormat {
		return fmt.Errorf("候选清单格式无效: %q", document.Format)
	}
	databaseSHA256 := strings.ToLower(strings.TrimSpace(
		document.LegacyDatabaseSHA256,
	))
	if !validSHA256(databaseSHA256) {
		return errors.New("候选清单数据库指纹无效")
	}
	if document.Approved ||
		strings.TrimSpace(document.ApprovedBy) != "" ||
		strings.TrimSpace(document.ApprovedAtUTC) != "" ||
		strings.TrimSpace(document.ApprovalReason) != "" ||
		strings.TrimSpace(document.ApprovalEvidenceSHA256) != "" {
		return errors.New("候选清单必须保持未批准")
	}
	if len(document.Tasks) == 0 && len(document.InputAssets) == 0 {
		return errors.New("候选清单没有任务或输入资产")
	}
	if len(document.Tasks) > 0 {
		if err := validateSourceLossTasks(document.Tasks, false); err != nil {
			return err
		}
	}
	if err := validateSourceLossInputAssets(
		document.InputAssets,
		false,
	); err != nil {
		return err
	}
	for index, task := range document.Tasks {
		if strings.TrimSpace(task.Reason) != "" {
			return fmt.Errorf(
				"候选清单 tasks[%d] 不得预填批准原因",
				index,
			)
		}
	}
	for index, asset := range document.InputAssets {
		if strings.TrimSpace(asset.Reason) != "" {
			return fmt.Errorf(
				"候选清单 input_assets[%d] 不得预填批准原因",
				index,
			)
		}
	}
	return nil
}

func sameSourceLossTasks(
	approved []sourceLossTaskAcknowledgement,
	candidate []sourceLossTaskAcknowledgement,
) bool {
	if len(approved) != len(candidate) {
		return false
	}
	for index := range approved {
		left := approved[index]
		right := candidate[index]
		if left.TaskID != right.TaskID ||
			left.Capability != right.Capability ||
			left.OfferingID != right.OfferingID ||
			left.ProviderID != right.ProviderID ||
			left.ModelID != right.ModelID ||
			left.IssueCount != right.IssueCount ||
			left.IssueSHA256 != right.IssueSHA256 {
			return false
		}
	}
	return true
}

func sameSourceLossInputAssets(
	approved []sourceLossInputAssetAcknowledgement,
	candidate []sourceLossInputAssetAcknowledgement,
) bool {
	if len(approved) != len(candidate) {
		return false
	}
	for index := range approved {
		left := approved[index]
		right := candidate[index]
		if left.AssetID != right.AssetID ||
			left.OwnerID != right.OwnerID ||
			left.AssetType != right.AssetType ||
			left.MediaType != right.MediaType ||
			left.FileSize != right.FileSize ||
			left.StoredSHA256 != right.StoredSHA256 ||
			left.StoragePath != right.StoragePath ||
			left.IssueSHA256 != right.IssueSHA256 {
			return false
		}
	}
	return true
}

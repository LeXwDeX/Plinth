package etl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const completenessManifestFormat = "art-ai-etl-completeness-manifest-v1"

type completenessManifest struct {
	Format               string             `json:"format"`
	LegacyDatabaseSHA256 string             `json:"legacy_database_sha256"`
	ImageTag             string             `json:"image_tag,omitempty"`
	SourceCommit         string             `json:"source_commit,omitempty"`
	GeneratedAtUTC       string             `json:"generated_at_utc"`
	Domains              []manifestDomain   `json:"domains"`
	Credits              manifestCredits    `json:"credits"`
	CAS                  manifestCAS        `json:"cas"`
	SourceLoss           manifestSourceLoss `json:"source_loss"`
	Overall              manifestOverall    `json:"overall"`
}

type manifestDomain struct {
	Domain         string `json:"domain"`
	SourceCount    int    `json:"source_count"`
	LoadedCount    int    `json:"loaded_count"`
	ImportedCount  int    `json:"imported_count"`
	TargetCount    int    `json:"target_count"`
	ArchivedCount  int    `json:"archived_count,omitempty"`
	SkippedCount   int    `json:"skipped_count"`
	ReadGap        int    `json:"read_gap"`
	UnexplainedGap int    `json:"unexplained_gap"`
	Status         string `json:"status"`
}

// A business skip describes an already read source row. Idempotent write skips
// are not included: imported/target counts also cover prior persisted imports.
func (domain manifestDomain) completeDisposition() bool {
	if domain.SourceCount < 0 || domain.LoadedCount < 0 || domain.ImportedCount < 0 ||
		domain.TargetCount < 0 || domain.SkippedCount < 0 || domain.ArchivedCount < 0 ||
		domain.SourceCount != domain.LoadedCount || domain.ImportedCount != domain.TargetCount ||
		domain.ArchivedCount > domain.ImportedCount {
		return false
	}
	switch domain.Domain {
	case "task_outputs":
		// Output skips are problem details, so they are not a source-row partition.
		return domain.ImportedCount <= domain.LoadedCount
	case "task_cache", "task_input_snapshot":
		return domain.LoadedCount == domain.ImportedCount && domain.SkippedCount == 0
	default:
		return domain.LoadedCount == domain.ImportedCount+domain.SkippedCount
	}
}

type manifestCredits struct {
	LoadedSum int  `json:"loaded_sum"`
	SourceSum int  `json:"source_sum"`
	TargetSum int  `json:"target_sum"`
	Equal     bool `json:"equal"`
}

type manifestCAS struct {
	References     int `json:"references"`
	Readable       int `json:"readable"`
	SHA256Sampled  int `json:"sha256_sampled"`
	SHA256Mismatch int `json:"sha256_mismatch"`
}

type manifestSourceLoss struct {
	CandidateCount int `json:"candidate_count"`
	Acknowledged   int `json:"acknowledged"`
	Unacknowledged int `json:"unacknowledged"`
}

type manifestOverall struct {
	FailureCount int    `json:"failure_count"`
	Conclusion   string `json:"conclusion"`
}

func writeCompletenessManifest(path string, manifest completenessManifest) error {
	manifest.Format = completenessManifestFormat
	manifest.GeneratedAtUTC = time.Now().UTC().Format(time.RFC3339Nano)
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 ETL manifest: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("创建 manifest 目录: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".etl-manifest-*")
	if err != nil {
		return fmt.Errorf("创建 manifest 临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("设置 manifest 权限: %w", err)
	}
	if _, err := temporary.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("写入 manifest: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("同步 manifest: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭 manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("提交 manifest: %w", err)
	}
	committed = true
	return nil
}

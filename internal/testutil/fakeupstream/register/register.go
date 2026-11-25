// Package register is the fake verification register (#573): one record per
// fake behavior that says whether the behavior was checked against a real
// upstream call (a committed, sanitized sample), built from the official
// documentation, or is a known gap.
//
// The data is register.json in this directory, read with Load. docs/fake-register.md
// is generated from it (Markdown) and is never edited by hand; regenerate with
//
//	UPDATE_FAKE_REGISTER=1 go test ./internal/testutil/fakeupstream/register -run Markdown
//
// The tests of this package keep the register complete and current: every route
// of every registered fake version has at least one entry, a real entry names
// committed samples whose metadata agrees with it and that exercise its routes, a
// constructed entry names its basis and the date it was checked, and a gap names
// its plan and issue.
//
// The register sits beside the wire coverage matrix
// (internal/testutil/wirefixture, TestWireCoverageMatrix), which reports per
// Catalog Offering whether a real sample exists for an endpoint. The matrix
// keys units by endpoint, so a unit lists every Offering that shares its
// endpoint with a sampled model; the Offering rows take only the samples that
// requested their own model in the Provider's model field (#575). The register
// records such behaviors per model and names the matrix unit it corrects
// (Entry.Correction). The matrix
// generation itself is not changed by the register.
package register

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Category is the kind of behavior an entry records.
type Category string

const (
	CategoryRequestValidation    Category = "request-validation"
	CategoryResponseShape        Category = "response-shape"
	CategoryStateMachine         Category = "state-machine"
	CategoryErrorCodeAndText     Category = "error-code-and-text"
	CategoryArtifactShape        Category = "artifact-shape"
	CategoryPricing              Category = "pricing"
	CategoryRateLimitConcurrency Category = "rate-limit-concurrency"
	CategoryOther                Category = "other"
)

// Categories lists the categories in the order the Markdown uses.
var Categories = []Category{
	CategoryRequestValidation, CategoryResponseShape, CategoryStateMachine, CategoryErrorCodeAndText,
	CategoryArtifactShape, CategoryPricing, CategoryRateLimitConcurrency, CategoryOther,
}

// Status says what an entry's behavior rests on.
type Status string

const (
	// StatusReal: the fake behavior agrees with committed samples captured from
	// the official API (Entry.Samples).
	StatusReal Status = "real"
	// StatusConstructed: no committed real sample shows the behavior; it is
	// built from the sources in Entry.Basis, checked on Entry.CheckedAt. A
	// constructed entry is not "aligned".
	StatusConstructed Status = "constructed"
	// StatusGap: a known difference or missing piece, with a plan and issues.
	StatusGap Status = "gap"
)

// Statuses lists the statuses in the order the Markdown uses.
var Statuses = []Status{StatusReal, StatusConstructed, StatusGap}

// Sample is one committed wirefixture candidate a real entry rests on. The
// fields repeat the candidate's .meta.json so a re-capture that changes the
// metadata also has to update the register.
type Sample struct {
	// File is the candidate path relative to the repository root.
	File string `json:"file"`
	// CapturedAt is the meta's captured_at_utc.
	CapturedAt string `json:"captured_at"`
	// Provenance is the meta's provenance (captured, or derived when only
	// request media was replaced).
	Provenance string `json:"provenance"`
	// AppRevision is the meta's source_revision ("unknown" when the capture did
	// not record it).
	AppRevision string `json:"app_revision"`
	// CatalogRevision is the meta's schema_revision, else "sha256:" plus its
	// catalog_sha256, else "unknown".
	CatalogRevision string `json:"catalog_revision"`
}

// Basis is one source a constructed entry is built from: an official page (URL
// and section) or a named observation that was not committed as a sample.
type Basis struct {
	Source  string `json:"source"`
	Section string `json:"section,omitempty"`
}

// Correction names the wire coverage unit an entry corrects: the matrix lists
// Offering under Unit with real (or derived) evidence because the unit's
// endpoint is shared, while the Offering's own behavior has no real sample.
type Correction struct {
	Unit     string `json:"unit"`
	Offering string `json:"offering"`
	Reason   string `json:"reason"`
}

// Entry is one registered fake behavior.
type Entry struct {
	// ID is unique and stable ("<provider>-<api>/<topic>").
	ID string `json:"id"`
	// Provider and API name the fake version (fakeupstream.Version).
	Provider string `json:"provider"`
	API      string `json:"api"`
	// Endpoints are Endpoint.Key() values of that version ("METHOD /path").
	Endpoints []string `json:"endpoints"`
	Category  Category `json:"category"`
	Status    Status   `json:"status"`
	// Behavior says, in a sentence, what the fake does.
	Behavior string `json:"behavior"`

	// Samples (real).
	Samples []Sample `json:"samples,omitempty"`
	// Basis, CheckedAt and WhyNotReal (constructed; a gap may carry them too).
	Basis      []Basis `json:"basis,omitempty"`
	CheckedAt  string  `json:"checked_at,omitempty"`
	WhyNotReal string  `json:"why_not_real,omitempty"`
	// Plan and Issues (gap).
	Plan   string `json:"plan,omitempty"`
	Issues []int  `json:"issues,omitempty"`

	Correction *Correction `json:"wire_coverage_correction,omitempty"`
	Note       string      `json:"note,omitempty"`
}

// Version is "provider/api".
func (e Entry) Version() string { return e.Provider + "/" + e.API }

// LatestCapture is the newest CapturedAt of the entry's samples.
func (e Entry) LatestCapture() string {
	latest := ""
	for _, sample := range e.Samples {
		if sample.CapturedAt > latest {
			latest = sample.CapturedAt
		}
	}
	return latest
}

// Register is the file's content.
type Register struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

//go:embed register.json
var registerJSON []byte

// Load decodes the committed register; unknown fields are an error.
func Load() (Register, error) {
	return Decode(registerJSON)
}

// Decode reads a register document.
func Decode(raw []byte) (Register, error) {
	var register Register
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&register); err != nil {
		return Register{}, fmt.Errorf("register: %w", err)
	}
	return register, nil
}

// Summary is the per-version count of one register.
type Summary struct {
	Version    string
	Counts     map[Status]int
	Total      int
	LatestReal string
}

// Summaries counts entries per version, in the order versions first appear.
func (r Register) Summaries() []Summary {
	var order []string
	byVersion := map[string]*Summary{}
	for _, entry := range r.Entries {
		summary, ok := byVersion[entry.Version()]
		if !ok {
			summary = &Summary{Version: entry.Version(), Counts: map[Status]int{}}
			byVersion[entry.Version()] = summary
			order = append(order, entry.Version())
		}
		summary.Counts[entry.Status]++
		summary.Total++
		if entry.Status == StatusReal && entry.LatestCapture() > summary.LatestReal {
			summary.LatestReal = entry.LatestCapture()
		}
	}
	out := make([]Summary, 0, len(order))
	for _, version := range order {
		out = append(out, *byVersion[version])
	}
	return out
}

// Markdown renders docs/fake-register.md.
func (r Register) Markdown() []byte {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# Fake 验证登记表\n\n")
	w("> 本文件由 `internal/testutil/fakeupstream/register/register.json` 生成，不要手改。改登记数据后重新生成：\n>\n")
	w("> `UPDATE_FAKE_REGISTER=1 go test ./internal/testutil/fakeupstream/register -run Markdown`\n\n")
	w("每条记录一个 fake 行为（Provider / API / 端点 + 行为类别）以及它的依据：\n\n")
	w("- `real`：fake 的该行为与仓库内已提交的脱敏真实样本一致；列出样本文件、真实调用日期（样本 meta 的 `captured_at_utc`）、`app_revision` 与 `catalog_revision`（meta 未记录时写 `unknown`）。\n")
	w("- `constructed`：没有真实样本，按所列官方文档或未入库的观测构造，写明核对日期与没有真实样本的原因。`constructed` 不等于已对齐。\n")
	w("- `gap`：已知缺口或差异，写明计划与 Issue。\n\n")
	w("登记表与 wire coverage 矩阵（`internal/testutil/wirefixture/testdata/wire-coverage.generated.json`）并存：矩阵按端点记 Offering 有无真实样本，单元的证据由共享端点的 Offering 共用；样本请求体在 Provider 声明的模型字段（`model`、Meshy 的 `ai_model` 等）里写了模型时，Offering 行只取请求了自身模型的样本（#575），但单元本身仍列出这些 Offering 并显示全部样本的最高证据。登记表按行为与模型记录，并在「按行为纠正 wire coverage」一节列出单元因共享端点显示为 real、而该 Offering 自己的行为没有真实样本的条目。矩阵的生成规则不因登记表改变。\n\n")
	w("测试（`internal/testutil/fakeupstream/register`）保证：每个 fake 版本的每个端点至少一条记录；`real` 条目引用的样本存在、能被 wirefixture 读取、日期与来源同样本 meta 一致，并且样本里确有该端点的交换；`constructed` 条目有依据与核对日期；`gap` 条目有计划与 Issue；本文件与数据一致。\n\n")

	w("## 汇总\n\n")
	w("| Provider / API | real | constructed | gap | 合计 | 最近一次真实调用 |\n|---|---|---|---|---|---|\n")
	total := map[Status]int{}
	all := 0
	latest := ""
	for _, summary := range r.Summaries() {
		realDate := summary.LatestReal
		if realDate == "" {
			realDate = "—"
		}
		w("| `%s` | %d | %d | %d | %d | %s |\n", summary.Version, summary.Counts[StatusReal], summary.Counts[StatusConstructed],
			summary.Counts[StatusGap], summary.Total, realDate)
		for _, status := range Statuses {
			total[status] += summary.Counts[status]
		}
		all += summary.Total
		if summary.LatestReal > latest {
			latest = summary.LatestReal
		}
	}
	w("| 合计 | %d | %d | %d | %d | %s |\n\n", total[StatusReal], total[StatusConstructed], total[StatusGap], all, latest)
	w("「最近一次真实调用」取该版本 `real` 条目所引样本中最新的 `captured_at_utc`；未入库的真实观测不计入。\n\n")

	w("## 按行为纠正 wire coverage\n\n")
	w("下列行为在 wire coverage 矩阵里因共享端点显示为有真实证据，登记表按行为记为非 `real`：\n\n")
	w("| 条目 | 矩阵单元 | Offering | 登记状态 | 原因 |\n|---|---|---|---|---|\n")
	for _, entry := range r.Entries {
		if entry.Correction == nil {
			continue
		}
		w("| `%s` | `%s` | `%s` | %s | %s |\n", entry.ID, cell(entry.Correction.Unit), entry.Correction.Offering, entry.Status, cell(entry.Correction.Reason))
	}
	w("\n")

	w("## 明细\n")
	versions := []string{}
	seen := map[string]bool{}
	for _, entry := range r.Entries {
		if !seen[entry.Version()] {
			seen[entry.Version()] = true
			versions = append(versions, entry.Version())
		}
	}
	for _, version := range versions {
		w("\n### `%s`\n", version)
		for _, entry := range r.Entries {
			if entry.Version() != version {
				continue
			}
			w("\n#### `%s` · %s · %s\n\n", entry.ID, entry.Category, entry.Status)
			endpoints := make([]string, len(entry.Endpoints))
			for i, endpoint := range entry.Endpoints {
				endpoints[i] = "`" + endpoint + "`"
			}
			w("- 端点：%s\n", strings.Join(endpoints, "、"))
			w("- 行为：%s\n", line(entry.Behavior))
			for _, sample := range entry.Samples {
				w("- 样本：`%s`（%s，%s，app_revision `%s`，catalog_revision `%s`）\n", sample.File, sample.CapturedAt,
					sample.Provenance, sample.AppRevision, sample.CatalogRevision)
			}
			for _, basis := range entry.Basis {
				if basis.Section != "" {
					w("- 依据：%s（%s）\n", line(basis.Source), line(basis.Section))
				} else {
					w("- 依据：%s\n", line(basis.Source))
				}
			}
			if entry.CheckedAt != "" {
				w("- 核对日期：%s\n", entry.CheckedAt)
			}
			if entry.WhyNotReal != "" {
				w("- 没有真实样本的原因：%s\n", line(entry.WhyNotReal))
			}
			if entry.Plan != "" {
				w("- 计划：%s\n", line(entry.Plan))
			}
			if len(entry.Issues) > 0 {
				numbers := append([]int(nil), entry.Issues...)
				sort.Ints(numbers)
				issues := make([]string, len(numbers))
				for i, issue := range numbers {
					issues[i] = fmt.Sprintf("#%d", issue)
				}
				w("- Issue：%s\n", strings.Join(issues, "、"))
			}
			if entry.Correction != nil {
				w("- 纠正 wire coverage：单元 `%s` 下的 `%s`。%s\n", entry.Correction.Unit, entry.Correction.Offering, line(entry.Correction.Reason))
			}
			if entry.Note != "" {
				w("- 备注：%s\n", line(entry.Note))
			}
		}
	}
	return []byte(b.String())
}

func line(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func cell(text string) string {
	return strings.ReplaceAll(line(text), "|", "\\|")
}

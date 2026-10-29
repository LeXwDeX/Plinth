package align

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// Diff is one structural difference between the official API and the fake.
type Diff struct {
	Step   string `json:"step"`
	Aspect string `json:"aspect"`
	Detail string `json:"detail"`
	// Note is the written explanation; empty means the difference is
	// unexplained and the version is not aligned.
	Note string `json:"note,omitempty"`
}

// Key identifies a Diff in a Notes table.
func (d Diff) Key() string { return d.Step + "|" + d.Aspect + "|" + d.Detail }

// Notes maps Diff.Key() to a written explanation.
type Notes map[string]string

// StepReport summarizes one compared step.
type StepReport struct {
	Step       string   `json:"step"`
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Auth       string   `json:"auth"`
	Official   int      `json:"official_status"`
	Fake       int      `json:"fake_status"`
	Fields     int      `json:"official_fields"`
	Statuses   []string `json:"official_task_statuses,omitempty"`
	Artifacts  []string `json:"artifacts,omitempty"`
	DiffCount  int      `json:"diffs"`
	Unexplains int      `json:"unexplained"`
}

// Report is the comparison of one version.
type Report struct {
	Provider         string        `json:"provider"`
	API              string        `json:"api"`
	OfficialBase     string        `json:"official_base"`
	CheckedAt        string        `json:"checked_at"`
	OfficialRequests int           `json:"official_requests"`
	PaidRequests     int           `json:"official_paid_requests"`
	FakeRequests     int           `json:"fake_requests"`
	Steps            []StepReport  `json:"steps"`
	Diffs            []Diff        `json:"diffs"`
	RequestNotes     []string      `json:"request_notes,omitempty"`
	Endpoints        []EndpointRow `json:"endpoints,omitempty"`
}

// EndpointRow is one endpoint of the version's table, as the report lists it.
type EndpointRow struct {
	Endpoint  string `json:"endpoint"`
	Evidence  string `json:"evidence"`
	Sample    string `json:"sample,omitempty"`
	DocURL    string `json:"doc_url,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Unexplained returns the differences without a written note.
func (r *Report) Unexplained() []Diff {
	var out []Diff
	for _, diff := range r.Diffs {
		if diff.Note == "" {
			out = append(out, diff)
		}
	}
	return out
}

// Compare lines up the two bundles step by step. Both must come from the same
// script.
func Compare(official, fake *Bundle, officialBase, checkedAt string, notes Notes, requestNotes ...string) *Report {
	report := &Report{
		Provider: official.Provider, API: official.API, OfficialBase: officialBase, CheckedAt: checkedAt,
		OfficialRequests: official.Requests, PaidRequests: official.PaidRequests, FakeRequests: fake.Requests,
		RequestNotes: requestNotes,
	}
	fakeByStep := map[string]Obs{}
	for _, obs := range fake.Obs {
		fakeByStep[obs.Step] = obs
	}
	add := func(step, aspect, detail string) {
		diff := Diff{Step: step, Aspect: aspect, Detail: detail}
		diff.Note = notes[diff.Key()]
		report.Diffs = append(report.Diffs, diff)
	}
	for _, real := range official.Obs {
		if real.Skipped != "" {
			continue
		}
		step := StepReport{Step: real.Step, Method: real.Method, Path: real.Path, Auth: real.AuthHeader, Official: real.Status}
		counted := len(report.Diffs)
		got, ok := fakeByStep[real.Step]
		if !ok || got.Skipped != "" {
			add(real.Step, "step", "fake did not run this step")
			report.Steps = append(report.Steps, step)
			continue
		}
		step.Fake = got.Status
		if real.Method != got.Method || real.Path != got.Path {
			add(real.Step, "request", fmt.Sprintf("official %s %s, fake %s %s", real.Method, real.Path, got.Method, got.Path))
		}
		if real.AuthHeader != got.AuthHeader {
			add(real.Step, "auth", fmt.Sprintf("official %q, fake %q", real.AuthHeader, got.AuthHeader))
		}
		if real.Status != got.Status {
			add(real.Step, "status", fmt.Sprintf("official %d, fake %d", real.Status, got.Status))
		}
		if real.Content != got.Content {
			add(real.Step, "content-type", fmt.Sprintf("official %q, fake %q", real.Content, got.Content))
		}
		if (real.RetryAfter != "") != (got.RetryAfter != "") {
			add(real.Step, "retry-after", fmt.Sprintf("official %q, fake %q", real.RetryAfter, got.RetryAfter))
		}
		if len(real.Body) > 0 || len(got.Body) > 0 {
			compareBodies(real, got, add, &step)
		}
		if len(real.Chunks) > 0 || len(got.Chunks) > 0 {
			compareChunks(real, got, add, &step)
		}
		compareStatuses(real, got, add, &step)
		compareArtifacts(real, got, add, &step)
		step.DiffCount = len(report.Diffs) - counted
		for _, diff := range report.Diffs[counted:] {
			if diff.Note == "" {
				step.Unexplains++
			}
		}
		report.Steps = append(report.Steps, step)
	}
	sort.SliceStable(report.Diffs, func(i, j int) bool { return report.Diffs[i].Key() < report.Diffs[j].Key() })
	return report
}

func compareBodies(real, got Obs, add func(step, aspect, detail string), step *StepReport) {
	if len(real.Body) == 0 || len(got.Body) == 0 {
		add(real.Step, "body", fmt.Sprintf("official has body=%t, fake has body=%t", len(real.Body) > 0, len(got.Body) > 0))
		return
	}
	want, err := fakeupstream.ShapeOf(real.Body)
	if err != nil {
		add(real.Step, "body", "official body: "+err.Error())
		return
	}
	have, err := fakeupstream.ShapeOf(got.Body)
	if err != nil {
		add(real.Step, "body", "fake body: "+err.Error())
		return
	}
	step.Fields = len(want)
	diff := fakeupstream.DiffShape(want, have)
	for _, path := range diff.Missing {
		add(real.Step, "field-missing-in-fake", path)
	}
	for _, path := range diff.Extra {
		add(real.Step, "field-extra-in-fake", path)
	}
	for _, path := range diff.TypeMismatch {
		add(real.Step, "field-type", path)
	}
}

// distinct collapses repeats while keeping first-seen order.
func distinct(polls []PollObs) []string {
	var out []string
	for _, poll := range polls {
		if len(out) == 0 || out[len(out)-1] != poll.Status {
			out = append(out, poll.Status)
		}
	}
	return out
}

func isSubsequence(want, have []string) bool {
	next := 0
	for _, status := range have {
		if next < len(want) && want[next] == status {
			next++
		}
	}
	return next == len(want)
}

func compareStatuses(real, got Obs, add func(step, aspect, detail string), step *StepReport) {
	if len(real.Polls) == 0 && len(got.Polls) == 0 {
		return
	}
	want, have := distinct(real.Polls), distinct(got.Polls)
	step.Statuses = want
	// Which intermediate status a poll catches depends on timing, so the
	// official flow only has to be a subsequence of the fake's full flow and
	// end in the same terminal status.
	if !isSubsequence(want, have) || len(want) == 0 || len(have) == 0 || want[len(want)-1] != have[len(have)-1] {
		add(real.Step, "task-status-flow", fmt.Sprintf("official %s, fake %s", strings.Join(want, ">"), strings.Join(have, ">")))
	}
}

func compareArtifacts(real, got Obs, add func(step, aspect, detail string), step *StepReport) {
	byField := map[string]ArtifactObs{}
	for _, artifact := range got.Artifacts {
		byField[artifact.Field] = artifact
	}
	for _, artifact := range real.Artifacts {
		step.Artifacts = append(step.Artifacts, fmt.Sprintf("%s %s %s", artifact.Field, artifact.ContentType, artifact.Format))
		fake, ok := byField[artifact.Field]
		if !ok {
			add(real.Step, "artifact", artifact.Field+" is not served by the fake")
			continue
		}
		if fake.Status != artifact.Status {
			add(real.Step, "artifact-status", fmt.Sprintf("%s official %d, fake %d", artifact.Field, artifact.Status, fake.Status))
		}
		if fake.ContentType != artifact.ContentType {
			add(real.Step, "artifact-content-type", fmt.Sprintf("%s official %q, fake %q", artifact.Field, artifact.ContentType, fake.ContentType))
		}
		if fake.Format != artifact.Format {
			add(real.Step, "artifact-format", fmt.Sprintf("%s official %q, fake %q", artifact.Field, artifact.Format, fake.Format))
		}
	}
}

// Markdown renders the report.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s %s 对齐实测报告\n\n", r.Provider, r.API)
	fmt.Fprintf(&b, "- 官方 API：`%s`\n- 核对日期：%s\n- 官方请求数：%d（其中付费：%d）；fake 请求数：%d\n", r.OfficialBase, r.CheckedAt, r.OfficialRequests, r.PaidRequests, r.FakeRequests)
	unexplained := len(r.Unexplained())
	fmt.Fprintf(&b, "- 差异：%d 项，其中未说明：%d 项\n\n", len(r.Diffs), unexplained)
	b.WriteString("只比对结构：方法、路径、鉴权头、状态码、响应 JSON 的字段集合、类型与嵌套、任务状态枚举与流转、产物 content-type 与格式；不比对值。\n\n")
	b.WriteString("## 步骤\n\n| 步骤 | 请求 | 鉴权 | 官方/fake 状态 | 字段数 | 任务状态流转 | 产物 | 差异（未说明） |\n|---|---|---|---|---|---|---|---|\n")
	for _, step := range r.Steps {
		fmt.Fprintf(&b, "| %s | `%s %s` | %s | %d/%d | %d | %s | %s | %d (%d) |\n", step.Step, step.Method, step.Path, step.Auth,
			step.Official, step.Fake, step.Fields, strings.Join(step.Statuses, " > "), strings.Join(step.Artifacts, "; "), step.DiffCount, step.Unexplains)
	}
	if len(r.RequestNotes) > 0 {
		b.WriteString("\n## 请求差异说明\n\n")
		for _, note := range r.RequestNotes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}
	if len(r.Endpoints) > 0 {
		b.WriteString("\n## 端点证据\n\n`real` 来自已提交的脱敏真实样本；`constructed-from-docs` 没有真实样本，按官方文档构造（核对日期见表）。\n\n| 端点 | 证据 | 样本 | 官方文档 | 核对日期 | 说明 |\n|---|---|---|---|---|---|\n")
		for _, row := range r.Endpoints {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n", row.Endpoint, row.Evidence, row.Sample, row.DocURL, row.CheckedAt, row.Note)
		}
	}
	b.WriteString("\n## 差异\n\n")
	if len(r.Diffs) == 0 {
		b.WriteString("无。\n")
	}
	for _, diff := range r.Diffs {
		note := diff.Note
		if note == "" {
			note = "**未说明**"
		}
		fmt.Fprintf(&b, "- `%s` / %s / %s：%s\n", diff.Step, diff.Aspect, diff.Detail, note)
	}
	return b.String()
}

// chunkShape merges the shapes of every SSE chunk (type sets are unioned).
func chunkShape(chunks []json.RawMessage) (fakeupstream.Shape, error) {
	merged := map[string]map[string]bool{}
	for _, chunk := range chunks {
		shape, err := fakeupstream.ShapeOf(chunk)
		if err != nil {
			return nil, err
		}
		for path, types := range shape {
			if merged[path] == nil {
				merged[path] = map[string]bool{}
			}
			for _, name := range strings.Split(types, "|") {
				merged[path][name] = true
			}
		}
	}
	out := fakeupstream.Shape{}
	for path, set := range merged {
		var names []string
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		out[path] = strings.Join(names, "|")
	}
	return out, nil
}

func compareChunks(real, got Obs, add func(step, aspect, detail string), step *StepReport) {
	want, err := chunkShape(real.Chunks)
	if err != nil {
		add(real.Step, "sse", "official chunk: "+err.Error())
		return
	}
	have, err := chunkShape(got.Chunks)
	if err != nil {
		add(real.Step, "sse", "fake chunk: "+err.Error())
		return
	}
	step.Fields = len(want)
	if real.Done != got.Done {
		add(real.Step, "sse-done", fmt.Sprintf("official %t, fake %t", real.Done, got.Done))
	}
	diff := fakeupstream.DiffShape(want, have)
	for _, path := range diff.Missing {
		add(real.Step, "field-missing-in-fake", path)
	}
	for _, path := range diff.Extra {
		add(real.Step, "field-extra-in-fake", path)
	}
	for _, path := range diff.TypeMismatch {
		add(real.Step, "field-type", path)
	}
}

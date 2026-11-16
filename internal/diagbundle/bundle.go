package diagbundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/wirecapture"
	"github.com/LeXwDeX/Plinth/internal/wirecapture/privacyscan"
)

// logsMemberPath is the one file the log lines are written to.
const logsMemberPath = "logs/art-ai.jsonl"

// Bundle is a diagnostics bundle that has been collected, scrubbed and scanned
// and is ready to be written. Members lists manifest.json first.
type Bundle struct {
	Manifest    Manifest
	Members     []Member
	GeneratedAt time.Time
}

// Directory is the top-level directory name inside the archive, so unpacking
// never scatters files into the current directory.
func (b *Bundle) Directory() string { return "plinth-diagnose-" + formatStamp(b.GeneratedAt) }

// DefaultFileName is the archive's file name when the caller names a directory.
func (b *Bundle) DefaultFileName() string { return b.Directory() + ".tar.gz" }

type runtimeDocument struct {
	Revision        string `json:"revision"`
	CatalogRevision string `json:"catalog_revision"`
	CatalogSHA256   string `json:"catalog_sha256"`
	Environment     string `json:"environment"`
	GoVersion       string `json:"go_version"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	GeneratedAt     string `json:"generated_at"`
}

type settingsDocument struct {
	Settings []settingEntry `json:"settings"`
	// RetiredEnvironmentSet names the retired variables the process still sees:
	// the service ignores them and warns once at start-up.
	RetiredEnvironmentSet []string `json:"retired_environment_set"`
}

// settingEntry says whether a setting is configured, never to what.
type settingEntry struct {
	Name             string   `json:"name"`
	Environment      []string `json:"environment"`
	Secret           bool     `json:"secret"`
	Set              bool     `json:"set"`
	Source           string   `json:"source,omitempty"`
	DeprecatedSource bool     `json:"deprecated_source,omitempty"`
}

// Build collects every category, scrubs the free text, scans each member as it
// will be written and returns the bundle. A privacy finding returns a
// *RefusedError and no bundle.
func Build(ctx context.Context, sources Sources, options Options) (*Bundle, error) {
	if options.MaxBytes < MinMaxBytes || options.MaxBytes > MaxMaxBytes {
		return nil, fmt.Errorf("最大体积须在 %d 到 %d 字节之间", MinMaxBytes, MaxMaxBytes)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	generated := now().UTC()
	since := options.Since.UTC()
	if !since.Before(generated) {
		return nil, errors.New("时间范围的起点必须早于现在")
	}
	scrubber := NewScrubber()

	catalogRevision := scrubber.Text(sources.CatalogRevision)
	runtimeData := encodeDocument(runtimeDocument{
		Revision: scrubber.Text(sources.Revision), CatalogRevision: catalogRevision, CatalogSHA256: scrubber.Text(sources.CatalogSHA256),
		Environment: scrubber.Text(sources.Environment), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		GeneratedAt: formatSecond(generated),
	})
	settingsData, settingCount := encodeSettings(sources)
	remaining := options.MaxBytes - int64(len(runtimeData)) - int64(len(settingsData)) - manifestReserve
	if remaining <= 0 {
		return nil, errors.New("最大体积太小，放不下运行信息与设置清单")
	}

	// Budgets: failures at most a tenth, captures at most three tenths of what
	// the fixed parts leave, the logs everything that is left.
	failures, err := collectFailures(ctx, sources, since, generated, remaining/10, scrubber)
	if err != nil {
		return nil, err
	}
	captures, err := collectCaptures(ctx, sources, since, generated, remaining*3/10)
	if err != nil {
		return nil, err
	}
	failureBytes := int64(0)
	if failures.entries > 0 {
		failureBytes = int64(len(failures.data))
	}
	logs, err := collectLogs(ctx, sources, since, generated, remaining-failureBytes-captures.bytes, scrubber)
	if err != nil {
		return nil, err
	}

	manifest := Manifest{
		Format: ManifestFormat, GeneratedAt: formatSecond(generated),
		Range:    TimeRange{Since: formatSecond(since), Until: formatSecond(generated)},
		MaxBytes: options.MaxBytes, Revision: scrubber.Text(sources.Revision), CatalogRevision: catalogRevision,
		CatalogSHA256: scrubber.Text(sources.CatalogSHA256),
		Privacy:       PrivacyReport{Scanner: "privacyscan"},
	}
	for i, warning := range sources.Warnings {
		if i == maxWarnings {
			break
		}
		manifest.Warnings = append(manifest.Warnings, scrubber.Text(warning))
	}
	var members []Member
	add := func(category, memberPath string, data []byte, report CategoryReport) {
		report.Category, report.Path, report.Bytes = category, memberPath, int64(len(data))
		sum := sha256.Sum256(data)
		report.SHA256 = hex.EncodeToString(sum[:])
		members = append(members, Member{Path: memberPath, Category: category, Data: data})
		manifest.Categories = append(manifest.Categories, report)
	}
	add(CategoryRuntime, "runtime.json", runtimeData, CategoryReport{Status: StatusIncluded, Entries: 1, Available: 1})
	add(CategorySettings, "settings.json", settingsData, CategoryReport{Status: StatusIncluded, Entries: settingCount, Available: settingCount})
	if failures.entries > 0 {
		add(CategoryFailures, "failures.json", failures.data, CategoryReport{
			Status: failures.status, Entries: failures.entries, Available: failures.available, Truncated: failures.truncated})
	} else {
		manifest.Categories = append(manifest.Categories, CategoryReport{Category: CategoryFailures, Status: failures.status,
			Reason: failures.reason, Available: failures.available})
	}
	if logs.entries > 0 {
		add(CategoryLogs, logsMemberPath, logs.data, CategoryReport{
			Status: logs.status, Entries: logs.entries, Available: logs.available, Truncated: logs.truncated, Dropped: logs.dropped})
	} else {
		manifest.Categories = append(manifest.Categories, CategoryReport{Category: CategoryLogs, Status: logs.status,
			Reason: logs.reason, Available: logs.available, Truncated: logs.truncated, Dropped: logs.dropped})
	}
	manifest.Categories = append(manifest.Categories, CategoryReport{
		Category: CategoryCaptures, Status: captures.status, Reason: captures.reason, Path: capturesPath(captures),
		Entries: captures.entries, Available: captures.available, Truncated: captures.truncated, Bytes: captures.bytes,
		Dropped: captures.dropped,
	})
	members = append(members, captures.members...)
	sortCategories(manifest.Categories)

	// The final scan: every member, as it will be written. The manifest is
	// scanned after the others because it states how many files were scanned.
	findings := verifyMembers(members, scrubber)
	manifest.Privacy.FilesScanned = len(members) + 1
	manifestData := encodeDocument(manifest)
	if int64(len(manifestData)) > manifestReserve {
		return nil, errors.New("清单超过预留大小")
	}
	manifestMember := Member{Path: "manifest.json", Category: "manifest", Data: manifestData}
	findings = append(findings, verifyMembers([]Member{manifestMember}, scrubber)...)
	if len(findings) > 0 {
		return nil, &RefusedError{Findings: findings}
	}
	return &Bundle{Manifest: manifest, Members: append([]Member{manifestMember}, members...), GeneratedAt: generated}, nil
}

func capturesPath(result captureResult) string {
	if result.entries == 0 {
		return ""
	}
	return "captures/"
}

// sortCategories keeps the manifest in the documented order.
func sortCategories(reports []CategoryReport) {
	order := map[string]int{CategoryRuntime: 0, CategorySettings: 1, CategoryFailures: 2, CategoryLogs: 3, CategoryCaptures: 4}
	sort.SliceStable(reports, func(i, j int) bool { return order[reports[i].Category] < order[reports[j].Category] })
}

// encodeDocument renders a document as indented JSON with a final newline.
func encodeDocument(document any) []byte {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return []byte("{}\n")
	}
	return out.Bytes()
}

// encodeSettings lists the Catalog's runtime settings with whether each is set.
// The list comes from the Catalog the process runs, not from a copy here.
func encodeSettings(sources Sources) ([]byte, int) {
	lookup := sources.Lookup
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	document := settingsDocument{Settings: make([]settingEntry, 0, len(sources.Settings)), RetiredEnvironmentSet: []string{}}
	for _, setting := range sources.Settings {
		entry := settingEntry{Name: setting.Name, Environment: []string{}, Secret: setting.Secret}
		for _, source := range setting.Environment {
			entry.Environment = append(entry.Environment, source.Name)
			if entry.Source == "" && strings.TrimSpace(lookup(source.Name)) != "" {
				entry.Set, entry.Source, entry.DeprecatedSource = true, source.Name, source.Deprecated
			}
		}
		document.Settings = append(document.Settings, entry)
	}
	sort.Slice(document.Settings, func(i, j int) bool { return document.Settings[i].Name < document.Settings[j].Name })
	for _, name := range sources.RetiredEnvironment {
		if strings.TrimSpace(lookup(name)) != "" {
			document.RetiredEnvironmentSet = append(document.RetiredEnvironmentSet, name)
		}
	}
	sort.Strings(document.RetiredEnvironmentSet)
	return encodeDocument(document), len(document.Settings)
}

// verifyMembers scans every member with the independent privacy scan, and the
// members that hold free text for any identity or prompt the export knows.
func verifyMembers(members []Member, scrubber *Scrubber) []privacyscan.Finding {
	var findings []privacyscan.Finding
	for _, member := range members {
		switch {
		case strings.HasPrefix(member.Path, "captures/"):
			record, err := wirecapture.ParseUpstreamRecord(path.Base(member.Path), member.Data)
			if err != nil {
				findings = append(findings, privacyscan.Finding{File: member.Path, Path: "$", Rule: "record-shape"})
				continue
			}
			recordFindings, _ := privacyscan.ScanRecord(member.Path, record)
			findings = append(findings, recordFindings...)
		case strings.HasPrefix(member.Path, "logs/"):
			for number, line := range bytes.Split(bytes.TrimSuffix(member.Data, []byte("\n")), []byte("\n")) {
				findings = append(findings, privacyscan.ScanJSON(fmt.Sprintf("%s:%d", member.Path, number+1), line)...)
			}
		default:
			findings = append(findings, privacyscan.ScanJSON(member.Path, member.Data)...)
		}
		if member.Category == CategoryLogs || member.Category == CategoryFailures {
			if rule := scrubber.Leaks(member.Data); rule != "" {
				findings = append(findings, privacyscan.Finding{File: member.Path, Path: "$", Rule: rule})
			}
		}
	}
	return findings
}

// WriteArchive writes the bundle as a gzip-compressed tar: one top-level
// directory (mode 0700), files at 0600, manifest.json first. Modes matter: the
// capture records are the format `fake-upstream-align draft` reads, which
// accepts only 0600 files in a 0700 directory, and they keep both modes when
// the archive is unpacked.
func (b *Bundle) WriteArchive(w io.Writer) error {
	gz := gzip.NewWriter(w)
	archive := tar.NewWriter(gz)
	root := b.Directory()
	directories := map[string]bool{root: true}
	write := func(header tar.Header, data []byte) error {
		header.ModTime = b.GeneratedAt.Truncate(time.Second)
		return writeEntry(archive, header, data)
	}
	if err := write(tar.Header{Name: root + "/", Typeflag: tar.TypeDir, Mode: 0o700}, nil); err != nil {
		return err
	}
	for _, member := range b.Members {
		if directory := path.Dir(member.Path); directory != "." && !directories[directory] {
			directories[directory] = true
			if err := write(tar.Header{Name: root + "/" + directory + "/", Typeflag: tar.TypeDir, Mode: 0o700}, nil); err != nil {
				return err
			}
		}
		header := tar.Header{Name: root + "/" + member.Path, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(member.Data))}
		if err := write(header, member.Data); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeEntry(archive *tar.Writer, header tar.Header, data []byte) error {
	if err := archive.WriteHeader(&header); err != nil {
		return err
	}
	_, err := archive.Write(data)
	return err
}

// Export builds a bundle and writes it to out: a new file with mode 0600, or a
// new file with the default name inside out when out is an existing directory.
// An existing file is never overwritten, and nothing is written unless the whole
// bundle passed the scan. It returns the path written.
func Export(ctx context.Context, sources Sources, options Options, out string) (*Bundle, string, error) {
	target, isDirectory, err := resolveOutput(out)
	if err != nil {
		return nil, "", err
	}
	bundle, err := Build(ctx, sources, options)
	if err != nil {
		return nil, "", err
	}
	if isDirectory {
		target = filepath.Join(target, bundle.DefaultFileName())
	}
	if err := writeNewFile(target, bundle); err != nil {
		return nil, "", err
	}
	return bundle, target, nil
}

// resolveOutput checks the output path before the work starts: a directory (the
// file is named inside it), or a name that is free in a directory that exists.
func resolveOutput(out string) (target string, isDirectory bool, err error) {
	if strings.TrimSpace(out) == "" {
		return "", false, errors.New("需要用 --out 指定输出文件")
	}
	out = filepath.Clean(out)
	info, err := os.Stat(out)
	switch {
	case err == nil && info.IsDir():
		return out, true, nil
	case err == nil:
		return "", false, errors.New("输出文件已存在，不会覆盖")
	case !errors.Is(err, os.ErrNotExist):
		return "", false, fmt.Errorf("无法使用输出路径: %w", err)
	}
	if _, linkErr := os.Lstat(out); linkErr == nil {
		return "", false, errors.New("输出路径是失效的符号链接，不会使用")
	}
	if parent, err := os.Stat(filepath.Dir(out)); err != nil || !parent.IsDir() {
		return "", false, errors.New("输出文件所在目录不存在")
	}
	return out, false, nil
}

// writeNewFile writes the archive next to its final name (mode 0600, created
// exclusively) and links it into place, so a reader never sees a half-written
// file and an existing file is never replaced.
func writeNewFile(target string, bundle *Bundle) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".plinth-diagnose-*")
	if err != nil {
		return fmt.Errorf("无法在输出目录创建文件: %w", err)
	}
	name := temporary.Name()
	defer func() {
		if err != nil {
			_ = temporary.Close()
		}
		_ = os.Remove(name)
	}()
	if err = temporary.Chmod(0o600); err != nil {
		return err
	}
	if err = bundle.WriteArchive(temporary); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Link(name, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("输出文件已存在，不会覆盖")
		}
		// A file system without hard links: the check above is what protects
		// an existing file.
		if _, statErr := os.Lstat(target); statErr == nil {
			return errors.New("输出文件已存在，不会覆盖")
		}
		return os.Rename(name, target)
	}
	return nil
}

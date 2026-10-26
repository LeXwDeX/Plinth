package etl

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/assets/legacygenerated"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type diagnosticCandidate struct {
	PathReference string `json:"path_reference"`
	Resolver      string `json:"resolver"`
	Exists        bool   `json:"exists"`
	Detail        string `json:"detail"`
}
type diagnosticEntry struct {
	Domain              string                `json:"domain"`
	ID                  string                `json:"id"`
	RawReference        string                `json:"raw_reference"`
	NormalizedReference string                `json:"normalized_reference"`
	Resolver            string                `json:"resolver"`
	CandidatePaths      []diagnosticCandidate `json:"candidate_paths"`
	Outcome             string                `json:"outcome"`
	Reason              string                `json:"reason"`
}
type diagnosticRollup struct {
	Domain   string `json:"domain"`
	Resolver string `json:"resolver"`
	Outcome  string `json:"outcome"`
	Count    int    `json:"count"`
}
type diagnosticReport struct {
	Format                 string `json:"format"`
	Mode                   string `json:"mode"`
	LegacyDatabaseSHA256   string `json:"legacy_database_sha256"`
	SnapshotManifestSHA256 string `json:"snapshot_manifest_sha256,omitempty"`
	SnapshotCompleteness   string `json:"snapshot_completeness"`
	GeneratedAtUTC         string `json:"generated_at_utc"`
	Sampling               struct {
		Policy string `json:"policy"`
		Seed   int    `json:"seed"`
	} `json:"sampling"`
	SourceCounts             map[string]int     `json:"source_counts"`
	LoadedCounts             map[string]int     `json:"loaded_counts"`
	SourceCredits            int                `json:"source_credits"`
	ProjectedOutputs         int                `json:"projected_outputs"`
	SourceInvariantsVerified bool               `json:"source_invariants_verified"`
	Entries                  []diagnosticEntry  `json:"entries"`
	Rollup                   []diagnosticRollup `json:"rollup"`
	Hypotheses               map[string]string  `json:"hypotheses"`
	SourceLossApproval       bool               `json:"source_loss_approval"`
}
type diagnosticProtectedRoots []string

func (r *diagnosticProtectedRoots) String() string     { return "protected roots" }
func (r *diagnosticProtectedRoots) Set(v string) error { *r = append(*r, v); return nil }

// All command failures use fixed codes. Underlying SQL, filesystem and Catalog
// errors can contain private IDs, source paths or payloads and never reach CLI.
func runDiagnoseAssets(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("diagnose-assets", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	database := flags.String("legacy-database", "", "standalone source SQLite snapshot")
	storage := flags.String("storage-root", "", "snapshot storage root")
	report := flags.String("report", "", "new report file outside all protected trees")
	catalogPath := flags.String("catalog", "", "optional Product Schema file")
	generatedAt := flags.String("generated-at-utc", "", "explicit RFC3339 report timestamp for reproducible runs")
	manifest := flags.String("snapshot-manifest", "", "optional snapshot manifest to hash, not completeness evidence")
	var protected diagnosticProtectedRoots
	flags.Var(&protected, "protected-root", "read-only CAS/target/source protection (repeatable)")
	fail := func(code string) int { fmt.Fprintln(stderr, "diagnose_assets."+code); return 1 }
	if flags.Parse(args) != nil || flags.NArg() != 0 || *database == "" || *storage == "" || *report == "" {
		return fail("invalid_arguments")
	}
	stamp, err := time.Parse(time.RFC3339, *generatedAt)
	if err != nil {
		return fail("invalid_timestamp")
	}
	sourcePath, err := filepath.Abs(*database)
	if err != nil {
		return fail("invalid_source")
	}
	storagePath, err := filepath.EvalSymlinks(*storage)
	if err != nil {
		return fail("invalid_storage")
	}
	storagePath, err = filepath.Abs(storagePath)
	if err != nil {
		return fail("invalid_storage")
	}
	roots := append([]string{filepath.Dir(sourcePath), storagePath}, protected...)
	if *catalogPath != "" {
		roots = append(roots, filepath.Dir(*catalogPath))
	}
	if *manifest != "" {
		roots = append(roots, filepath.Dir(*manifest))
	}
	output, err := diagnosticOutputPath(*report, roots)
	if err != nil {
		return fail("unsafe_output")
	}
	outputRoot, err := os.OpenRoot(filepath.Dir(output))
	if err != nil {
		return fail("unsafe_output")
	}
	defer outputRoot.Close()
	outputIdentity, err := outputRoot.Stat(".")
	if err != nil {
		return fail("unsafe_output")
	}
	identity, digest, err := diagnosticSnapshotIdentity(sourcePath)
	if err != nil {
		return fail("invalid_snapshot")
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		return fail("invalid_catalog")
	}
	if *catalogPath != "" {
		file, e := os.Open(*catalogPath)
		if e != nil {
			return fail("invalid_catalog")
		}
		product, err = catalog.Load(file)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return fail("invalid_catalog")
		}
	}
	doc, err := buildDiagnosticReportAt(sourcePath, storagePath, *manifest, product, stamp, diagnosticObserved{info: identity, digest: digest})
	if err != nil {
		return fail(err.Error())
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fail("report_failed")
	}
	body = append(body, '\n')
	if _, err := diagnosticOutputPath(output, roots); err != nil {
		return fail("unsafe_output")
	}
	currentOutputIdentity, err := os.Stat(filepath.Dir(output))
	if err != nil || !os.SameFile(outputIdentity, currentOutputIdentity) {
		return fail("unsafe_output")
	}
	if err := writeDiagnosticReport(outputRoot, filepath.Base(output), body); err != nil {
		return fail("report_failed")
	}
	fmt.Fprintf(stdout, "diagnose_assets.ok sha256:%s entries=%d\n", diagnosticHash(body), len(doc.Entries))
	return 0
}

func buildDiagnosticReport(sourcePath, storagePath, manifest string, product *catalog.Catalog, stamp time.Time) (diagnosticReport, error) {
	identity, digest, err := diagnosticSnapshotIdentity(sourcePath)
	if err != nil {
		return diagnosticReport{}, errors.New("invalid_snapshot")
	}
	return buildDiagnosticReportAt(sourcePath, storagePath, manifest, product, stamp, diagnosticObserved{info: identity, digest: digest})
}

func buildDiagnosticReportAt(sourcePath, storagePath, manifest string, product *catalog.Catalog, stamp time.Time, expected diagnosticObserved) (diagnosticReport, error) {
	identity, digest := expected.info, expected.digest
	var err error
	doc := diagnosticReport{Format: "asset-resolution-diagnostic-manifest-v1", Mode: "dry-run", LegacyDatabaseSHA256: digest, SnapshotCompleteness: "unknown", GeneratedAtUTC: stamp.UTC().Format(time.RFC3339), SourceLossApproval: false}
	doc.Sampling.Policy = "full"
	doc.Hypotheses = map[string]string{}
	for _, key := range []string{"H1", "H2", "H3", "H4", "H5", "H6"} {
		doc.Hypotheses[key] = "undetermined"
	}
	if manifest != "" {
		_, doc.SnapshotManifestSHA256, err = diagnosticSnapshotFile(manifest)
		if err != nil {
			return diagnosticReport{}, errors.New("invalid_snapshot_manifest")
		}
	}
	db, err := sql.Open("sqlite", readOnlyDSN(sourcePath)+"&immutable=1")
	if err != nil {
		return diagnosticReport{}, errors.New("source_read_failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return diagnosticReport{}, errors.New("source_read_failed")
	}
	defer tx.Rollback()
	var check string
	if err := tx.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		return diagnosticReport{}, errors.New("invalid_snapshot")
	}
	baseline, err := readLegacyBaseline(tx)
	if err != nil {
		return diagnosticReport{}, errors.New("source_invariants_failed")
	}
	doc.SourceCounts = baseline.counts
	doc.SourceCredits = baseline.credits
	tasks, _, loaded, err := loadLegacyTasksWithSchema(tx, taskProductSchemaFromCatalog(product))
	if err != nil {
		return diagnosticReport{}, errors.New("source_projection_failed")
	}
	doc.LoadedCounts = map[string]int{"usage_ledger": loaded.ledgerRows, "task_cache": loaded.cacheLoaded, "task_input_snapshot": loaded.snapshotLoaded, "generation_tasks": len(tasks)}
	if loaded.ledgerRows != baseline.counts["usage_ledger"] || loaded.cacheLoaded != baseline.counts["task_cache"] || loaded.snapshotLoaded != baseline.counts["task_input_snapshot"] || len(tasks) != baseline.tasks || loaded.credits != baseline.credits {
		return diagnosticReport{}, errors.New("source_invariants_failed")
	}
	scanner := newDiagnosticScanner(storagePath, product.Application().Administration.Generations.Assets.MaximumPackageBytes)
	if err := diagnoseInputs(tx, scanner, &doc); err != nil {
		return diagnosticReport{}, errors.New("input_read_failed")
	}
	application := product.Application()
	assets := application.Administration.Generations.Assets
	reader, resolver, err := diagnosticSources(storagePath, product)
	if err != nil {
		return diagnosticReport{}, err
	}
	projector, err := legacygeneration.NewProjector(legacygeneration.ProjectorConfig{Capabilities: application.Capabilities, Aliases: application.TaskStream.Aliases, ExcludedRoles: application.Runtime.LegacyMigration.TaskOutputExcludedRoles})
	if err != nil {
		return diagnosticReport{}, errors.New("invalid_catalog")
	}
	audit, err := newLegacyAuditAssetRecovery(storagePath, application.Runtime.LegacyMigration.AuditAssetRecovery, assets)
	if err != nil {
		return diagnosticReport{}, errors.New("audit_read_failed")
	}
	urls, err := loadLegacyTaskResultURLs(context.Background(), tx)
	if err != nil {
		return diagnosticReport{}, errors.New("source_projection_failed")
	}
	for _, task := range tasks {
		if task.Task.State != generation.TaskSucceeded {
			continue
		}
		attempt := task.CurrentAttempt()
		if attempt == nil {
			return diagnosticReport{}, errors.New("source_projection_failed")
		}
		projected := projector.ProjectLegacyGenerationAssets(string(task.Task.Capability), attempt.Opaque, urls[task.Task.ID])
		required := map[string]int{}
		seen := map[string]int{}
		for _, r := range task.Task.Offering.Outputs {
			if r.Delivery == generation.AssetDeliveryBackground || r.Delivery == generation.AssetDeliveryOnDemand {
				continue
			}
			n := 1
			if r.Minimum != nil && *r.Minimum > 0 {
				n = *r.Minimum
			}
			required[r.Role] = n
		}
		type resolvedOutput struct {
			role  string
			entry diagnosticEntry
		}
		resolved := make([]resolvedOutput, 0, len(projected))
		for _, asset := range projected {
			doc.ProjectedOutputs++
			seen[asset.Role]++
			entry := newDiagnosticEntry("task_output_optional", string(task.Task.ID), asset.SourceURL)
			source := resolver.Resolve(asset.SourceURL)
			entry.NormalizedReference = diagnosticNormalized(source)
			scanner.output(&entry, string(task.Task.ID), source, reader, audit, asset)
			resolved = append(resolved, resolvedOutput{role: asset.Role, entry: entry})
		}
		// Strict migration assigns required roles after materialization. A later
		// readable copy satisfies the slot before an earlier unavailable reference.
		sort.SliceStable(resolved, func(i, j int) bool {
			return resolved[i].entry.Outcome == "materialized" && resolved[j].entry.Outcome != "materialized"
		})
		assigned := map[string]int{}
		for _, output := range resolved {
			if assigned[output.role] < required[output.role] {
				output.entry.Domain = "task_output_required"
				assigned[output.role]++
			}
			doc.Entries = append(doc.Entries, output.entry)
		}
		for role, minimum := range required {
			for n := seen[role]; n < minimum; n++ {
				entry := newDiagnosticEntry("task_output_required", string(task.Task.ID), "")
				entry.Resolver = "none"
				entry.Outcome = "unresolved_route"
				entry.Reason = "H4:missing_declaration"
				doc.Entries = append(doc.Entries, entry)
			}
		}
	}
	if err := tx.Rollback(); err != nil {
		return diagnosticReport{}, errors.New("source_read_failed")
	}
	if err := db.Close(); err != nil {
		return diagnosticReport{}, errors.New("source_read_failed")
	}
	if !diagnosticSourceUnchanged(sourcePath, identity, digest) {
		return diagnosticReport{}, errors.New("snapshot_changed")
	}
	if !scanner.unchanged() {
		return diagnosticReport{}, errors.New("asset_snapshot_changed")
	}
	doc.SourceInvariantsVerified = true
	doc.finish()

	return doc, nil
}

func diagnosticSources(storagePath string, product *catalog.Catalog) (*legacygeneration.Reader, *legacygeneration.SourceResolver, error) {
	application := product.Application()
	assets := application.Administration.Generations.Assets
	generated, err := legacygenerated.New(filepath.Join(storagePath, "generated"))
	if err != nil {
		return nil, nil, errors.New("invalid_storage")
	}
	reader, err := legacygeneration.NewReader(legacygeneration.ReaderConfig{
		Native: administration.GenerationAssetReaderFunc(func(context.Context, administration.GenerationAssetRequest) (administration.GenerationAssetContent, error) {
			return administration.GenerationAssetContent{}, errors.New("diagnostic port disabled")
		}),
		GeneratedImages:           generated,
		GeneratedImageRoute:       application.Runtime.HTTP.LegacyRoutes.GeneratedImage,
		CacheRoot:                 filepath.Join(storagePath, "cache"),
		CachePrefixCharacters:     assets.LegacyCachePrefixCharacters,
		CacheMetadataSuffix:       assets.LegacyCacheMetadataSuffix,
		CacheContentTypeField:     assets.LegacyCacheContentTypeField,
		AllowedHosts:              legacygeneration.CollectAssetHosts(product.List("")),
		DefaultMediaType:          assets.DefaultMediaType,
		UnknownExtension:          assets.UnknownExtension,
		MaximumBytes:              assets.MaximumPackageBytes,
		RemoteTimeoutMilliseconds: assets.RemoteDownloadTimeoutMilliseconds,
		MaximumRedirects:          assets.MaximumRemoteRedirects,
		MediaTypeExtensions:       assets.MediaTypeExtensions,
		AssetDownloads:            legacygeneration.CollectAssetDownloads(application.Capabilities),
	})
	if err != nil {
		return nil, nil, errors.New("invalid_catalog")
	}
	resolver, err := legacygeneration.NewSourceResolver(application.Runtime.HTTP.LegacyRoutes.MeshyProxyAsset, application.Runtime.AssetDelivery.Proxy.API.Query.URL)
	if err != nil {
		return nil, nil, errors.New("invalid_catalog")
	}

	return reader, resolver, nil
}

func newDiagnosticEntry(domain, id, raw string) diagnosticEntry {
	return diagnosticEntry{Domain: domain, ID: "sha256:" + diagnosticHash([]byte(id)), RawReference: "sha256:" + diagnosticHash([]byte(raw)), NormalizedReference: diagnosticNormalized(raw), CandidatePaths: []diagnosticCandidate{}}
}
func diagnosticHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
func diagnosticNormalized(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "invalid:sha256:" + diagnosticHash([]byte(raw))
	}
	scheme := "relative"
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		scheme = parsed.Scheme
	} else if parsed.Scheme != "" {
		scheme = "unsupported"
	}
	value := scheme + ":host_sha256:" + diagnosticHash([]byte(parsed.Host)) + ":path_sha256:" + diagnosticHash([]byte(parsed.Path))
	if parsed.RawQuery != "" {
		value += ":<QUERY_REDACTED>"
	}
	return value
}
func (doc *diagnosticReport) finish() {
	if doc.Entries == nil {
		doc.Entries = []diagnosticEntry{}
	}
	sort.SliceStable(doc.Entries, func(i, j int) bool {
		a, b := doc.Entries[i], doc.Entries[j]
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return string(left) < string(right)
	})
	counts := map[string]diagnosticRollup{}
	for _, entry := range doc.Entries {
		key := entry.Domain + "/" + entry.Resolver + "/" + entry.Outcome
		v := counts[key]
		v.Domain = entry.Domain
		v.Resolver = entry.Resolver
		v.Outcome = entry.Outcome
		v.Count++
		counts[key] = v
	}
	doc.Rollup = []diagnosticRollup{}
	keys := []string{}
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		doc.Rollup = append(doc.Rollup, counts[key])
	}
}
func diagnosticSnapshotFile(path string) (os.FileInfo, string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, "", errors.New("unsafe file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, "", errors.New("changed file")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil || n != before.Size() {
		return nil, "", errors.New("read failed")
	}
	after, err = os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, "", errors.New("changed file")
	}
	return before, hex.EncodeToString(h.Sum(nil)), nil
}
func diagnosticNoJournal(path string) bool {
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}
func diagnosticSnapshotIdentity(path string) (os.FileInfo, string, error) {
	if !diagnosticNoJournal(path) {
		return nil, "", errors.New("not standalone")
	}
	info, digest, err := diagnosticSnapshotFile(path)
	if err != nil || !diagnosticNoJournal(path) {
		return nil, "", errors.New("snapshot changed")
	}
	return info, digest, nil
}
func diagnosticSourceUnchanged(path string, expected os.FileInfo, digest string) bool {
	info, actual, err := diagnosticSnapshotIdentity(path)
	return err == nil && os.SameFile(expected, info) && expected.Size() == info.Size() && expected.ModTime().Equal(info.ModTime()) && digest == actual
}

func diagnosticOutputPath(path string, roots []string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	result := filepath.Join(parent, filepath.Base(path))
	if _, err := os.Lstat(result); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("exists")
	}
	for _, root := range roots {
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(canonical, result)
		if err != nil || relative == "." || (!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "..") {
			return "", errors.New("protected")
		}
	}
	return result, nil
}
func writeDiagnosticReport(root *os.Root, name string, body []byte) error {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	temporary := ".asset-diagnostic-" + hex.EncodeToString(random)
	f, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Link(temporary, name)
}

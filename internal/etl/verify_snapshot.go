package etl

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

const proofUsageText = `art-ai-etl verify-snapshot
  --evidence <0600 art-ai-snapshot-evidence-v1 JSON>
  --scope-bindings <0600 art-ai-snapshot-bindings-v1 JSON>
  --legacy-database <standalone consistent DELETE SQLite snapshot>
  --snapshot-storage <read-only storage tree>
  --snapshot-manifest <0600 existing backup manifest>
  --inventory <0600 existing inventory>
  --report <new 0600 JSON outside every input/protected root>
  --generated-at-utc <RFC3339>
  [--catalog <Product Schema>] [--protected-root <CAS/target root> ...]
No Docker/SSH/export, network, credentials, migration or source-loss approval.
Missing/contradictory evidence yields STOP. Supplied claims are not authenticated.
`

type proofConfig struct {
	database, storage, manifest, inventory, evidence, bindings, catalog, output string
	stamp                                                                       time.Time
}
type proofStableFile struct {
	info   os.FileInfo
	digest string
}
type proofCheck struct {
	config         proofConfig
	evidence       proofEvidence
	bindings       proofBindings
	product        *catalog.Catalog
	files          map[string]proofStableFile
	sourceTrees    map[string]proofTree
	treeIdentities map[string]map[string]os.FileInfo
	snapshot       proofTree
	merged         proofTree
	report         *proofReport
	stage          string
}

func readProofFile(path string, private bool) ([]byte, proofStableFile, error) {
	initial, initialErr := os.Lstat(path)
	if initialErr != nil || !initial.Mode().IsRegular() || initial.Size() > 4<<20 {
		return nil, proofStableFile{}, errors.New("evidence_unreadable")
	}
	info, digest, err := diagnosticSnapshotFile(path)
	if err != nil || !os.SameFile(initial, info) || info.Size() > 4<<20 {
		return nil, proofStableFile{}, errors.New("evidence_unreadable")
	}
	if private && info.Mode().Perm() != 0600 {
		return nil, proofStableFile{}, errors.New("evidence_permissions")
	}
	raw, err := os.ReadFile(path)
	if err != nil || diagnosticHash(raw) != digest {
		return nil, proofStableFile{}, errors.New("evidence_changed")
	}
	return raw, proofStableFile{info: info, digest: digest}, nil
}
func runVerifySnapshot(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--help" {
		fmt.Fprint(stdout, proofUsageText)
		return 0
	}
	fail := func(code string) int { fmt.Fprintln(stderr, "verify_snapshot."+code); return 1 }
	flags := flag.NewFlagSet("verify-snapshot", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var cfg proofConfig
	flags.StringVar(&cfg.database, "legacy-database", "", "standalone consistent source snapshot")
	flags.StringVar(&cfg.storage, "snapshot-storage", "", "snapshot storage root")
	flags.StringVar(&cfg.manifest, "snapshot-manifest", "", "actual backup manifest")
	flags.StringVar(&cfg.inventory, "inventory", "", "actual collection inventory")
	flags.StringVar(&cfg.evidence, "evidence", "", "bounded metadata projection")
	flags.StringVar(&cfg.bindings, "scope-bindings", "", "explicit authorized local source scope bindings")
	flags.StringVar(&cfg.output, "report", "", "new report outside all input/protected trees")
	flags.StringVar(&cfg.catalog, "catalog", "", "optional Product Schema")
	stamp := flags.String("generated-at-utc", "", "explicit RFC3339 timestamp")
	var protected diagnosticProtectedRoots
	flags.Var(&protected, "protected-root", "extra read-only CAS/target root (repeatable)")
	seenFlags := map[string]bool{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--") {
			key, _, _ := strings.Cut(arg, "=")
			if seenFlags[key] && key != "--protected-root" {
				return fail("duplicate_argument")
			}
			seenFlags[key] = true
		}
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 || cfg.database == "" || cfg.storage == "" || cfg.manifest == "" || cfg.inventory == "" || cfg.evidence == "" || cfg.bindings == "" || cfg.output == "" {
		return fail("invalid_arguments")
	}
	var err error
	cfg.stamp, err = time.Parse(time.RFC3339, *stamp)
	if err != nil {
		return fail("invalid_timestamp")
	}
	cfg.database, err = filepath.Abs(cfg.database)
	if err != nil {
		return fail("invalid_snapshot")
	}
	cfg.storage, err = filepath.EvalSymlinks(cfg.storage)
	if err != nil {
		return fail("invalid_storage")
	}
	cfg.storage, err = filepath.Abs(cfg.storage)
	if err != nil {
		return fail("invalid_storage")
	}
	report := newProofReport(cfg.stamp.UTC().Format(time.RFC3339))
	check := proofCheck{config: cfg, files: map[string]proofStableFile{}, sourceTrees: map[string]proofTree{}, treeIdentities: map[string]map[string]os.FileInfo{}, merged: proofTree{}, report: &report}
	for _, input := range []struct {
		path, key string
		private   bool
		target    any
	}{{cfg.evidence, "D1_D3", true, &check.evidence}, {cfg.bindings, "scope_bindings", true, &check.bindings}, {cfg.inventory, "inventory", true, nil}, {cfg.manifest, "backup_manifest", true, nil}} {
		raw, stable, err := readProofFile(input.path, input.private)
		if err != nil {
			return fail("invalid_evidence")
		}
		check.files[input.path] = stable
		report.Evidence[input.key] = "sha256:" + stable.digest
		if input.target != nil && strictProofJSON(raw, input.target) != nil {
			return fail("invalid_evidence")
		}
	}
	if len(check.bindings.Scopes) > 128 {
		return fail("scope_limit")
	}
	roots := append([]string{cfg.storage, filepath.Dir(cfg.database), filepath.Dir(cfg.evidence), filepath.Dir(cfg.bindings), filepath.Dir(cfg.inventory), filepath.Dir(cfg.manifest)}, protected...)
	for _, scope := range check.bindings.Scopes {
		if !filepath.IsAbs(scope.SourceRoot) || filepath.Clean(scope.SourceRoot) == string(filepath.Separator) {
			return fail("invalid_scope")
		}
		roots = append(roots, scope.SourceRoot)
	}
	check.product, err = catalog.LoadDefault()
	if err != nil {
		return fail("invalid_catalog")
	}
	if cfg.catalog != "" {
		raw, stable, err := readProofFile(cfg.catalog, false)
		if err != nil {
			return fail("invalid_catalog")
		}
		check.files[cfg.catalog] = stable
		report.Evidence["catalog_file"] = "sha256:" + stable.digest
		check.product, err = catalog.Load(bytes.NewReader(raw))
		if err != nil {
			return fail("invalid_catalog")
		}
		roots = append(roots, filepath.Dir(cfg.catalog))
	}
	rawProduct, _ := json.Marshal(struct {
		Application catalog.ApplicationSchema `json:"application"`
		Offerings   any                       `json:"offerings"`
	}{check.product.Application(), check.product.List("")})
	report.Evidence["catalog"] = proofRef(string(rawProduct))
	output, err := diagnosticOutputPath(cfg.output, roots)
	if err != nil {
		return fail("unsafe_output")
	}
	outputRoot, err := os.OpenRoot(filepath.Dir(output))
	if err != nil {
		return fail("unsafe_output")
	}
	defer outputRoot.Close()
	outputInfo, err := outputRoot.Stat(".")
	if err != nil {
		return fail("unsafe_output")
	}
	err = check.verify()
	if err != nil {
		var issue *proofIssue
		if errors.As(err, &issue) {
			report.criterion(issue.criterion, issue.status, issue.reason)
			report.collection(check.stage, issue.status, issue.reason)
		} else {
			return fail("verification_failed")
		}
	}
	if !check.unchanged() {
		return fail("inputs_changed")
	}
	if err == nil {
		report.Decision = "supported"
		snapshotInputs := map[string]string{}
		for key, value := range report.Evidence {
			if key != "diagnostic" {
				snapshotInputs[key] = value
			}
		}
		identity, _ := json.Marshal(struct {
			Evidence map[string]string `json:"evidence"`
			Trees    map[string]string `json:"trees"`
		}{snapshotInputs, report.ScopeTrees})
		report.SnapshotIdentity = proofRef(string(identity))
	}
	body, marshalErr := json.MarshalIndent(report, "", "  ")
	if marshalErr != nil {
		return fail("report_failed")
	}
	body = append(body, '\n')
	if _, pathErr := diagnosticOutputPath(output, roots); pathErr != nil {
		return fail("unsafe_output")
	}
	currentOutputInfo, pathErr := os.Stat(filepath.Dir(output))
	if pathErr != nil || !os.SameFile(outputInfo, currentOutputInfo) {
		return fail("unsafe_output")
	}
	if err := writeDiagnosticReport(outputRoot, filepath.Base(output), body); err != nil {
		return fail("report_failed")
	}
	if report.Decision != "supported" {
		fmt.Fprintf(stderr, "verify_snapshot.STOP sha256:%s\n", diagnosticHash(body))
		return 1
	}
	fmt.Fprintf(stdout, "verify_snapshot.supported sha256:%s\n", diagnosticHash(body))
	return 0
}
func (p *proofCheck) unchanged() bool {
	for path, expected := range p.files {
		info, digest, err := diagnosticSnapshotFile(path)
		if err != nil || digest != expected.digest || !os.SameFile(info, expected.info) || info.Mode() != expected.info.Mode() || info.Size() != expected.info.Size() || !info.ModTime().Equal(expected.info.ModTime()) {
			return false
		}
	}
	if _, ok := p.files[p.config.database]; ok && !diagnosticNoJournal(p.config.database) {
		return false
	}
	for path, expected := range p.sourceTrees {
		observed := map[string]os.FileInfo{}
		actual, err := scanProofObservedTree(path, observed)
		if err != nil || !proofSameTree(expected, actual) || !proofSameObjects(p.treeIdentities[path], observed) {
			return false
		}
	}
	if p.snapshot != nil {
		observed := map[string]os.FileInfo{}
		actual, err := scanProofObservedTree(p.config.storage, observed)
		if err != nil || !proofSameTree(p.snapshot, actual) || !proofSameObjects(p.treeIdentities[p.config.storage], observed) {
			return false
		}
	}
	return true
}

type proofIssue struct{ criterion, status, reason string }

func (e *proofIssue) Error() string        { return e.reason }
func proofReject(id, reason string) error  { return &proofIssue{id, "refuted", reason} }
func proofUnknown(id, reason string) error { return &proofIssue{id, "unknown", reason} }
func (p *proofCheck) verify() error {
	p.stage = "D1"
	if err := p.topology(); err != nil {
		return err
	}
	p.report.collection("D1", "supported", "declared_topology_projected", p.report.Evidence["D1_D3"], p.report.Evidence["scope_bindings"])
	p.stage = "D2"
	p.report.criterion("C1", "supported", "declared_mount_scopes_covered", p.report.Evidence["D1_D3"], p.report.Evidence["scope_bindings"])
	if err := p.trees(); err != nil {
		return err
	}
	p.report.collection("D2", "supported", "full_tree_three_way_reconciled", p.report.Evidence["storage_tree"], p.report.Evidence["inventory"], p.report.Evidence["backup_manifest"])
	p.report.criterion("C2", "supported", "full_tree_and_three_way_counts_match", p.report.Evidence["inventory"], p.report.Evidence["backup_manifest"])
	p.report.criterion("C3", "supported", "links_resolve_within_declared_roots", p.report.Evidence["storage_tree"])
	p.stage = "D3"
	if err := p.writable(); err != nil {
		return err
	}
	p.report.collection("D3", "supported", "writable_diff_accounted", p.report.Evidence["D1_D3"], p.report.Evidence["storage_tree"])
	p.stage = "D4"
	if err := p.references(); err != nil {
		return err
	}
	p.report.collection("D4", "supported", "independent_reference_buckets_classified", p.report.Evidence["database"], p.report.Evidence["diagnostic"])
	p.report.criterion("C4", "supported", "all_input_reference_buckets_classified", p.report.Evidence["database"], p.report.Evidence["diagnostic"])
	p.report.criterion("C5", "supported", "all_output_reference_buckets_classified", p.report.Evidence["database"], p.report.Evidence["diagnostic"])
	p.report.criterion("C6", "supported", "no_unexplained_evidence_difference", p.report.Evidence["diagnostic"])
	return nil
}

func proofSameObjects(before, after map[string]os.FileInfo) bool {
	if len(before) != len(after) {
		return false
	}
	for path, a := range before {
		b, ok := after[path]
		if !ok || !os.SameFile(a, b) || a.Size() != b.Size() || a.Mode() != b.Mode() || !a.ModTime().Equal(b.ModTime()) {
			return false
		}
	}
	return true
}

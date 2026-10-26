package etl

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

func (p *proofCheck) references() error {
	if err := p.referenceBudget(); err != nil {
		return err
	}
	doc, err := buildDiagnosticReport(p.config.database, p.config.storage, p.config.manifest, p.product, p.config.stamp)
	if err != nil {
		return proofUnknown("C4", "diagnostic_read_failed")
	}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	raw = append(raw, '\n')
	p.report.Evidence["diagnostic"] = proofRef(string(raw))
	p.report.DiagnosticRollup = doc.Rollup
	p.report.SourceCounts = doc.SourceCounts
	knownReferences := map[string]bool{}
	var inputTotal, inputUnsafe, outputTotal, outputUnsafe int
	for _, entry := range doc.Entries {
		if entry.Outcome == "materialized" {
			knownReferences[entry.RawReference] = true
		}
		if entry.Domain == "input_asset" {
			inputTotal++
			if entry.Outcome == "illegal_path" || entry.Outcome == "sha256_mismatch" {
				inputUnsafe++
			}
		}
		if entry.Domain == "task_output_required" || entry.Domain == "task_output_optional" {
			outputTotal++
			if entry.Outcome == "illegal_path" || entry.Outcome == "sha256_mismatch" {
				outputUnsafe++
			}
		}
		if entry.Outcome == "unresolved_route" {
			if entry.Domain == "input_asset" {
				return proofUnknown("C4", "unexplained_input_reference")
			}
			return proofUnknown("C5", "unexplained_output_reference")
		}
	}
	// The diagnosis protocol's >10% safety stop is independent of whether
	// malformed paths have been counted into an otherwise known bucket.
	if inputUnsafe*10 > inputTotal {
		return proofReject("C4", "unsafe_reference_threshold")
	}
	if outputUnsafe*10 > outputTotal {
		return proofReject("C5", "unsafe_reference_threshold")
	}
	reader, resolver, err := diagnosticSources(p.config.storage, p.product)
	if err != nil {
		return proofUnknown("C4", "catalog_routes_unavailable")
	}
	db, err := sql.Open("sqlite", readOnlyDSN(p.config.database)+"&immutable=1")
	if err != nil {
		return proofUnknown("C4", "reference_read_failed")
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return proofUnknown("C4", "reference_read_failed")
	}
	defer tx.Rollback()
	buckets := map[string]proofBucket{}
	add := func(domain, kind, ref string) {
		key := domain + "\x00" + kind + "\x00" + ref
		b := buckets[key]
		b.Domain = domain
		b.Kind = kind
		b.Reference = ref
		b.Count++
		buckets[key] = b
	}
	rows, err := tx.Query(`SELECT COALESCE(storagePath, '') FROM asset_registry ORDER BY id`)
	if err != nil {
		return proofUnknown("C4", "reference_read_failed")
	}
	var inputs int
	for rows.Next() {
		var value string
		if rows.Scan(&value) != nil {
			rows.Close()
			return proofUnknown("C4", "reference_read_failed")
		}
		inputs++
		if inputs > proofMaximumFiles || len(value) > 4<<20 {
			rows.Close()
			return proofUnknown("C4", "reference_limit")
		}
		kind, ref := "relative_prefix", ""
		traversal := strings.Contains(value, "\\")
		for _, part := range strings.Split(value, "/") {
			traversal = traversal || part == ".."
		}
		if value == "" {
			kind = "empty"
		} else if filepath.IsAbs(value) {
			kind = "absolute"
		} else if traversal {
			kind = "traversal"
		} else if strings.ContainsRune(value, 0) {
			kind = "invalid"
		} else {
			prefix, _, _ := strings.Cut(value, "/")
			ref = proofRef(prefix)
		}
		add("input_asset", kind, ref)
	}
	if rows.Err() != nil {
		rows.Close()
		return proofUnknown("C4", "reference_read_failed")
	}
	rows.Close()
	if inputs != doc.SourceCounts["asset_registry"] {
		return proofReject("C4", "input_bucket_denominator_mismatch")
	}
	rows, err = tx.Query(`SELECT COALESCE(resultUrls, '') FROM usage_ledger ORDER BY id`)
	if err != nil {
		return proofUnknown("C5", "reference_read_failed")
	}
	var ledgerRows, totalReferences int
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			rows.Close()
			return proofUnknown("C5", "reference_read_failed")
		}
		ledgerRows++
		if ledgerRows > proofMaximumFiles || len(raw) > 4<<20 {
			rows.Close()
			return proofUnknown("C5", "reference_limit")
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var refs []string
		if json.Unmarshal([]byte(raw), &refs) != nil {
			rows.Close()
			return proofUnknown("C5", "result_url_array_invalid")
		}
		if len(refs) > proofMaximumFiles {
			rows.Close()
			return proofUnknown("C5", "reference_limit")
		}
		for _, original := range refs {
			totalReferences++
			if totalReferences > proofMaximumFiles {
				rows.Close()
				return proofUnknown("C5", "reference_limit")
			}
			source := resolver.Resolve(strings.TrimSpace(original))
			kind := "unexplained"
			candidates := reader.DiagnosticCandidates(source, filepath.Join(p.config.storage, "generated"))
			if len(candidates) > 0 && (candidates[0].Resolver == "generated_route" || candidates[0].Resolver == "legacy_download_route") {
				if candidates[0].Invalid {
					kind = "illegal_route"
				} else {
					kind = candidates[0].Resolver
				}
			} else if parsed, e := url.Parse(source); e == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
				kind = "remote_offline"
			} else if knownReferences[proofRef(strings.TrimSpace(original))] {
				kind = "local_cache"
			}
			if kind == "unexplained" {
				rows.Close()
				return proofUnknown("C5", "unexplained_url_bucket")
			}
			if source != strings.TrimSpace(original) {
				add("output_url", "proxied", "")
			}
			add("output_url", kind, "")
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return proofUnknown("C5", "reference_read_failed")
	}
	rows.Close()
	if ledgerRows != doc.SourceCounts["usage_ledger"] {
		return proofReject("C5", "output_bucket_denominator_mismatch")
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p.report.Buckets = append(p.report.Buckets, buckets[key])
	}
	return nil
}

// Limits apply to this offline evidence utility only. Exceeding them yields an
// explicit unknown/STOP; existing migrate and diagnose-assets contracts do not
// inherit these memory/work budgets.
func (p *proofCheck) referenceBudget() error {
	info := p.files[p.config.database].info
	if info == nil || info.Size() > 512<<20 {
		return proofUnknown("C4", "diagnostic_budget_exceeded")
	}
	db, err := sql.Open("sqlite", readOnlyDSN(p.config.database)+"&immutable=1")
	if err != nil {
		return proofUnknown("C4", "reference_read_failed")
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return proofUnknown("C4", "reference_read_failed")
	}
	defer tx.Rollback()
	baseline, err := readLegacyBaseline(tx)
	if err != nil {
		return proofUnknown("C4", "source_invariants_failed")
	}
	var count int64
	for _, n := range baseline.counts {
		if n < 0 || int64(n) > int64(proofMaximumFiles)-count {
			return proofUnknown("C4", "diagnostic_budget_exceeded")
		}
		count += int64(n)
	}
	var total int64
	for _, query := range []string{
		`SELECT COALESCE(SUM(COALESCE(length(CAST(params AS BLOB)),0)+COALESCE(length(CAST(resultUrls AS BLOB)),0)),0),COALESCE(MAX(max(COALESCE(length(CAST(params AS BLOB)),0),COALESCE(length(CAST(resultUrls AS BLOB)),0))),0) FROM usage_ledger`,
		`SELECT COALESCE(SUM(length(CAST(data AS BLOB))),0),COALESCE(MAX(length(CAST(data AS BLOB))),0) FROM task_cache`,
		`SELECT COALESCE(SUM(COALESCE(length(CAST(inputParams AS BLOB)),0)+COALESCE(length(CAST(inputFiles AS BLOB)),0)),0),COALESCE(MAX(max(COALESCE(length(CAST(inputParams AS BLOB)),0),COALESCE(length(CAST(inputFiles AS BLOB)),0))),0) FROM task_input_snapshot`,
	} {
		var size, maximum int64
		if tx.QueryRow(query).Scan(&size, &maximum) != nil {
			return proofUnknown("C4", "reference_read_failed")
		}
		if size < 0 || maximum > 4<<20 || size > (64<<20)-total {
			return proofUnknown("C4", "diagnostic_budget_exceeded")
		}
		total += size
	}
	return nil
}

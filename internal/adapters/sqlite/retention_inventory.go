package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type retentionQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type retentionCell struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type retentionRow struct {
	Table   string          `json:"table"`
	Columns []string        `json:"columns"`
	Cells   []retentionCell `json:"cells"`
}

func (r retentionRow) text(column string) string {
	for i, c := range r.Columns {
		if c == column {
			return r.Cells[i].Value
		}
	}
	return ""
}
func (r retentionRow) integer(column string) (int64, error) {
	return strconv.ParseInt(r.text(column), 10, 64)
}
func (r retentionRow) digest() string    { b, _ := json.Marshal(r); return retentionHash(b) }
func retentionHash(b []byte) string      { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func quoteRetentionName(v string) string { return `"` + strings.ReplaceAll(v, `"`, `""`) + `"` }

type retentionSnapshot struct {
	Schema       string
	Rows         []retentionRow
	Hash         string
	CompleteHash string
}

var retentionExpectedSchema struct {
	sync.Once
	value string
	err   error
}

func expectedRetentionSchema(ctx context.Context) (string, error) {
	retentionExpectedSchema.Do(func() {
		r, e := Open(":memory:")
		if e != nil {
			retentionExpectedSchema.err = e
			return
		}
		defer r.Close()
		retentionExpectedSchema.value, _, retentionExpectedSchema.err = retentionSchema(ctx, r.db)
	})
	return retentionExpectedSchema.value, retentionExpectedSchema.err
}
func retentionSchema(ctx context.Context, q retentionQuery) (string, []string, error) {
	rows, e := q.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY type,name`)
	if e != nil {
		return "", nil, e
	}
	defer rows.Close()
	var entries [][4]string
	var tables []string
	for rows.Next() {
		var kind, name, table string
		var statement sql.NullString
		if e = rows.Scan(&kind, &name, &table, &statement); e != nil {
			return "", nil, e
		}
		entries = append(entries, [4]string{kind, name, table, statement.String})
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	if e = rows.Err(); e != nil {
		return "", nil, e
	}
	b, e := json.Marshal(entries)
	sort.Strings(tables)
	return retentionHash(b), tables, e
}

func readRetentionSnapshot(ctx context.Context, q retentionQuery) (retentionSnapshot, error) {
	var out retentionSnapshot
	schema, tables, e := retentionSchema(ctx, q)
	if e != nil {
		return out, e
	}
	expected, e := expectedRetentionSchema(ctx)
	if e != nil {
		return out, e
	}
	if schema != expected {
		return out, errors.New("retention refuses unknown database schema; migrate and review its reference contract first")
	}
	out.Schema = schema
	totalBytes, totalRows := 0, 0
	for _, table := range tables {
		rows, err := q.QueryContext(ctx, "SELECT * FROM "+quoteRetentionName(table))
		if err != nil {
			return out, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return out, err
		}
		for rows.Next() {
			totalRows++
			if totalRows > 1_000_000 {
				rows.Close()
				return out, errors.New("retention snapshot exceeds bounded row limit")
			}
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				return out, err
			}
			r := retentionRow{Table: table, Columns: columns, Cells: make([]retentionCell, len(columns))}
			for i, v := range values {
				var c retentionCell
				switch v := v.(type) {
				case nil:
					c.Type = "null"
				case string:
					c = retentionCell{"text", v}
				case []byte:
					c = retentionCell{"blob", base64.StdEncoding.EncodeToString(v)}
				case int64:
					c = retentionCell{"integer", strconv.FormatInt(v, 10)}
				case float64:
					c = retentionCell{"real", strconv.FormatFloat(v, 'g', -1, 64)}
				case time.Time:
					c = retentionCell{"timestamp", v.UTC().Format(time.RFC3339Nano)}
				default:
					rows.Close()
					return out, errors.New("unsupported SQLite value in retention snapshot")
				}
				r.Cells[i] = c
				totalBytes += len(c.Value)
				if totalBytes > 256<<20 {
					rows.Close()
					return out, errors.New("retention snapshot exceeds bounded byte limit")
				}
			}
			out.Rows = append(out.Rows, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		return a.digest() < b.digest()
	})
	complete := sha256.New()
	content := sha256.New()
	complete.Write([]byte(schema))
	content.Write([]byte(schema))
	for _, r := range out.Rows {
		b, _ := json.Marshal(r)
		complete.Write(b)
		complete.Write([]byte{0})
		if r.Table != "generation_retention_receipts" {
			content.Write(b)
			content.Write([]byte{0})
		}
	}
	out.Hash = hex.EncodeToString(content.Sum(nil))
	out.CompleteHash = hex.EncodeToString(complete.Sum(nil))
	return out, nil
}

// retentionStrings scans all persisted fields rather than projecting only a
// known DTO version. It includes future JSON fields such as #212 candidates,
// nested JSON text, URL escapes and the historical base64 tool-argument form.
// Malformed JSON and typed argument strings fail closed. No raw row is emitted.
var retentionEscapes = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)

func retentionStrings(value any, key string, depth int, out *[]string) error {
	if depth > 24 {
		return errors.New("retention reference nesting exceeds safe bound")
	}
	switch v := value.(type) {
	case map[string]any:
		for k, x := range v {
			*out = append(*out, k)
			if e := retentionStrings(x, k, depth+1, out); e != nil {
				return e
			}
		}
	case []any:
		for _, x := range v {
			if e := retentionStrings(x, key, depth+1, out); e != nil {
				return e
			}
		}
	case string:
		*out = append(*out, v)
		parsed := false
		if len(v) > 0 && json.Valid([]byte(v)) {
			decoded, e := decodeRetentionJSON(v)
			if e != nil {
				return e
			}
			if s, ok := decoded.(string); !ok || s != v {
				parsed = true
				if e := retentionStrings(decoded, key, depth+1, out); e != nil {
					return e
				}
			}
		}
		if !parsed {
			for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
				b, e := encoding.DecodeString(v)
				if e == nil && len(b) > 0 && json.Valid(b) {
					decoded, e := decodeRetentionJSON(string(b))
					if e != nil {
						return e
					}
					parsed = true
					if e = retentionStrings(decoded, key, depth+1, out); e != nil {
						return e
					}
					break
				}
			}
		}
		if strings.EqualFold(key, "arguments") && !parsed && v != "" {
			return errors.New("unrecognized persisted tool argument encoding")
		}
		if strings.Contains(v, "%") {
			u, e := url.PathUnescape(v)
			if e != nil {
				// Ordinary prompt text can contain a literal percent sign.
				// Still decode every valid escape so mixed text cannot hide IDs.
				u = retentionEscapes.ReplaceAllStringFunc(v, func(part string) string { decoded, _ := url.PathUnescape(part); return decoded })
			}
			if u != v {
				if e = retentionStrings(u, "", depth+1, out); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func retentionRowStrings(r retentionRow) ([]string, error) {
	out := []string{}
	for i, c := range r.Cells {
		if c.Type == "null" {
			continue
		}
		v := c.Value
		if c.Type == "blob" {
			b, e := base64.StdEncoding.DecodeString(v)
			if e != nil {
				return nil, e
			}
			v = string(b)
			if !(r.Table == "generation_artifact_claims" && r.Columns[i] == "inline_data") && !json.Valid(b) {
				return nil, errors.New("unsupported binary reference encoding")
			}
		}
		if strings.HasSuffix(r.Columns[i], "_json") {
			if !json.Valid([]byte(v)) {
				return nil, fmt.Errorf("invalid persisted JSON in %s.%s", r.Table, r.Columns[i])
			}
			decoded, e := decodeRetentionJSON(v)
			if e != nil {
				return nil, e
			}
			if e := retentionStrings(decoded, r.Columns[i], 0, &out); e != nil {
				return nil, e
			}
		} else {
			if e := retentionStrings(v, r.Columns[i], 0, &out); e != nil {
				return nil, e
			}
		}
	}
	return out, nil
}

func inventoryFromRetentionSnapshot(s retentionSnapshot) (generation.RetentionInventory, error) {
	out := generation.RetentionInventory{SnapshotHash: s.Hash, SchemaHash: s.Schema}
	groups := map[string]string{}
	aliases := map[string][]string{}
	attempts := map[string]string{}
	taskRows := map[string]retentionRow{}
	entities := map[string]int{}
	addAlias := func(id, group string) {
		if id != "" {
			aliases[id] = append(aliases[id], group)
		}
	}
	for _, r := range s.Rows {
		if r.Table == "legacy_migration_records" {
			return out, errors.New("legacy migration records require a reviewed retention decoder")
		}
		switch r.Table {
		case "generation_tasks":
			id := r.text("id")
			taskRows[id] = r
			g := "task:" + id
			groups[r.digest()] = g
			entities[g] = len(out.Entities)
			out.Entities = append(out.Entities, generation.RetentionEntity{Kind: "task", ID: id, State: r.text("state"), Time: r.text("finished_at"), Protected: r.text("lease_token") != "" || r.text("lease_owner") != "" || r.text("lease_until") != "" || r.text("next_action_at") != ""})
			resources, err := generation.DecodeResourceStatesFromColumn([]byte(r.text("resource_state_json")))
			if err != nil {
				return out, err
			}
			for _, resource := range resources {
				switch resource.State {
				case generation.ResourcePending, generation.ResourceReady, generation.ResourceIncomplete, generation.ResourceFailed, generation.ResourceDenied, generation.ResourceAbandoned:
				default:
					return out, errors.New("unknown persistent resource state")
				}
			}
			if generation.ResourcesPending(resources) {
				out.Entities[len(out.Entities)-1].Protected = true
			}
			addAlias(id, g)
		case "generation_input_assets":
			id := r.text("id")
			g := "input:" + id
			groups[r.digest()] = g
			entities[g] = len(out.Entities)
			out.Entities = append(out.Entities, generation.RetentionEntity{Kind: "input", ID: id, Time: r.text("created_at")})
			addAlias(id, g)
		}
	}
	for _, r := range s.Rows {
		if r.Table == "generation_attempts" {
			g := "task:" + r.text("task_id")
			if _, ok := entities[g]; !ok {
				return out, errors.New("orphan attempt in retention snapshot")
			}
			attempts[r.text("id")] = g
			addAlias(r.text("id"), g)
		}
	}
	for _, r := range s.Rows {
		switch r.Table {
		case "generation_attempts", "generation_assets", "generation_task_events", "generation_task_tombstones", "generation_task_summaries":
			groups[r.digest()] = "task:" + r.text("task_id")
		case "generation_artifact_claims":
			g := attempts[r.text("attempt_id")]
			if g == "" {
				return out, errors.New("orphan artifact claim")
			}
			groups[r.digest()] = g
		}
		if r.Table == "generation_assets" {
			addAlias(r.text("id"), groups[r.digest()])
		}
		if r.Table == "generation_attempts" {
			if state := r.text("state"); state != string(generation.AttemptUpstreamSucceeded) && state != string(generation.AttemptFailed) && state != string(generation.AttemptCanceled) && state != string(generation.AttemptDeliveryCompleted) {
				i := entities[groups[r.digest()]]
				out.Entities[i].Protected = true
			}
		}
	}
	blobs := map[string]*generation.RetentionBlob{}
	addBlob := func(g, key, digest string, size int64) error {
		if key == "" || size < 0 || len(digest) != 64 {
			return errors.New("incomplete persisted CAS identity")
		}
		b := blobs[key]
		if b == nil {
			b = &generation.RetentionBlob{File: generation.RetentionFile{Key: key, Size: size, SHA256: digest}}
			blobs[key] = b
		} else if b.File.Size != size || b.File.SHA256 != digest {
			return errors.New("conflicting persisted CAS identity")
		}
		b.Groups = append(b.Groups, g)
		return nil
	}
	for _, r := range s.Rows {
		switch r.Table {
		case "generation_assets", "generation_input_assets":
			size, e := r.integer("size_bytes")
			if e != nil {
				return out, e
			}
			if e = addBlob(groups[r.digest()], r.text("storage_key"), r.text("sha256"), size); e != nil {
				return out, e
			}
		case "generation_recovery_receipts":
			receipt, err := decodeRecoveryReceipt(r.text("payload_json"), r.text("id"), r.text("digest"))
			if err != nil {
				return out, err
			}
			source := receipt.Source
			task, exists := taskRows[string(source.TaskID)]
			if !exists || string(source.OwnerID) != r.text("owner_id") || source.SessionID != r.text("session_id") ||
				string(source.TaskID) != r.text("source_task_id") || string(source.AttemptID) != r.text("source_attempt_id") ||
				task.text("owner_id") != string(source.OwnerID) || task.text("session_id") != source.SessionID ||
				attempts[string(source.AttemptID)] != "task:"+string(source.TaskID) {
				return out, errors.New("inconsistent recovery receipt source binding")
			}
			// Staged bytes can exist before a recovery Task registers any assets.
			// Receipts are external references, never part of a task deletion closure.
			for _, slot := range receipt.Slots {
				if err := addBlob("", slot.StorageKey, slot.SHA256, slot.Size); err != nil {
					return out, err
				}
			}
		case "generation_artifact_claims":
			if v := r.text("stored_asset_json"); v != "" {
				var b struct {
					StorageKey string `json:"storage_key"`
					Size       int64  `json:"size"`
					SHA256     string `json:"sha256"`
				}
				if e := json.Unmarshal([]byte(v), &b); e != nil {
					return out, e
				}
				if e := addBlob(groups[r.digest()], b.StorageKey, b.SHA256, b.Size); e != nil {
					return out, e
				}
			}
		}
	}
	for _, r := range s.Rows {
		if r.Table == "generation_retention_receipts" {
			continue
		}
		stringsFound, e := retentionRowStrings(r)
		if e != nil {
			return out, e
		}
		g := groups[r.digest()]
		seen := map[string]bool{}
		for _, v := range stringsFound {
			for alias, targets := range aliases {
				if strings.Contains(v, alias) {
					for _, target := range targets {
						if !seen[target] {
							seen[target] = true
							out.References = append(out.References, generation.RetentionReference{From: g, To: target, Reason: r.Table + ":" + r.digest()[:16]})
						}
					}
				}
			}
			for key, b := range blobs {
				if strings.Contains(v, key) {
					b.Groups = append(b.Groups, g)
				}
			}
			// A declared storage key not represented by an asset, stored claim or validated receipt is
			// unknown evidence, not permission to ignore that file.
			if strings.HasPrefix(v, "blobs/") {
				if _, ok := blobs[v]; !ok {
					return out, errors.New("unregistered persistent CAS reference")
				}
			}
		}
	}
	for _, b := range blobs {
		sort.Strings(b.Groups)
		out.Blobs = append(out.Blobs, *b)
	}
	sort.Slice(out.Blobs, func(i, j int) bool { return out.Blobs[i].File.Key < out.Blobs[j].File.Key })
	sort.Slice(out.References, func(i, j int) bool {
		a, b := out.References[i], out.References[j]
		return a.From+"\x00"+a.To+"\x00"+a.Reason < b.From+"\x00"+b.To+"\x00"+b.Reason
	})
	return out, nil
}

// Duplicate object keys are ambiguous reference evidence: never let a generic
// JSON decoder silently discard an earlier persistent source reference.
func decodeRetentionJSON(raw string) (any, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var parse func(int) (any, error)
	parse = func(depth int) (any, error) {
		if depth > 24 {
			return nil, errors.New("retention JSON exceeds depth bound")
		}
		token, e := d.Token()
		if e != nil {
			return nil, e
		}
		switch token {
		case json.Delim('{'):
			out := map[string]any{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, e
				}
				s, ok := key.(string)
				if !ok {
					return nil, errors.New("invalid JSON key")
				}
				if _, exists := out[s]; exists {
					return nil, errors.New("duplicate persistent JSON key")
				}
				v, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				out[s] = v
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, errors.New("invalid JSON object")
			}
			return out, nil
		case json.Delim('['):
			out := []any{}
			for d.More() {
				v, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				out = append(out, v)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, errors.New("invalid JSON array")
			}
			return out, nil
		default:
			if _, ok := token.(json.Delim); ok {
				return nil, errors.New("unexpected JSON delimiter")
			}
			return token, nil
		}
	}
	out, e := parse(0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); !errors.Is(e, io.EOF) {
		return nil, errors.New("extra persistent JSON value")
	}
	return out, nil
}

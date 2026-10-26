// Package harness defines the immutable human selection attached to an operation.
// It deliberately does not depend on the Catalog or Generation kernel.
package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"
)

var (
	ErrConflict = errors.New("harness: selection revision conflict")
	ErrInvalid  = errors.New("harness: invalid selection")
	ErrNotFound = errors.New("harness: selection not found")
	ErrStale    = errors.New("harness: selection changed; prepare the operation again")
)

// Binding is execution metadata, never part of Provider input. The all-zero
// value denotes a historical unbound operation; a new default has a real digest.
type Binding struct {
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

func (b Binding) IsZero() bool { return b == (Binding{}) }
func (b Binding) Valid() bool {
	if b.IsZero() {
		return true
	}
	if b.Revision < 0 || len(b.Digest) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(b.Digest)
	return err == nil && len(decoded) == sha256.Size && b.Digest == strings.ToLower(b.Digest)
}
func SameBinding(a, b Binding) bool { return a == b }

// SourceKind distinguishes real uploaded inputs from delivered Task outputs.
// The empty kind retains the historical Task-backed wire shape and digest.
type SourceKind string

const (
	SourceTaskAsset  SourceKind = "task_asset"
	SourceInputAsset SourceKind = "input_asset"
)

type Source struct {
	Kind          SourceKind `json:"kind,omitempty"`
	Parameter     string     `json:"parameter"`
	TaskID        string     `json:"task_id,omitempty"`
	AssetID       string     `json:"asset_id,omitempty"`
	SelectionMode string     `json:"selection_mode,omitempty"`
}

type Selection struct {
	OwnerID   string `json:"-"`
	SessionID string `json:"session_id"`
	Binding
	OperationID        string                     `json:"operation_id,omitempty"`
	OfferingID         string                     `json:"offering_id,omitempty"`
	OfferingRevision   string                     `json:"offering_revision,omitempty"`
	EnabledOperations  []string                   `json:"enabled_operations"`
	Sources            []Source                   `json:"sources"`
	FixedParameters    map[string]json.RawMessage `json:"fixed_parameters"`
	FillableParameters []string                   `json:"fillable_parameters"`
	AllowPrerequisites bool                       `json:"allow_prerequisites"`
	UpdatedAt          time.Time                  `json:"updated_at"`
}

type Store interface {
	GetHarnessSelection(context.Context, string, string) (Selection, error)
	CompareAndSwapHarnessSelection(context.Context, Selection, int64) (Selection, error)
	GetHarnessSelectionVersion(context.Context, string, string, Binding) (Selection, error)
}

func DefaultSelection(owner, session string) Selection {
	s, _ := NormalizeSelection(Selection{OwnerID: owner, SessionID: session})
	return s
}

// NormalizeSelection makes a detached copy and hashes semantic content. Owner,
// session, revision and update time are addressed separately by the store.
func NormalizeSelection(s Selection) (Selection, error) {
	if s.OwnerID == "" || s.SessionID == "" || s.Revision < 0 {
		return Selection{}, ErrInvalid
	}
	s.EnabledOperations = sortedUnique(s.EnabledOperations)
	s.FillableParameters = sortedUnique(s.FillableParameters)
	s.Sources = append([]Source{}, s.Sources...)
	sort.Slice(s.Sources, func(i, j int) bool {
		a, b := s.Sources[i], s.Sources[j]
		if a.Parameter != b.Parameter {
			return a.Parameter < b.Parameter
		}
		if a.TaskID != b.TaskID {
			return a.TaskID < b.TaskID
		}
		if a.AssetID != b.AssetID {
			return a.AssetID < b.AssetID
		}
		return a.Kind < b.Kind
	})
	for i, source := range s.Sources {
		if source.Parameter == "" || (source.TaskID == "" && source.AssetID == "" && source.SelectionMode != "agent") || (i > 0 && s.Sources[i-1] == source) {
			return Selection{}, ErrInvalid
		}
		if source.Parameter == "*" && (source.SelectionMode != "agent" || source.Kind != SourceTaskAsset) {
			return Selection{}, ErrInvalid
		}
		if source.SelectionMode != "" && source.SelectionMode != "fixed" && source.SelectionMode != "agent" {
			return Selection{}, ErrInvalid
		}
		if source.SelectionMode == "agent" {
			if source.Kind != SourceTaskAsset || source.TaskID != "" || source.AssetID != "" {
				return Selection{}, ErrInvalid
			}
			continue
		}
		switch source.Kind {
		case "": // Preserve persisted historical selections exactly.
		case SourceTaskAsset:
			if source.TaskID == "" {
				return Selection{}, ErrInvalid
			}
		case SourceInputAsset:
			if source.AssetID == "" || source.TaskID != "" {
				return Selection{}, ErrInvalid
			}
		default:
			return Selection{}, ErrInvalid
		}
	}
	fixed := make(map[string]json.RawMessage, len(s.FixedParameters))
	for key, raw := range s.FixedParameters {
		if key == "" {
			return Selection{}, ErrInvalid
		}
		value, err := canonicalJSON(raw)
		if err != nil {
			return Selection{}, fmt.Errorf("%w: parameter %s: %v", ErrInvalid, key, err)
		}
		fixed[key] = value
	}
	s.FixedParameters = fixed
	content := struct {
		OperationID        string                     `json:"operation_id"`
		OfferingID         string                     `json:"offering_id"`
		OfferingRevision   string                     `json:"offering_revision"`
		EnabledOperations  []string                   `json:"enabled_operations"`
		Sources            []Source                   `json:"sources"`
		FixedParameters    map[string]json.RawMessage `json:"fixed_parameters"`
		FillableParameters []string                   `json:"fillable_parameters"`
		AllowPrerequisites bool                       `json:"allow_prerequisites"`
	}{s.OperationID, s.OfferingID, s.OfferingRevision, s.EnabledOperations, s.Sources, s.FixedParameters, s.FillableParameters, s.AllowPrerequisites}
	encoded, err := json.Marshal(content)
	if err != nil {
		return Selection{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	digest := sha256.Sum256(encoded)
	s.Digest = hex.EncodeToString(digest[:])
	return s, nil
}

func sortedUnique(input []string) []string {
	values := append([]string{}, input...)
	sort.Strings(values)
	result := values[:0]
	for _, v := range values {
		if len(result) == 0 || result[len(result)-1] != v {
			result = append(result, v)
		}
	}
	return result
}

func canonicalJSON(raw []byte) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := canonicalValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return json.Marshal(value)
}

// Token decoding also rejects duplicate object keys rather than silently
// authorizing only the last occurrence of an ambiguous value.
func canonicalValue(d *json.Decoder) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch v := token.(type) {
	case json.Delim:
		switch v {
		case '{':
			object := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok {
					return nil, ErrInvalid
				}
				if _, ok := object[name]; ok {
					return nil, errors.New("duplicate object key")
				}
				value, err := canonicalValue(d)
				if err != nil {
					return nil, err
				}
				object[name] = value
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrInvalid
			}
			return object, nil
		case '[':
			array := []any{}
			for d.More() {
				value, err := canonicalValue(d)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrInvalid
			}
			return array, nil
		default:
			return nil, ErrInvalid
		}
	case json.Number:
		return normalizeNumber(string(v)), nil
	default:
		return token, nil
	}
}

// Decimal normalization avoids float rounding and exponent-sized allocations.
func normalizeNumber(number string) json.Number {
	sign := ""
	if strings.HasPrefix(number, "-") {
		sign = "-"
		number = number[1:]
	}
	exponent := new(big.Int)
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		exponent.SetString(number[i+1:], 10)
		number = number[:i]
	}
	if i := strings.IndexByte(number, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(number)-i-1)))
		number = number[:i] + number[i+1:]
	}
	number = strings.TrimLeft(number, "0")
	if number == "" {
		return json.Number("0")
	}
	trimmed := strings.TrimRight(number, "0")
	exponent.Add(exponent, big.NewInt(int64(len(number)-len(trimmed))))
	if exponent.Sign() == 0 {
		return json.Number(sign + trimmed)
	}
	return json.Number(sign + trimmed + "e" + exponent.String())
}

// Package rigcheckreport defines the closed public Rig Check report protocol.
// Provider identities, URLs, credentials and free text are never report fields.
package rigcheckreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/assets/printreport"
)

const (
	Format    = "art-ai.rig-check-report.v1"
	MediaType = "application/vnd.art-ai.rig-check-report+json"
	MaxBytes  = 32 << 10
)

var ErrInvalid = errors.New("invalid rig check report")

type Report struct {
	Format   string `json:"format"`
	Riggable bool   `json:"riggable"`
	RigType  string `json:"rig_type"`
}

// CheckJSONBounds reuses the existing bounded JSON syntax walker (depth 8,
// 128 fields/items), with this protocol's smaller byte limit and valid UTF-8.
// It validates wire syntax only; Decode still requires the closed public DTO.
func CheckJSONBounds(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxBytes || !utf8.Valid(raw) || printreport.CheckJSONBounds(raw) != nil {
		return ErrInvalid
	}
	return nil
}
func ValidRigType(value string) bool {
	switch value {
	case "biped", "quadruped", "hexapod", "octopod", "avian", "serpentine", "aquatic":
		return true
	}
	return false
}
func Decode(raw []byte) (Report, error) {
	if CheckJSONBounds(raw) != nil {
		return Report{}, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return Report{}, ErrInvalid
	}
	for key := range fields {
		if key != "format" && key != "riggable" && key != "rig_type" {
			return Report{}, ErrInvalid
		}
	}
	var value Report
	if json.Unmarshal(fields["format"], &value.Format) != nil || value.Format != Format {
		return Report{}, ErrInvalid
	}
	switch string(bytes.TrimSpace(fields["riggable"])) {
	case "true":
		value.Riggable = true
	case "false":
	default:
		return Report{}, ErrInvalid
	}
	if json.Unmarshal(fields["rig_type"], &value.RigType) != nil || !ValidRigType(value.RigType) {
		return Report{}, ErrInvalid
	}
	return value, nil
}
func Marshal(value Report) ([]byte, error) {
	if value.Format != Format || !ValidRigType(value.RigType) {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxBytes {
		return nil, ErrInvalid
	}
	return raw, nil
}
func ValidateReader(reader io.Reader, maxBytes int64) error {
	if reader == nil || maxBytes <= 0 || maxBytes > MaxBytes {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxBytes {
		return ErrInvalid
	}
	_, err = Decode(raw)
	return err
}

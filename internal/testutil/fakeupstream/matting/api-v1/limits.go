package mattingapiv1

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strings"
)

// Source says where a bound or behavior of the fake comes from.
type Source string

const (
	// SourceServiceCode: read from the service's own source (see limitsChecked).
	SourceServiceCode Source = "service-source"
	// SourceConstructed: nothing observed from the service backs it. The
	// number is the product Schema's declared bound (so the two cannot drift),
	// the way the service refuses it is the documented FastAPI/pydantic
	// vocabulary; the live round's /openapi.json (I22) settles both.
	SourceConstructed Source = "constructed"
)

// Kind is the type of a pipeline parameter.
type Kind string

const (
	KindInteger Kind = "integer"
	KindNumber  Kind = "number"
	KindBoolean Kind = "boolean"
)

// PipelineLimit is one field of the service's PipelineParams: its type, its
// bounds (numbers only) and where they come from. A null birefnet value is
// "auto" (the service picks GPU 2048, CPU 1024); every other field has a default
// instead, so null is not a valid value there.
type PipelineLimit struct {
	Field    string
	Kind     Kind
	Integer  bool
	Min, Max float64
	NullOK   bool
	Source   Source
}

// ResolutionLimit is the range of one process-resolution field, kept for the
// tests that hold the product Schema's ranges against the fake.
type ResolutionLimit struct {
	Field    string
	Min, Max int64
	NullOK   bool
}

// pipelineLimits is the one table: PipelineParams in the order the Offering
// declares it. The two process resolutions were read from the service source on
// limitsChecked (birefnet_process_res ge=512 le=4096, sdmatte_process_res ge=512
// le=2048); a value outside them is HTTP 422. The service lowers both to 1024
// only when it runs on a CPU; that is not a request error and the fake does not
// model it. The other bounds are the Schema's (constructed, I23).
var pipelineLimits = []PipelineLimit{
	{Field: "birefnet_process_res", Kind: KindInteger, Integer: true, Min: 512, Max: 4096, NullOK: true, Source: SourceServiceCode},
	{Field: "birefnet_mask_blur", Kind: KindNumber, Min: 0, Max: 50, Source: SourceConstructed},
	{Field: "expand", Kind: KindNumber, Min: 0, Max: 1000, Source: SourceConstructed},
	{Field: "tapered_corners", Kind: KindBoolean, Source: SourceConstructed},
	{Field: "blur_radius", Kind: KindNumber, Min: 0, Max: 100, Source: SourceConstructed},
	{Field: "sdmatte_process_res", Kind: KindInteger, Integer: true, Min: 512, Max: 2048, Source: SourceServiceCode},
	{Field: "transparent_object", Kind: KindBoolean, Source: SourceConstructed},
	{Field: "mask_refine", Kind: KindBoolean, Source: SourceConstructed},
	{Field: "sensitivity", Kind: KindNumber, Min: 0, Max: 1, Source: SourceConstructed},
}

// PipelineLimits returns every pipeline parameter the fake holds a request to.
func PipelineLimits() []PipelineLimit { return append([]PipelineLimit(nil), pipelineLimits...) }

// ResolutionLimits returns the process-resolution bounds the fake enforces.
func ResolutionLimits() []ResolutionLimit {
	var limits []ResolutionLimit
	for _, limit := range pipelineLimits {
		if limit.Kind == KindInteger {
			limits = append(limits, ResolutionLimit{Field: limit.Field, Min: int64(limit.Min), Max: int64(limit.Max), NullOK: limit.NullOK})
		}
	}
	return limits
}

// UploadLimit is what one batch's files are held to: how many, how big each one
// is, and which extensions the service takes. Constructed (I23): the numbers are
// the Offering's source_assets declaration; the service's own reaction to a
// batch over them was never observed, so the fake refuses a batch of too many
// files whole (a pydantic too_long on the files field) and a file of the wrong
// extension or size as a REJECTED task.
type UploadLimit struct {
	MaxFiles     int
	MaxFileBytes int64
	Extensions   []string
	Source       Source
}

// uploadLimits is a variable only so a test can lower the byte limit instead of
// sending 100 MiB.
var uploadLimits = UploadLimit{
	MaxFiles: 20, MaxFileBytes: 100 << 20,
	Extensions: []string{"png", "jpg", "jpeg", "webp", "bmp", "tiff"},
	Source:     SourceConstructed,
}

// UploadLimits returns the upload limits the fake enforces.
func UploadLimits() UploadLimit {
	limits := uploadLimits
	limits.Extensions = append([]string(nil), limits.Extensions...)
	return limits
}

// extensionAllowed reports whether the file name ends in an accepted extension
// (case-insensitive; a name with no extension is not accepted).
func (limit UploadLimit) extensionAllowed(name string) bool {
	extension := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	for _, allowed := range limit.Extensions {
		if extension == allowed {
			return true
		}
	}
	return false
}

// pipelineProblems validates every pipeline parameter the fake knows bounds for
// and answers them as the FastAPI/pydantic validation list, in field order. A
// field that is absent takes the service's default; a pipeline that is not a
// JSON object is not judged (the service's reaction to it was never observed).
func pipelineProblems(raw string) []any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return nil
	}
	var problems []any
	for _, limit := range pipelineLimits {
		value, present := fields[limit.Field]
		if !present || value == nil && limit.NullOK {
			continue
		}
		if problem := limit.problem(value); problem != nil {
			problems = append(problems, problem)
		}
	}
	return problems
}

// problem is the pydantic error of a value that does not fit the limit, or nil.
func (limit PipelineLimit) problem(value any) map[string]any {
	loc := []any{"body", PipelineField, limit.Field}
	if limit.Kind == KindBoolean {
		return limit.booleanProblem(loc, value)
	}
	wrongType := "int_type"
	message := "Input should be a valid integer"
	if !limit.Integer {
		wrongType, message = "float_type", "Input should be a valid number"
	}
	number, isNumber := value.(json.Number)
	if !isNumber {
		return map[string]any{"type": wrongType, "loc": loc, "msg": message, "input": value}
	}
	parsed, err := number.Float64()
	if err != nil {
		return map[string]any{"type": wrongType, "loc": loc, "msg": message, "input": value}
	}
	if limit.Integer && parsed != math.Trunc(parsed) {
		return map[string]any{"type": "int_from_float", "loc": loc,
			"msg": "Input should be a valid integer, got a number with a fractional part", "input": value}
	}
	switch {
	case parsed < limit.Min:
		return map[string]any{"type": "greater_than_equal", "loc": loc, "input": value,
			"msg": "Input should be greater than or equal to " + formatBound(limit.Min), "ctx": map[string]any{"ge": limit.Min}}
	case parsed > limit.Max:
		return map[string]any{"type": "less_than_equal", "loc": loc, "input": value,
			"msg": "Input should be less than or equal to " + formatBound(limit.Max), "ctx": map[string]any{"le": limit.Max}}
	}
	return nil
}

func formatBound(value float64) string {
	if value == math.Trunc(value) {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprint(value)
}

// booleanWords are the strings pydantic v2's lax mode reads as a boolean.
var booleanWords = map[string]bool{"0": true, "off": true, "f": true, "false": true, "n": true, "no": true,
	"1": true, "on": true, "t": true, "true": true, "y": true, "yes": true}

// booleanProblem judges a boolean field the way pydantic v2's lax mode does: a
// bool, the integers 0 and 1 and the words above are accepted; any other string
// or number is a bool_parsing error and any other type a bool_type error.
func (limit PipelineLimit) booleanProblem(loc []any, value any) map[string]any {
	switch typed := value.(type) {
	case bool:
		return nil
	case string:
		if booleanWords[strings.ToLower(typed)] {
			return nil
		}
		return booleanParsing(loc, value)
	case json.Number:
		if typed.String() == "0" || typed.String() == "1" || typed.String() == "0.0" || typed.String() == "1.0" {
			return nil
		}
		return booleanParsing(loc, value)
	}
	return map[string]any{"type": "bool_type", "loc": loc, "msg": "Input should be a valid boolean", "input": value}
}

func booleanParsing(loc []any, value any) map[string]any {
	return map[string]any{"type": "bool_parsing", "loc": loc, "msg": "Input should be a valid boolean, unable to interpret input", "input": value}
}

package schema

import (
	"strconv"
	"strings"
)

// InputRefusalReason names the Schema text for a synchronous input check that
// refuses a step before anything is submitted (#358). A Provider chooses one
// reason from its own check; the text and its values are Schema copy, so the
// Agent's tool result can be written in the turn's language.
type InputRefusalReason string

const (
	// InputRefusalMismatch: the resolved source no longer matches the frozen
	// input (identity, owner, dependency, digest or declared shape).
	InputRefusalMismatch InputRefusalReason = "input_source_mismatch"
	// InputRefusalUnavailable: the server could not read or write the source
	// file for a temporary reason; the same step may succeed later.
	InputRefusalUnavailable InputRefusalReason = "input_source_unavailable"
	// InputRefusalTooLarge: the frozen source is over the declared size limit.
	InputRefusalTooLarge InputRefusalReason = "input_source_too_large"
	// InputRefusalMediaType: the frozen source type is not among the declared
	// types.
	InputRefusalMediaType InputRefusalReason = "input_source_media_type"
	// InputRefusalDimensions: the image dimensions or pixel count are outside
	// the declared image contract.
	InputRefusalDimensions InputRefusalReason = "input_source_dimensions"
	// InputRefusalInvalid: the source bytes cannot be read as the declared
	// format (damaged, truncated or replaced bytes).
	InputRefusalInvalid InputRefusalReason = "input_source_invalid"
	// InputRefusalRigRequired: the step needs a supported native generation
	// Task of the same Provider as its source.
	InputRefusalRigRequired InputRefusalReason = "input_source_rig_required"
	// InputRefusalPixels: the source image exceeds the pixel limit. It does
	// not claim a minimum width or height.
	InputRefusalPixels InputRefusalReason = "input_source_pixels"
	// InputRefusalParameter: one declared input parameter has an invalid
	// value; Label is its public Schema label.
	InputRefusalParameter InputRefusalReason = "input_parameter_invalid"
	// InputRefusalParameters: the request's input parameters are invalid and
	// no public label is available.
	InputRefusalParameters InputRefusalReason = "input_parameters_invalid"
	// InputRefusalSourceExpired: a referenced Task source is past the
	// Provider's retention; Label is the public source parameter label.
	InputRefusalSourceExpired InputRefusalReason = "input_source_task_expired"
	// InputRefusalSourceUnsupported: a referenced Task source is missing or
	// does not meet this step's source requirements.
	InputRefusalSourceUnsupported InputRefusalReason = "input_source_task_unsupported"
	// InputRefusalFaceLimit: the 3D source has more triangle faces than the
	// step's declared limit; Faces is the counted value, FaceLimit the maximum.
	InputRefusalFaceLimit InputRefusalReason = "input_source_face_limit"
)

// InputRefusalValues are the facts a refusal template fills in. Only the
// values its reason declares are used; Size and Limit are already formatted
// with SizeText, and Formats is the declared list joined for reading.
type InputRefusalValues struct {
	Label     string
	Size      string
	Limit     string
	MediaType string
	Formats   string
	Width     int64
	Height    int64
	MinWidth  int64
	MinHeight int64
	MaxPixels int64
	Faces     int64
	FaceLimit int64
}

// InputRefusal returns the Offering's Schema refusal for a synchronous input
// check (#358), or nil when the Schema declares no text for reason (an
// external Catalog without the newer copy keeps the Provider diagnostic).
//
// The Error is marked UserFacing: Detail is complete text for the requester,
// DetailTemplate and DetailValues let a transport or the Agent write it in
// the turn's language. Code is ErrInvalidInput except for a temporary server
// condition, which is ErrUnavailable.
func (messages ExecutionMessages) InputRefusal(reason InputRefusalReason, values InputRefusalValues) *Error {
	template := ""
	code := ErrInvalidInput
	var pairs []string
	switch reason {
	case InputRefusalMismatch:
		template = messages.InputSourceMismatch
	case InputRefusalUnavailable:
		template, code = messages.InputSourceUnavailable, ErrUnavailable
	case InputRefusalTooLarge:
		template = messages.InputSourceTooLarge
		if values.Size == "" || values.Limit == "" {
			return nil
		}
		pairs = []string{"{size}", values.Size, "{limit}", values.Limit}
	case InputRefusalMediaType:
		template = messages.InputSourceMediaType
		if values.MediaType == "" || values.Formats == "" {
			return nil
		}
		pairs = []string{"{media_type}", values.MediaType, "{formats}", values.Formats}
	case InputRefusalDimensions:
		template = messages.InputSourceDimensions
		if values.Width <= 0 || values.Height <= 0 || values.MinWidth <= 0 ||
			values.MinHeight <= 0 || values.MaxPixels <= 0 {
			return nil
		}
		pairs = []string{
			"{width}", strconv.FormatInt(values.Width, 10), "{height}", strconv.FormatInt(values.Height, 10),
			"{min_width}", strconv.FormatInt(values.MinWidth, 10), "{min_height}", strconv.FormatInt(values.MinHeight, 10),
			"{max_pixels}", strconv.FormatInt(values.MaxPixels, 10),
		}
	case InputRefusalInvalid:
		template = messages.InputSourceInvalid
	case InputRefusalRigRequired:
		template = messages.InputSourceRigRequired
	case InputRefusalPixels:
		template = messages.InputSourcePixels
		if values.Width <= 0 || values.Height <= 0 || values.MaxPixels <= 0 {
			return nil
		}
		pairs = []string{
			"{width}", strconv.FormatInt(values.Width, 10), "{height}", strconv.FormatInt(values.Height, 10),
			"{max_pixels}", strconv.FormatInt(values.MaxPixels, 10),
		}
	case InputRefusalParameter:
		template = messages.InputParameterInvalid
		if values.Label == "" {
			return nil
		}
		pairs = []string{"{label}", values.Label}
	case InputRefusalParameters:
		template = messages.InputParametersInvalid
	case InputRefusalSourceExpired:
		template = messages.InputSourceTaskExpired
		if values.Label == "" {
			return nil
		}
		pairs = []string{"{label}", values.Label}
	case InputRefusalSourceUnsupported:
		template = messages.InputSourceTaskUnsupported
		if values.Label == "" {
			return nil
		}
		pairs = []string{"{label}", values.Label}
	case InputRefusalFaceLimit:
		template = messages.InputSourceFaceLimit
		if values.Faces <= 0 || values.FaceLimit <= 0 {
			return nil
		}
		pairs = []string{
			"{faces}", strconv.FormatInt(values.Faces, 10), "{limit}", strconv.FormatInt(values.FaceLimit, 10),
		}
	default:
		return nil
	}
	if template == "" {
		return nil
	}
	return &Error{
		Code:           code,
		Operation:      "validate_provider_input",
		Detail:         strings.NewReplacer(pairs...).Replace(template),
		UserFacing:     true,
		DetailTemplate: template,
		DetailValues:   pairs,
	}
}

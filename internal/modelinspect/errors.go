package modelinspect

import "errors"

// Parse, Inspect and Render classify failures with these sentinel errors.
// Callers should test them with errors.Is.
var (
	// ErrInvalid reports malformed or internally inconsistent model data.
	ErrInvalid = errors.New("modelinspect: invalid model")
	// ErrUnsupported reports a valid feature this package does not implement.
	ErrUnsupported = errors.New("modelinspect: unsupported model feature")
	// ErrTooLarge reports that a caller-supplied limit was exceeded.
	ErrTooLarge = errors.New("modelinspect: model exceeds configured limits")
	// ErrTooDense reports that a render mode or size cannot represent the model.
	ErrTooDense = errors.New("modelinspect: model is too dense for this render")
)

// invalidf formats an ErrInvalid with structural context. It must not be given
// file paths or payload bytes; index values are safe.
func invalidf(format string, args ...any) error {
	return wrapf(ErrInvalid, format, args...)
}

func unsupportedf(format string, args ...any) error {
	return wrapf(ErrUnsupported, format, args...)
}

func toolargef(format string, args ...any) error {
	return wrapf(ErrTooLarge, format, args...)
}

func toodensef(format string, args ...any) error {
	return wrapf(ErrTooDense, format, args...)
}

// CompressionError reports a required mesh compression extension that Parse
// cannot decode. It wraps ErrUnsupported, so errors.Is(err, ErrUnsupported)
// still holds; callers use errors.As to tell a compressed file apart from
// other unsupported features and to name the extension.
type CompressionError struct {
	// Extension is the glTF extension name, for example
	// "KHR_draco_mesh_compression".
	Extension string
}

func (e *CompressionError) Error() string {
	return ErrUnsupported.Error() + ": compressed geometry (" + e.Extension + ")"
}

func (e *CompressionError) Unwrap() error { return ErrUnsupported }

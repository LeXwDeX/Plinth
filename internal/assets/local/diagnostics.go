package local

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// WriteError retains the cause for internal errors.Is checks; callers log only
// StorageDiagnostic, never Error(), which can include a private filesystem path.
type WriteError struct {
	cause      error
	diagnostic generation.AssetStorageDiagnostic
}

func (e *WriteError) Error() string                                        { return e.cause.Error() }
func (e *WriteError) Unwrap() error                                        { return e.cause }
func (e *WriteError) StorageDiagnostic() generation.AssetStorageDiagnostic { return e.diagnostic }

func diagnoseWrite(stage, reason string, err error) error {
	if err == nil {
		return nil
	}
	var inspection *inspectionRejection
	var unavailable *inspectionUnavailable
	switch {
	case errors.As(err, &unavailable):
		// 检查器因资源争用、超时或取消而没能给出结论：不是对字节的判定，
		// 单独的阶段让内核保持可重试，而不是按内容拒收终止任务。
		stage, reason = "inspect_unavailable", "inspection_busy"
	case errors.As(err, &inspection):
		reason = inspection.reason
	}
	errno := "other"
	switch {
	case errors.Is(err, context.Canceled):
		errno = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		errno = "deadline"
	case errors.Is(err, os.ErrPermission):
		errno = "permission"
	case errors.Is(err, os.ErrNotExist):
		errno = "not_found"
	case errors.Is(err, syscall.ENOSPC):
		errno = "no_space"
	case errors.Is(err, syscall.EROFS):
		errno = "read_only"
	case errors.Is(err, syscall.EIO):
		errno = "io"
	case errors.Is(err, io.ErrUnexpectedEOF):
		errno = "unexpected_eof"
	}
	return &WriteError{cause: err, diagnostic: generation.AssetStorageDiagnostic{Stage: stage, Reason: reason, Errno: errno}}
}

// inspectionRejection carries a stable structural reason without disclosing
// metadata text, addresses or bytes. It does not change the delivery checks.
type inspectionRejection struct {
	reason string
	cause  error
}

func (e *inspectionRejection) Error() string { return e.cause.Error() }
func (e *inspectionRejection) Unwrap() error { return e.cause }
func rejectInspection(reason string, err error) error {
	return &inspectionRejection{reason: reason, cause: err}
}

// inspectionUnavailable marks an inspection that could not reach a verdict for
// reasons unrelated to the bytes (execution slot contention, cancellation). The
// same bytes may pass on another try, so it is never a content rejection.
type inspectionUnavailable struct{ cause error }

func (e *inspectionUnavailable) Error() string { return e.cause.Error() }
func (e *inspectionUnavailable) Unwrap() error { return e.cause }
func unavailableInspection(err error) error    { return &inspectionUnavailable{cause: err} }

// inspectionTransient reports an inspection error caused by cancellation, a
// deadline or an I/O fault rather than by the content being inspected.
func inspectionTransient(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.EIO) || errors.Is(err, syscall.ENOSPC)
}

// labelInspectionFailure keeps a specific structural reason when the inspector
// supplied one; otherwise it classifies the rejection by wire family so the
// diagnostic still names where the bytes failed, never what they contained.
func labelInspectionFailure(format string, err error) error {
	var inspection *inspectionRejection
	if errors.As(err, &inspection) {
		return err
	}
	if _, rejected := rejectionReasonForFormat[format]; !rejected {
		return err
	}
	return rejectInspection(rejectionReasonForFormat[format], err)
}

var rejectionReasonForFormat = map[string]string{
	"archive": "archive_rejected",
	"usdc":    "usdc_rejected",
	"glb":     "glb_rejected",
	"gltf":    "gltf_rejected",
	"stl":     "stl_rejected",
	"3mf":     "3mf_rejected",
	"blend":   "blend_rejected",
	"text":    "content_rejected",
}

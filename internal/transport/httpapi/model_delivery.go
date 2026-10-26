package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/modeldelivery"
	"github.com/LeXwDeX/Plinth/internal/processing"
)

// getModelFormats projects which target formats a 3D task already delivers,
// which a declared Offering can still produce, and why the rest cannot be
// had. Reading it never starts a task and never contacts a provider.
func (s *Server) getModelFormats(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	s.setPrivateAssetCacheControl(writer)
	coverage, err := s.modelDelivery.Coverage(
		request.Context(), taskReadScope(principal), generation.TaskID(request.PathValue("taskID")),
	)
	if err != nil {
		s.writeModelDeliveryError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, s.localizedSchemaData(request, coverage))
}

// completeModelFormatsRequest is the optional body of the completion route.
// Formats narrows it to the steps that deliver those formats (the row action
// of one format); an empty body completes every obtainable format.
type completeModelFormatsRequest struct {
	Formats []string `json:"formats"`
}

// completeModelFormats starts the processing plans that fill the obtainable
// formats. Only the owner can start it; the plans go through the ordinary
// approval policy and task kernel.
func (s *Server) completeModelFormats(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body completeModelFormatsRequest
	if request.ContentLength != 0 {
		raw, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes))
		if err != nil {
			s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_json", "error.invalid_json")
			return
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			request.Body = io.NopCloser(bytes.NewReader(raw))
			if err := s.decodeJSON(writer, request, &body); err != nil {
				return
			}
		}
	}
	coverage, err := s.modelDelivery.Complete(
		request.Context(), owner, generation.TaskID(request.PathValue("taskID")), body.Formats...,
	)
	if err != nil {
		s.writeModelDeliveryError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, s.localizedSchemaData(request, coverage))
}

// downloadModelArchive streams one ZIP of the task's already-isolated local
// model files. The README inside states what is included and what is missing.
func (s *Server) downloadModelArchive(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	s.setPrivateAssetCacheControl(writer)
	taskID := request.PathValue("taskID")
	started := time.Now()
	var hitsBefore, missesBefore, bytesBefore uint64
	if s.assetVerificationStats != nil {
		hitsBefore, missesBefore, bytesBefore = s.assetVerificationStats()
	}
	archive, err := s.modelDelivery.Prepare(request.Context(), taskReadScope(principal), generation.TaskID(taskID))
	if err != nil {
		s.writeModelDeliveryError(writer, err)
		return
	}
	defer archive.Close()
	// 预检是否复用已校验结论要看命中/未命中计数与未命中读取的字节数；
	// 只记录时延无法区分 "跳过重复校验" 与 "本次没有校验工作"。
	if s.assetVerificationStats != nil && s.logger != nil {
		hits, misses, verifiedBytes := s.assetVerificationStats()
		s.logger.InfoContext(request.Context(), "模型归档预检完成",
			"task_id", taskID,
			"entries", archive.Entries,
			"duration_ms", time.Since(started).Milliseconds(),
			"verification_hits", hits-hitsBefore,
			"verification_misses", misses-missesBefore,
			"verification_bytes", verifiedBytes-bytesBefore,
		)
	}
	audit := s.application.ModelDelivery.Archive.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       audit.Action,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: audit.ResourceType,
		ResourceID:   taskID,
		Detail: map[string]any{
			audit.DetailFields.TaskID:  taskID,
			audit.DetailFields.Entries: archive.Entries,
			audit.DetailFields.Bytes:   archive.Bytes,
			audit.DetailFields.Missing: strings.Join(archive.MissingFormats, ","),
		},
	})
	writer.Header().Set("Content-Type", archive.MediaType)
	writer.Header().Set("Content-Disposition", `attachment; filename="`+archive.Filename+`"`)
	setSandboxedBytesHeaders(writer)
	// Range is served by materializing the ZIP once (see serveRangedArchive);
	// the ordinary response keeps streaming so its first bytes do not wait for
	// the whole archive.
	writer.Header().Set("Accept-Ranges", "bytes")
	if request.Header.Get("Range") != "" {
		s.serveRangedArchive(writer, request, archive)
		return
	}
	writer.WriteHeader(http.StatusOK)
	// Send the status and headers as soon as Prepare has finished instead of
	// letting them sit in the server's buffer until the first file chunk.
	_ = http.NewResponseController(writer).Flush()
	if _, err := archive.WriteTo(writer); err != nil && s.logger != nil {
		s.logger.WarnContext(request.Context(), "模型压缩包写出中断",
			"task_id", taskID, "entries", strconv.Itoa(archive.Entries), "error", err)
	}
}

// serveRangedArchive answers a byte-range request. The archive is streamed
// through Deflate for several formats, so its total length is not known before
// it is written; a single materialization to a temporary file gives
// ServeContent the exact bytes and lets it set Content-Length, Content-Range
// and If-Range. The temporary file is removed when the response ends.
func (s *Server) serveRangedArchive(writer http.ResponseWriter, request *http.Request, archive *modeldelivery.Archive) {
	temp, err := s.createArchiveTempFile()
	if err != nil {
		s.writeModelDeliveryError(writer, err)
		return
	}
	name := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(name)
	}()
	if _, err := archive.WriteTo(temp); err != nil {
		s.writeModelDeliveryError(writer, err)
		return
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		s.writeModelDeliveryError(writer, err)
		return
	}
	http.ServeContent(writer, request, archive.Filename, time.Time{}, temp)
}

// createArchiveTempFile 把归档物化到装配时指定的目录。容器默认的 /tmp
// 常是小容量 tmpfs，而一个真实 3D 归档可达上百 MiB，写在那里会 ENOSPC。
func (s *Server) createArchiveTempFile() (*os.File, error) {
	return os.CreateTemp(s.archiveTempDir, "art-ai-model-archive-*.zip")
}

func (s *Server) writeModelDeliveryError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	switch {
	case errors.Is(err, modeldelivery.ErrNotApplicable):
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.model_formats_not_applicable")
	case errors.Is(err, modeldelivery.ErrNothingToComplete):
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.model_formats_nothing_to_complete")
	case errors.Is(err, modeldelivery.ErrEmptyArchive):
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.model_archive_empty")
	case errors.Is(err, modeldelivery.ErrArchiveTooLarge):
		s.writeMessageProblem(writer, http.StatusRequestEntityTooLarge, "too_large", "error.model_archive_too_large")
	case errors.Is(err, processing.ErrInvalid), errors.Is(err, processing.ErrConflict), errors.Is(err, processing.ErrMissing):
		s.writeProcessingError(writer, err)
	default:
		s.writeDomainError(writer, err)
	}
}

package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) recordModelCapture(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	if s.captures == nil {
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.internal")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var cmd capture.Command
	if err := decoder.Decode(&cmd); err != nil {
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
		return
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.internal")
		return
	}
	cmd.ID = "capture_" + hex.EncodeToString(identifier)
	cmd.OwnerID = string(owner)
	record, err := s.captures.Record(request.Context(), cmd)
	if err != nil {
		s.writeModelCaptureError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writer.Header().Set("Location", s.modelCaptureItemRoute.Render(record.ID))
	writeJSON(writer, http.StatusCreated, record)
}

func (s *Server) getModelCapture(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	if s.captures == nil {
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.internal")
		return
	}
	record, err := s.captures.Get(request.Context(), string(owner), request.PathValue("captureID"))
	if err != nil {
		s.writeModelCaptureError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, record)
}

func (s *Server) writeModelCaptureError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	switch {
	case errors.Is(err, capture.ErrInvalid):
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.Is(err, capture.ErrNotFound):
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.not_found")
	case errors.Is(err, capture.ErrConflict), errors.Is(err, capture.ErrMismatch):
		s.writeMessageProblem(writer, http.StatusConflict, "conflict", "error.conflict")
	default:
		s.writeMessageProblem(writer, http.StatusServiceUnavailable, "unavailable", "error.internal")
	}
}

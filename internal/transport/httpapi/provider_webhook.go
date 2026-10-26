package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func (s *Server) providerWebhookStatus(
	writer http.ResponseWriter,
	request *http.Request,
) {
	binding, pathSecret, matched := s.matchProviderWebhook(request.URL.Path)
	if !matched {
		http.NotFound(writer, request)
		return
	}
	schema := binding.schema
	request.SetPathValue(schema.PathParameter, pathSecret)
	if !providerWebhookSecretEqual(
		binding.pathSecret,
		pathSecret,
	) {
		s.writeProviderWebhookError(
			writer,
			schema,
			schema.API.Statuses.NotFound,
			schema.API.Values.NotFound,
		)
		return
	}
	writeJSON(writer, schema.API.Statuses.OK, map[string]any{
		schema.API.Fields.Status:    schema.API.Values.OK,
		schema.API.Fields.Service:   schema.API.Values.Service,
		schema.API.Fields.Timestamp: s.providerWebhookTimestamp(schema),
	})
}

func (s *Server) providerWebhookReceive(
	writer http.ResponseWriter,
	request *http.Request,
) {
	binding, pathSecret, matched := s.matchProviderWebhook(request.URL.Path)
	if !matched {
		http.NotFound(writer, request)
		return
	}
	schema := binding.schema
	request.SetPathValue(schema.PathParameter, pathSecret)
	if !providerWebhookSecretEqual(
		binding.pathSecret,
		pathSecret,
	) {
		s.writeProviderWebhookError(
			writer,
			schema,
			schema.API.Statuses.NotFound,
			schema.API.Values.NotFound,
		)
		return
	}
	if binding.key != "" &&
		!providerWebhookSecretEqual(
			binding.key,
			providerWebhookRequestKey(request, schema),
		) {
		s.writeProviderWebhookError(
			writer,
			schema,
			schema.API.Statuses.Unauthorized,
			schema.API.Values.Unauthorized,
		)
		return
	}
	payload, remoteID, err := s.decodeProviderWebhookPayload(
		writer,
		request,
		schema,
	)
	if err != nil {
		s.writeProviderWebhookError(
			writer,
			schema,
			schema.API.Statuses.BadRequest,
			schema.API.Values.BadRequest,
		)
		return
	}
	if remoteID == "" {
		s.writeProviderWebhookAcknowledgement(writer, schema)
		return
	}
	_, err = s.providerWebhooks.IngestProviderWebhook(
		request.Context(),
		generation.ProviderWebhookCommand{
			ProviderID: schema.ProviderID,
			RemoteID:   remoteID,
			Payload:    payload,
			EventType:  schema.EventType,
		},
	)
	switch {
	case err == nil:
		s.writeProviderWebhookAcknowledgement(writer, schema)
	case generation.IsCode(err, generation.ErrNotFound):
		s.logger.Info(
			"provider webhook ignored for unknown upstream job",
			"provider_id",
			schema.ProviderID,
			"remote_id",
			remoteID,
		)
		s.writeProviderWebhookAcknowledgement(writer, schema)
	case generation.IsCode(err, generation.ErrInvalidInput):
		s.writeProviderWebhookError(
			writer,
			schema,
			schema.API.Statuses.BadRequest,
			schema.API.Values.BadRequest,
		)
	default:
		s.logger.Error(
			"provider webhook ingestion failed",
			"provider_id",
			schema.ProviderID,
			"remote_id",
			remoteID,
			"error",
			err,
		)
		s.writeProviderWebhookError(
			writer,
			schema,
			schema.API.Statuses.Unavailable,
			schema.API.Values.Unavailable,
		)
	}
}

func (s *Server) matchProviderWebhook(
	path string,
) (providerWebhookBinding, string, bool) {
	paths := []string{path}
	if path != "/" && strings.HasSuffix(path, "/") {
		// 旧 Next/Webhook 配置长期公开的是带尾斜杠 URL。只兼容一个
		// 额外尾斜杠；不会吞掉路径中的斜杠，也不会扩大 secret 段。
		paths = append(paths, strings.TrimSuffix(path, "/"))
	}
	for _, binding := range s.providerWebhookBindings {
		for _, candidate := range paths {
			pathSecret, matched := binding.route.Match(candidate)
			if matched {
				return binding, pathSecret, true
			}
		}
	}
	return providerWebhookBinding{}, "", false
}

func (s *Server) decodeProviderWebhookPayload(
	writer http.ResponseWriter,
	request *http.Request,
	schema catalog.ProviderWebhookSchema,
) ([]byte, string, error) {
	body := http.MaxBytesReader(
		writer,
		request.Body,
		s.application.Runtime.HTTP.JSONBodyMaxBytes,
	)
	defer body.Close()
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, "", err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, "", err
	}
	for _, field := range schema.PayloadJobIDFields {
		raw, exists := object[field]
		if !exists {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			return payload, value, nil
		}
	}
	return payload, "", nil
}

func providerWebhookRequestKey(
	request *http.Request,
	schema catalog.ProviderWebhookSchema,
) string {
	for _, header := range schema.KeyHeaders {
		if value := request.Header.Get(header); value != "" {
			return value
		}
	}
	return ""
}

func providerWebhookSecretEqual(expected string, actual string) bool {
	if expected == "" {
		return false
	}
	expectedDigest := sha256.Sum256([]byte(expected))
	actualDigest := sha256.Sum256([]byte(actual))
	return subtle.ConstantTimeCompare(
		expectedDigest[:],
		actualDigest[:],
	) == 1
}

func (s *Server) providerWebhookTimestamp(
	schema catalog.ProviderWebhookSchema,
) string {
	return s.clock().UTC().Format(schema.API.TimestampLayout)
}

func (s *Server) writeProviderWebhookAcknowledgement(
	writer http.ResponseWriter,
	schema catalog.ProviderWebhookSchema,
) {
	writeJSON(writer, schema.API.Statuses.OK, map[string]any{
		schema.API.Fields.Received:  true,
		schema.API.Fields.Timestamp: s.providerWebhookTimestamp(schema),
	})
}

func (s *Server) writeProviderWebhookError(
	writer http.ResponseWriter,
	schema catalog.ProviderWebhookSchema,
	status int,
	message string,
) {
	writeJSON(writer, status, map[string]any{
		schema.API.Fields.Error: message,
	})
}

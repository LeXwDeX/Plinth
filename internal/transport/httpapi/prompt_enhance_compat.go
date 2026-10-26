package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/promptenhance"
)

func (s *Server) legacyPromptEnhance(
	writer http.ResponseWriter,
	request *http.Request,
	_ generation.OwnerID,
) {
	body, ok := s.decodePromptEnhanceBody(writer, request)
	if !ok {
		return
	}
	contract := s.application.Chat.API.PromptEnhance
	prompt := strings.TrimSpace(rawJSONString(body[contract.PromptField]))
	if prompt == "" {
		s.writePromptEnhanceError(
			writer,
			http.StatusBadRequest,
			"error.prompt_enhance_required",
		)
		return
	}
	result, err := s.promptEnhancer.Enhance(
		request.Context(),
		promptenhance.Request{
			Prompt: prompt,
			Capability: generation.Capability(
				rawJSONString(body[contract.CapabilityField]),
			),
			ModelID: rawJSONString(body[contract.ModelField]),
		},
	)
	if errors.Is(err, agents.ErrNotConfigured) {
		// Unavailable, not failed: the deployment has no Agent settings (#253).
		s.logger.Warn("提示词增强不可用：Agent 对话模型未配置", "error", err)
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
			contract.ErrorField: s.application.Chat.Presentation.Messages.AgentNotConfigured,
		})
		return
	}
	if err != nil {
		s.logger.Error("提示词增强失败", "error", err)
		s.writePromptEnhanceError(
			writer,
			http.StatusInternalServerError,
			"error.prompt_enhance_failed",
		)
		return
	}
	payload := map[string]any{
		contract.ResultField:               result.Prompt,
		contract.PromptResultField:         result.Prompt,
		contract.NegativePromptResultField: result.NegativePrompt,
	}
	writeJSON(writer, http.StatusOK, payload)
}

func (s *Server) withPromptEnhanceIdentity(
	next identityHandler,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		principal, err := s.identity.Identify(request)
		if err != nil {
			writeJSON(
				writer,
				http.StatusUnauthorized,
				map[string]string{
					s.application.Chat.API.PromptEnhance.ErrorField: s.
						application.Authentication.Messages.Unauthenticated,
				},
			)
			return
		}
		next(writer, request, generation.OwnerID(principal.ID()))
	}
}

func (s *Server) decodePromptEnhanceBody(
	writer http.ResponseWriter,
	request *http.Request,
) (map[string]json.RawMessage, bool) {
	request.Body = http.MaxBytesReader(
		writer,
		request.Body,
		s.application.Runtime.HTTP.JSONBodyMaxBytes,
	)
	decoder := json.NewDecoder(request.Body)
	var body map[string]json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		s.writePromptEnhanceError(
			writer,
			http.StatusBadRequest,
			"error.invalid_json",
		)
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writePromptEnhanceError(
			writer,
			http.StatusBadRequest,
			"error.trailing_json",
		)
		return nil, false
	}
	return body, true
}

func (s *Server) writePromptEnhanceError(
	writer http.ResponseWriter,
	status int,
	messageKey string,
) {
	writeJSON(
		writer,
		status,
		map[string]string{
			s.application.Chat.API.PromptEnhance.ErrorField: s.localeMessage(
				writer,
				messageKey,
			),
		},
	)
}

func rawJSONString(value json.RawMessage) string {
	var result string
	_ = json.Unmarshal(value, &result)
	return result
}

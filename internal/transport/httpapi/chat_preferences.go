package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// chatPreferenceWire renders one stored preference with the Schema's member
// names. The write members and the read members share the preference object;
// the attachment names describe the object the record is about.
func (s *Server) chatPreferenceWire(preference chat.Preference) map[string]any {
	fields := s.application.Chat.API.Preference
	response := s.application.Chat.API.ResponseFields
	attachment := s.application.Chat.API.Attachment
	alternatives := preference.Alternatives
	if alternatives == nil {
		alternatives = []string{}
	}
	return map[string]any{
		response.ID:         preference.ID,
		fields.Kind:         preference.Kind,
		fields.Source:       preference.Source,
		fields.Direction:    preference.Direction,
		fields.ReasonPreset: preference.ReasonPreset,
		fields.ReasonText:   preference.ReasonText,
		fields.Active:       preference.Active,
		fields.Explicit:     preference.Explicit,
		fields.UserEdited:   preference.UserEdited,
		fields.Alternatives: alternatives,
		attachment.Handle:   preference.Handle,
		attachment.TaskID:   preference.TaskID,
		attachment.AssetID:  preference.AssetID,
		response.CreatedAt:  preference.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// chatPreferencesWire renders the session's active preference records under
// the response member the Schema declares.
func (s *Server) chatPreferencesWire(preferences []chat.Preference) map[string]any {
	member := s.application.Chat.API.ResponseFields.Preferences
	items := make([]map[string]any, 0, len(preferences))
	for _, preference := range preferences {
		items = append(items, s.chatPreferenceWire(preference))
	}
	return map[string]any{member: items}
}

func (s *Server) writeChatPreferencesError(writer http.ResponseWriter, err error) {
	noteCause(writer, err)
	messages := s.application.Chat.Presentation.Messages
	switch {
	case errors.Is(err, chat.ErrSessionNotFound), errors.Is(err, chat.ErrPreferenceNotFound):
		// A missing session, another owner's session and another owner's
		// preference are the same answer, so the route leaks nothing.
		s.writeChatError(writer, http.StatusNotFound, messages.SessionNotFound)
	case errors.Is(err, chat.ErrPreferenceRejected), errors.Is(err, chat.ErrPreferenceTooLong):
		s.writeChatError(writer, http.StatusBadRequest, messages.InvalidParameters)
	default:
		s.writeChatInternalError(writer, err)
	}
}

// getChatSessionPreferences lists the caller's own active preference records.
func (s *Server) getChatSessionPreferences(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	preferences, err := s.chat.ListPreferences(request.Context(), string(owner), request.PathValue("sessionID"))
	if err != nil {
		s.writeChatPreferencesError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, s.chatPreferencesWire(preferences))
}

// postChatSessionPreferences receives either an explicit rejection (an
// attachment claim plus an optional reason preset and text) or a weak signal
// (a signal name plus the claimed handles). The two shapes never mix.
func (s *Server) postChatSessionPreferences(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil || body == nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	fields := s.application.Chat.API.Preference
	sessionID := request.PathValue("sessionID")
	var signal string
	_ = json.Unmarshal(body[fields.Signal], &signal)
	if signal != "" {
		refs := s.chatAttachmentRefs(body, fields.Handles)
		preferences, err := s.chat.RecordSignal(request.Context(), string(owner), sessionID, signal, refs)
		if err != nil {
			s.writeChatPreferencesError(writer, err)
			return
		}
		writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
		writeJSON(writer, http.StatusOK, s.chatPreferencesWire(preferences))
		return
	}
	claim := s.chatAttachmentRef(body)
	if claim.Kind == "" && claim.URL == "" && claim.AssetID == "" && claim.TaskID == "" && claim.CaptureID == "" {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	var reasonPreset, reasonText string
	_ = json.Unmarshal(body[fields.ReasonPreset], &reasonPreset)
	_ = json.Unmarshal(body[fields.ReasonText], &reasonText)
	preference, err := s.chat.RecordRejection(request.Context(), string(owner), sessionID, claim, reasonPreset, reasonText)
	if err != nil {
		s.writeChatPreferencesError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{
		s.application.Chat.API.ResponseFields.Preferences: []map[string]any{s.chatPreferenceWire(preference)},
	})
}

// putChatSessionPreference edits one record: its direction, its reason and
// whether it stays active.
func (s *Server) putChatSessionPreference(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	var body map[string]json.RawMessage
	reader := http.MaxBytesReader(writer, request.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes)
	if err := json.NewDecoder(reader).Decode(&body); err != nil || body == nil {
		s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
		return
	}
	fields := s.application.Chat.API.Preference
	edit := chat.PreferenceEdit{}
	if raw, present := body[fields.Direction]; present {
		var direction string
		if json.Unmarshal(raw, &direction) != nil {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		edit.Direction = &direction
	}
	if raw, present := body[fields.ReasonPreset]; present {
		var preset string
		if json.Unmarshal(raw, &preset) != nil {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		edit.ReasonPreset = &preset
	}
	if raw, present := body[fields.ReasonText]; present {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		edit.ReasonText = &text
	}
	if raw, present := body[fields.Active]; present {
		var active bool
		if json.Unmarshal(raw, &active) != nil {
			s.writeChatError(writer, http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters)
			return
		}
		edit.Active = &active
	}
	preference, err := s.chat.UpdatePreference(request.Context(), string(owner),
		request.PathValue("sessionID"), request.PathValue("preferenceID"), edit)
	if err != nil {
		s.writeChatPreferencesError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{
		s.application.Chat.API.ResponseFields.Preferences: []map[string]any{s.chatPreferenceWire(preference)},
	})
}

// deleteChatSessionPreference hides one record; the next turn no longer
// injects it.
func (s *Server) deleteChatSessionPreference(writer http.ResponseWriter, request *http.Request, owner generation.OwnerID) {
	if err := s.chat.DeletePreference(request.Context(), string(owner),
		request.PathValue("sessionID"), request.PathValue("preferenceID")); err != nil {
		s.writeChatPreferencesError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, map[string]any{
		s.application.Chat.API.ResponseFields.ID:     request.PathValue("preferenceID"),
		s.application.Chat.API.Preference.Active:     false,
		s.application.Chat.API.Preference.UserEdited: false,
	})
}

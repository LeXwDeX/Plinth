package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

// connectionProber is the administrator's "test connection" (#322 Scope 2):
// one minimal chat request to the connection carrying a small generated
// picture. When the picture is refused, one text-only request tells whether
// the connection itself works. Only a classified outcome is kept: the
// upstream body never reaches the settings, the page, the audit or the log.
func connectionProber(adapter agents.Adapter, schema catalog.ApplicationSchema, logger *slog.Logger) llmsettings.Prober {
	probe := schema.LLMSettings.Probe
	parts := schema.Chat.Context
	picture := probePicture(probe.ImageSize)
	return func(ctx context.Context, connection llmsettings.Connection) llmsettings.ProbeResult {
		ctx, cancel := context.WithTimeout(ctx, time.Duration(probe.TimeoutSeconds)*time.Second)
		defer cancel()
		request := agents.Request{
			Model:      connection.Model,
			Connection: connection.ID,
			// The cheapest level the model offers keeps the test small.
			ReasoningEffort: probeEffort(schema.Chat.Agent.Thinking, connection.Model),
			Temperature:     schema.Chat.Agent.Temperature,
			Messages: []agents.Message{{Role: schema.Chat.Roles.User, Parts: []agents.ContentPart{
				{Kind: parts.TextPartKind, Text: probe.Prompt},
				{Kind: parts.ImagePartKind, URL: picture, Detail: parts.ImageDetail},
			}}},
		}
		_, err := adapter.Complete(ctx, request)
		if err == nil {
			return llmsettings.ProbeResult{Reachable: true, Vision: llmsettings.VisionSupported, Outcome: llmsettings.OutcomeOK}
		}
		if pictureRefused(err) {
			request.Messages = []agents.Message{{Role: schema.Chat.Roles.User, Content: probe.Prompt}}
			if _, textErr := adapter.Complete(ctx, request); textErr == nil {
				return llmsettings.ProbeResult{Reachable: true, Vision: llmsettings.VisionUnsupported, Outcome: llmsettings.OutcomeOK}
			} else {
				err = textErr
			}
		}
		outcome := probeOutcome(err)
		if logger != nil {
			attributes := []any{"connection_id", connection.ID, "outcome", outcome}
			var refused *agents.StatusError
			if errors.As(err, &refused) {
				attributes = append(attributes, "status", refused.Status)
			}
			logger.Warn("LLM 连接测试未通过", attributes...)
		}
		return llmsettings.ProbeResult{Vision: llmsettings.VisionUnknown, Outcome: outcome}
	}
}

// pictureRefused: the model said it cannot read pictures, or refused the
// request with a picture as invalid input.
func pictureRefused(err error) bool {
	if errors.Is(err, agents.ErrImagesRejected) {
		return true
	}
	var refused *agents.StatusError
	if errors.As(err, &refused) {
		switch refused.Status {
		case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
			return true
		}
	}
	return false
}

func probeOutcome(err error) string {
	var refused *agents.StatusError
	switch {
	case errors.Is(err, agents.ErrNotConfigured):
		return llmsettings.OutcomeNotConfigured
	case errors.Is(err, context.DeadlineExceeded):
		return llmsettings.OutcomeTimeout
	case errors.As(err, &refused):
		switch refused.Status {
		case http.StatusUnauthorized, http.StatusForbidden:
			return llmsettings.OutcomeUnauthorized
		case http.StatusNotFound:
			return llmsettings.OutcomeNotFound
		case http.StatusTooManyRequests:
			return llmsettings.OutcomeRateLimited
		}
		return llmsettings.OutcomeUpstreamError
	}
	return llmsettings.OutcomeUnreachable
}

func probeEffort(thinking *catalog.ChatThinkingSchema, model string) string {
	profile, _ := thinking.ProfileFor(model)
	if !profile.OffersEffort() {
		return ""
	}
	return profile.EffortLevels[0]
}

// probePicture is a small two-colour PNG as a data URI.
func probePicture(size int) string {
	picture := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			shade := color.RGBA{R: 230, G: 90, B: 40, A: 255}
			if x >= size/2 {
				shade = color.RGBA{R: 40, G: 120, B: 230, A: 255}
			}
			picture.Set(x, y, shade)
		}
	}
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, picture)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

// chatModelSource turns the administrator's effective settings into the
// chat sessions' model set.
func chatModelSource(settings *llmsettings.Service) chat.ChatModelSource {
	return func(ctx context.Context) (chat.ChatModelSet, error) {
		effective, err := settings.Effective(ctx)
		if err != nil {
			return chat.ChatModelSet{}, err
		}
		set := chat.ChatModelSet{DefaultID: effective.DefaultID, Options: make([]chat.ChatModelOption, 0, len(effective.Models))}
		for _, model := range effective.Models {
			set.Options = append(set.Options, chat.ChatModelOption{ID: model.ID, Model: model.Model,
				EffortLevels: model.EffortLevels, DefaultEffort: model.DefaultEffort, Window: model.Window})
		}
		return set, nil
	}
}

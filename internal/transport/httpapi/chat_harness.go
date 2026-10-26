package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
)

type harnessView struct {
	PriceMode         string                          `json:"price_mode,omitempty"`
	NativeQuote       *generation.NativeQuote         `json:"native_quote,omitempty"`
	SourceViews       []resolve.SourceView            `json:"source_views,omitempty"`
	Selection         harness.Selection               `json:"selection"`
	Status            string                          `json:"status"`
	Reason            string                          `json:"reason"`
	MissingParameters []string                        `json:"missing_parameters"`
	CanPropose        bool                            `json:"can_propose"`
	ActiveOperation   *catalog.PublicHarnessOperation `json:"active_operation,omitempty"`
	EstimatedCredits  int                             `json:"estimated_credits"`
	Turn              *chat.HarnessTurnView           `json:"turn,omitempty"`
}

func (s *Server) writeHarnessError(w http.ResponseWriter, err error) {
	noteCause(w, err)
	var domain *generation.Error
	if errors.As(err, &domain) {
		s.writeDomainError(w, err)
		return
	}
	code, message := http.StatusInternalServerError, s.application.Chat.Presentation.Messages.InternalError
	switch {
	case errors.Is(err, harness.ErrConflict), errors.Is(err, harness.ErrStale):
		code, message = http.StatusConflict, s.application.Chat.Harness.ChangedMessage
	case errors.Is(err, harness.ErrNotFound), errors.Is(err, chat.ErrSessionNotFound):
		code, message = http.StatusNotFound, s.application.Chat.Presentation.Messages.SessionNotFound
	case errors.Is(err, harness.ErrInvalid):
		code, message = http.StatusBadRequest, s.application.Chat.Presentation.Messages.InvalidParameters
	case errors.Is(err, resolve.ErrUnavailable):
		code, message = http.StatusServiceUnavailable, s.application.Chat.Harness.UnavailableMessage
	}
	writeJSON(w, code, map[string]string{"detail": message})
}

func (s *Server) writeHarnessView(w http.ResponseWriter, r *http.Request, owner string) {
	snapshot, err := s.harness.Snapshot(r.Context(), owner, r.PathValue("sessionID"))
	if err != nil {
		s.writeHarnessError(w, err)
		return
	}
	turn, err := s.chat.CurrentHarnessTurn(r.Context(), owner, r.PathValue("sessionID"))
	if err != nil {
		s.writeHarnessError(w, err)
		return
	}
	view := harnessView{Selection: snapshot.Selection, Status: snapshot.Status, Reason: snapshot.Reason, MissingParameters: snapshot.MissingParameters, CanPropose: snapshot.CanPropose, SourceViews: snapshot.SourceViews, Turn: turn}
	if snapshot.Operation != nil {
		op := snapshot.Operation.PublicHarnessOperation
		view.ActiveOperation = &op
	}
	if snapshot.Offering != nil {
		input, _ := json.Marshal(harnessEstimateInput(*snapshot.Offering, snapshot.Input))
		if snapshot.Offering.NativePricing != nil {
			view.PriceMode = "native"
			view.NativeQuote, _ = snapshot.Offering.DefaultNativeQuote(input)
		} else {
			view.EstimatedCredits = snapshot.Offering.EstimateTotalCredits(input)
		}
	}
	w.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(w, http.StatusOK, view)
}

// harnessEstimateInput fills only a missing fixed cost selection for display.
// It never mutates the durable selection or serves as execution input.
func harnessEstimateInput(offering generation.Offering, input map[string]json.RawMessage) map[string]json.RawMessage {
	if offering.Cost == nil || offering.Cost.SelectionParameter == "" {
		return input
	}
	name := offering.Cost.SelectionParameter
	if _, present := input[name]; present {
		return input
	}
	for _, parameter := range offering.Parameters {
		if parameter.Name != name || !parameter.Fixed || len(parameter.Default) == 0 {
			continue
		}
		estimated := make(map[string]json.RawMessage, len(input)+1)
		for key, value := range input {
			estimated[key] = value
		}
		estimated[name] = parameter.Default
		return estimated
	}
	return input
}

func (s *Server) getChatSessionHarness(w http.ResponseWriter, r *http.Request, owner generation.OwnerID) {
	if s.harness == nil {
		s.writeHarnessError(w, resolve.ErrUnavailable)
		return
	}
	s.writeHarnessView(w, r, string(owner))
}
func (s *Server) putChatSessionHarness(w http.ResponseWriter, r *http.Request, principal access.Principal) {
	if s.harness == nil {
		s.writeHarnessError(w, resolve.ErrUnavailable)
		return
	}
	var body struct {
		ExpectedRevision *int64             `json:"expected_revision"`
		Selection        *harness.Selection `json:"selection"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.application.Runtime.HTTP.JSONBodyMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.ExpectedRevision == nil || body.Selection == nil || *body.ExpectedRevision < 0 {
		s.writeHarnessError(w, harness.ErrInvalid)
		return
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		s.writeHarnessError(w, harness.ErrInvalid)
		return
	}
	_, err := s.harness.Update(r.Context(), principal.ID(), r.PathValue("sessionID"), *body.ExpectedRevision, *body.Selection)
	if err != nil {
		s.writeHarnessError(w, err)
		return
	}
	s.writeHarnessView(w, r, principal.ID())
}

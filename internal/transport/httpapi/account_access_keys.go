package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/accesskeys"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// accessKeyWire is one personal access key as the browser sees it: its
// identifying prefix, scopes and dates, never the key or its digest.
type accessKeyWire struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	// Active is false once the key was revoked or expired.
	Active bool `json:"active"`
}

// accessKeyCreatedWire is the answer of an issue request: the key's record and
// its plaintext, the only time the plaintext is ever returned.
type accessKeyCreatedWire struct {
	accessKeyWire
	Key string `json:"key"`
}

type accessKeyListWire struct {
	Data []accessKeyWire `json:"data"`
}

func accessKeyRecordWire(record accesskeys.Record, now time.Time) accessKeyWire {
	optional := func(value time.Time) *time.Time {
		if value.IsZero() {
			return nil
		}
		value = value.UTC()
		return &value
	}
	return accessKeyWire{
		ID: record.ID, Name: record.Name, Prefix: record.Prefix, Scopes: append([]string{}, record.Scopes...),
		CreatedAt: record.CreatedAt.UTC(), LastUsedAt: optional(record.LastUsedAt),
		ExpiresAt: optional(record.ExpiresAt), RevokedAt: optional(record.RevokedAt),
		Active: record.Active(now),
	}
}

// registerAccountAccessKeysContract serves the person's personal access keys
// (#332): list and issue on one route, revoke on the item route. All three
// need a real browser session, so a key can never manage keys. They are
// registered only when the Catalog declares application.access_keys and its
// routes.
func (s *Server) registerAccountAccessKeysContract() {
	routes := s.application.Runtime.HTTP.Routes
	if routes.AccountAccessKeys == "" || routes.AccountAccessKey == "" || s.accessKeys == nil || s.application.AccessKeys == nil {
		return
	}
	s.mux.HandleFunc("GET "+routes.AccountAccessKeys, s.withPrincipal(s.listAccountAccessKeys))
	s.mux.HandleFunc("POST "+routes.AccountAccessKeys, s.withPrincipal(s.createAccountAccessKey))
	s.mux.HandleFunc("DELETE "+routes.AccountAccessKey, s.withPrincipal(s.revokeAccountAccessKey))
}

// accessKeyOwner is the person a key route acts for. The session is checked
// the way a payment decision's is: the ordinary identity resolver may also
// accept a Bearer token or a development credential, and neither is a person
// at a browser. It writes the refusal itself.
func (s *Server) accessKeyOwner(writer http.ResponseWriter, request *http.Request, principal access.Principal) (string, bool) {
	if !principal.Can(access.PermissionTasksReadOwn) {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return "", false
	}
	owner := generation.OwnerID(principal.ID())
	if _, human := s.taskHumanPrincipal(request, owner); !human {
		s.writeMessageProblem(writer, http.StatusForbidden, "forbidden", "error.forbidden")
		return "", false
	}
	return principal.ID(), true
}

func (s *Server) listAccountAccessKeys(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.accessKeyOwner(writer, request, principal)
	if !ok {
		return
	}
	records, err := s.accessKeys.List(request.Context(), owner)
	if err != nil {
		s.writeAccessKeyError(writer, request, err)
		return
	}
	now := time.Now()
	response := accessKeyListWire{Data: make([]accessKeyWire, 0, len(records))}
	for _, record := range records {
		response.Data = append(response.Data, accessKeyRecordWire(record, now))
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusOK, response)
}

func (s *Server) createAccountAccessKey(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.accessKeyOwner(writer, request, principal)
	if !ok {
		return
	}
	var body struct {
		Name    string   `json:"name"`
		Scopes  []string `json:"scopes"`
		TTLDays *int     `json:"ttl_days"`
	}
	if err := s.decodeJSON(writer, request, &body); err != nil {
		return
	}
	command := accesskeys.CreateRequest{OwnerID: owner, Name: body.Name, Scopes: body.Scopes}
	if body.TTLDays != nil {
		// A lifetime is a positive number of days; the maximum is the
		// service's to enforce.
		if *body.TTLDays < 1 || *body.TTLDays > int(^uint32(0)>>1)/24 {
			s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
			return
		}
		command.TTL = time.Duration(*body.TTLDays) * 24 * time.Hour
	}
	created, err := s.accessKeys.Create(request.Context(), command)
	if err != nil {
		s.writeAccessKeyError(writer, request, err)
		return
	}
	// The audit names the key by its ID and visible prefix; the key itself is
	// never recorded.
	audit := s.application.AccessKeys.Audit
	s.recordProductAudit(request, administration.AuditEvent{
		Action:       audit.CreateAction,
		ActorID:      principal.ID(),
		ActorName:    principal.Claims.Name,
		ResourceType: audit.ResourceType,
		ResourceID:   created.Record.ID,
		Detail: map[string]any{
			"name":       created.Record.Name,
			"prefix":     created.Record.Prefix,
			"scopes":     created.Record.Scopes,
			"expires_at": created.Record.ExpiresAt.UTC().Format(time.RFC3339),
		},
	})
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writeJSON(writer, http.StatusCreated, accessKeyCreatedWire{
		accessKeyWire: accessKeyRecordWire(created.Record, time.Now()),
		Key:           created.Key,
	})
}

// revokeAccountAccessKey ends the person's key at once. Revoking a key that is
// already revoked succeeds and writes no audit entry: it is not a change.
func (s *Server) revokeAccountAccessKey(writer http.ResponseWriter, request *http.Request, principal access.Principal) {
	owner, ok := s.accessKeyOwner(writer, request, principal)
	if !ok {
		return
	}
	record, changed, err := s.accessKeys.Revoke(request.Context(), owner, request.PathValue("id"))
	if err != nil {
		s.writeAccessKeyError(writer, request, err)
		return
	}
	if changed {
		audit := s.application.AccessKeys.Audit
		s.recordProductAudit(request, administration.AuditEvent{
			Action:       audit.RevokeAction,
			ActorID:      principal.ID(),
			ActorName:    principal.Claims.Name,
			ResourceType: audit.ResourceType,
			ResourceID:   record.ID,
			Detail: map[string]any{
				"name":   record.Name,
				"prefix": record.Prefix,
			},
		})
	}
	writer.Header().Set("Cache-Control", s.application.Runtime.HTTP.PrivateCacheControl)
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeAccessKeyError(writer http.ResponseWriter, request *http.Request, err error) {
	noteCause(writer, err)
	switch {
	case errors.Is(err, accesskeys.ErrInvalid):
		s.writeMessageProblem(writer, http.StatusBadRequest, "invalid_input", "error.invalid_input")
	case errors.Is(err, accesskeys.ErrNotFound):
		s.writeMessageProblem(writer, http.StatusNotFound, "not_found", "error.not_found")
	case errors.Is(err, accesskeys.ErrLimit):
		detail := s.textIn(requestLocale(request), s.application.AccessKeys.Messages.LimitReached)
		detail = strings.ReplaceAll(detail, "{max}", strconv.Itoa(s.application.AccessKeys.MaxKeysPerUser))
		writeProblem(writer, http.StatusConflict, "access_key_limit", detail)
	default:
		s.writeMessageProblem(writer, http.StatusInternalServerError, "internal", "error.internal")
	}
}

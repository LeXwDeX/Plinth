package accesskeys

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/authentication"
)

// Directory is the person lookup the authenticator needs. The sqlite
// repository is the same directory the sign-in uses.
type Directory interface {
	FindByID(context.Context, string) (authentication.UserRecord, bool, error)
}

// Authenticator resolves "Authorization: Bearer <access key>" to a Principal.
// It is not part of the main HTTP server's identity: the main server does not
// accept access keys, and a key sent to it is rejected like any unknown Bearer
// token. A separate listener, such as the external MCP one, uses this
// authenticator as its identity resolver.
//
// The Principal is the key owner's scope only. The user's roles never apply,
// so an administrator's key has no administrator permissions, and the Principal
// is of the owner who holds the key, never of anyone else. A key whose owner
// was deleted or disabled no longer authenticates.
type Authenticator struct {
	Keys   *Service
	Users  Directory
	Policy *access.Policy
}

// ErrNotAKey is returned when the request carries no Bearer token of the shape
// of an access key. It is not a refusal of a key: the request has none.
var ErrNotAKey = errors.New("access keys: no access key in the request")

// Identify satisfies httpapi.IdentityResolver. It returns ErrNotAKey when the
// request has no access key, ErrUnauthorized when it has one that cannot
// authenticate (or whose owner can no longer act), and another error when the
// key or its owner could not be looked up.
func (a *Authenticator) Identify(request *http.Request) (access.Principal, error) {
	if a == nil || a.Keys == nil || a.Users == nil || a.Policy == nil {
		return access.Principal{}, errors.New("access keys: authenticator is not configured")
	}
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(header, "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || !a.Keys.LooksLikeKey(token) {
		return access.Principal{}, ErrNotAKey
	}
	authenticated, err := a.Keys.Authenticate(request.Context(), token)
	if err != nil {
		return access.Principal{}, err
	}
	record, found, err := a.Users.FindByID(request.Context(), authenticated.OwnerID)
	if err != nil {
		return access.Principal{}, err
	}
	if !found || record.Deleted || !record.Active {
		return access.Principal{}, ErrUnauthorized
	}
	name := record.Nickname
	if name == "" {
		name = record.Name
	}
	if name == "" {
		name = record.Username
	}
	return a.Policy.AccessKeyPrincipal(
		access.Claims{ID: record.ID, Name: name, Email: record.Email},
		authenticated.KeyID, authenticated.Scopes), nil
}

package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/access"
)

var errUnauthenticated = errors.New("未认证")

type IdentityResolver interface {
	Identify(*http.Request) (access.Principal, error)
}

type DevelopmentIdentity struct {
	DefaultClaims access.Claims
	Policy        *access.Policy
}

func (d DevelopmentIdentity) Identify(request *http.Request) (access.Principal, error) {
	if d.Policy == nil {
		return access.Principal{}, errors.New("development identity 缺少 access policy")
	}
	claims := d.DefaultClaims
	if owner := strings.TrimSpace(request.Header.Get("X-User-ID")); owner != "" {
		claims.ID = owner
	}
	if strings.TrimSpace(claims.ID) == "" {
		return access.Principal{}, errUnauthenticated
	}
	return d.Policy.Principal(claims), nil
}

type BearerIdentity struct {
	tokens []bearerToken
	policy *access.Policy
}

type bearerToken struct {
	token  string
	claims access.Claims
}

func NewBearerIdentity(
	tokens map[string]access.Claims,
	policy *access.Policy,
) (*BearerIdentity, error) {
	if policy == nil {
		return nil, errors.New("Bearer identity 缺少 access policy")
	}
	identity := &BearerIdentity{policy: policy}
	for token, claims := range tokens {
		token = strings.TrimSpace(token)
		if len(token) < 24 || strings.TrimSpace(claims.ID) == "" {
			return nil, errors.New("Bearer token 至少需要 24 个字符，且必须绑定 owner")
		}
		identity.tokens = append(identity.tokens, bearerToken{token: token, claims: claims})
	}
	if len(identity.tokens) == 0 {
		return nil, errors.New("至少需要一个 Bearer token")
	}
	return identity, nil
}

func (b *BearerIdentity) Identify(request *http.Request) (access.Principal, error) {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return access.Principal{}, errUnauthenticated
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	for _, candidate := range b.tokens {
		if len(provided) == len(candidate.token) &&
			subtle.ConstantTimeCompare([]byte(provided), []byte(candidate.token)) == 1 {
			return b.policy.Principal(candidate.claims), nil
		}
	}
	return access.Principal{}, errUnauthenticated
}

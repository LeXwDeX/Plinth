package access

import "slices"

// CredentialKind names the credential an identity resolver authenticated a
// Principal with (#332). Before personal access keys existed the Principal did
// not say: whether a request came from a person at a browser was each route's
// own look at the request headers. A route that needs a person still verifies
// the session itself (an approval, a setting that lifts a guard); the kind is
// the explicit fact other code can branch on and log.
type CredentialKind string

const (
	// CredentialUnknown is the zero value: a Principal that no identity
	// resolver built (tests, tooling). Nothing may treat it as a person.
	CredentialUnknown CredentialKind = ""
	// CredentialBrowserSession is a signed-in browser session cookie.
	CredentialBrowserSession CredentialKind = "browser_session"
	// CredentialDeploymentToken is the deployment-level static Bearer token of
	// the auth_mode bearer deployment.
	CredentialDeploymentToken CredentialKind = "deployment_token"
	// CredentialDevelopment is the development identity (auth_mode development
	// or the non-production mock user): there is no credential at all.
	CredentialDevelopment CredentialKind = "development"
	// CredentialAccessKey is a person's personal access key (#332). Its
	// Principal carries the person's owner scope only, never an administrator's
	// permissions, and the scopes the key was issued with.
	CredentialAccessKey CredentialKind = "access_key"
)

// Credential is how a Principal was authenticated.
type Credential struct {
	Kind CredentialKind
	// KeyID is the public ID of the access key (CredentialAccessKey only).
	KeyID string
	// Scopes are the scopes the access key was issued with (CredentialAccessKey
	// only).
	Scopes []string
}

// HasScope reports whether the credential was issued with the scope. Only an
// access key has scopes; every other kind answers false.
func (c Credential) HasScope(scope string) bool {
	return c.Kind == CredentialAccessKey && slices.Contains(c.Scopes, scope)
}

// Credential is how the Principal was authenticated. The scopes are a copy.
func (p Principal) Credential() Credential {
	credential := p.credential
	credential.Scopes = slices.Clone(credential.Scopes)
	return credential
}

// WithCredential returns the Principal marked as authenticated by the
// credential. Identity resolvers call it where they build a Principal.
func (p Principal) WithCredential(credential Credential) Principal {
	credential.Scopes = slices.Clone(credential.Scopes)
	p.credential = credential
	return p
}

// AccessKeyPrincipal is the Principal of a personal access key (#332): the
// person's owner scope and nothing more. The claims' roles never travel with a
// key, so an administrator's key has no administrator permissions and cannot
// read another owner's data; only the default role's permissions apply, minus
// the one that reads across owners.
func (p *Policy) AccessKeyPrincipal(claims Claims, keyID string, scopes []string) Principal {
	claims.Roles = nil
	principal := p.Principal(claims)
	principal.admin = false
	delete(principal.permissions, PermissionTasksReadAny)
	return principal.WithCredential(Credential{Kind: CredentialAccessKey, KeyID: keyID, Scopes: scopes})
}

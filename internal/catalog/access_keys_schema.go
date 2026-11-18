package catalog

// AccessKeysSchema is application.access_keys (#332): the personal access keys
// a person issues to let a program act as them without a browser, such as an
// external MCP client. A key is "<key_prefix>_<id>_<secret>"; only its digest
// is stored and the plaintext is shown once, at creation. A key carries the
// person's owner scope only and can never manage keys.
//
// The section is optional so older Catalog documents stay valid. Without it
// the management routes are not registered and no key can be issued.
type AccessKeysSchema struct {
	// Enabled is the overall switch's initial state. An administrator can
	// turn the service off and on from the administration view; until then
	// this value applies. Off refuses every key request and signed download
	// on the external listener.
	Enabled bool `json:"enabled"`
	// KeyPrefix starts every key (lowercase letters and digits, one word).
	KeyPrefix string `json:"key_prefix"`
	// MaxKeysPerUser caps the active keys (not revoked, not expired) one
	// person holds; revoking or letting one expire gives the place back.
	MaxKeysPerUser int `json:"max_keys_per_user"`
	// DefaultTTLDays is the lifetime of a key whose request names none;
	// MaxTTLDays bounds what a request may ask for. A key always expires.
	DefaultTTLDays int `json:"default_ttl_days"`
	MaxTTLDays     int `json:"max_ttl_days"`
	// NameMaxLength is the longest key name, in characters.
	NameMaxLength int `json:"name_max_length"`
	// Scopes lists what a key may be issued for, with labels. The values are
	// the fixed vocabulary of the code that reads them (read, generate); the
	// Schema owns the labels.
	Scopes []ValueLabel `json:"scopes"`
	// LastUsedUpdateSeconds is the least time between two writes of one key's
	// last-use time; authentication in between writes nothing. Server-side
	// only.
	LastUsedUpdateSeconds int `json:"last_used_update_seconds"`
	// RateLimit bounds the traffic of the external MCP listener (#332): the
	// requests one key may make a minute, and the failed authentications one
	// client address may make a minute. Optional so older Catalog documents stay
	// valid: omitted, the listener limits nothing. Server-side only.
	RateLimit *AccessKeysRateLimitSchema `json:"rate_limit,omitempty"`
	// Messages is the copy of the server's answers. Server-side only.
	Messages AccessKeysMessagesSchema `json:"messages"`
	// Audit names the product-audit actions of issuing and revoking a key.
	Audit AccessKeysAuditSchema `json:"audit"`
}

// AccessKeysRateLimitSchema is the per-minute traffic limit of the external MCP
// listener (#332). A declared limit has both counts, each a positive integer.
// The windows are fixed minutes counted in the memory of one process: the
// numbers are per instance, and a restart starts every window clean. The copy
// of the refusals is mcp_public.messages.rate_limited and auth_throttled.
type AccessKeysRateLimitSchema struct {
	// RequestsPerMinute is the most requests one access key may make in a
	// minute, the JSON-RPC endpoint and the download route together. The next
	// one is answered 429 with the time to wait.
	RequestsPerMinute int `json:"requests_per_minute"`
	// AuthFailuresPerMinute is the most failed authentications one client
	// address (an IPv6 /64 counts as one address) may make in a minute. Past it
	// the address is answered 429 before any key is looked up, until the minute
	// ends.
	AuthFailuresPerMinute int `json:"auth_failures_per_minute"`
}

// AccessKeysMessagesSchema is the copy the server answers with.
type AccessKeysMessagesSchema struct {
	// LimitReached answers an issue request when the person already holds the
	// most active keys. {max} is the limit.
	LimitReached string `json:"limit_reached"`
}

// AccessKeysAuditSchema declares the audit actions recorded when a person
// issues or revokes a key. The key itself is never audited: only its ID, name,
// visible prefix, scopes and expiry.
type AccessKeysAuditSchema struct {
	CreateAction string `json:"create_action"`
	RevokeAction string `json:"revoke_action"`
	ResourceType string `json:"resource_type"`
}

// PublicAccessKeysSchema is the browser-visible part of application.access_keys:
// what the key dialog needs to offer the right choices and say how many keys
// there may be. The last-use interval, the rate limit, the server's messages and
// the audit declaration stay server-side.
type PublicAccessKeysSchema struct {
	KeyPrefix      string       `json:"key_prefix"`
	MaxKeysPerUser int          `json:"max_keys_per_user"`
	DefaultTTLDays int          `json:"default_ttl_days"`
	MaxTTLDays     int          `json:"max_ttl_days"`
	NameMaxLength  int          `json:"name_max_length"`
	Scopes         []ValueLabel `json:"scopes"`
}

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
	// Messages is the copy of the server's answers. Server-side only.
	Messages AccessKeysMessagesSchema `json:"messages"`
	// Audit names the product-audit actions of issuing and revoking a key.
	Audit AccessKeysAuditSchema `json:"audit"`
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
// there may be. The last-use interval, the server's messages and the audit
// declaration stay server-side.
type PublicAccessKeysSchema struct {
	KeyPrefix      string       `json:"key_prefix"`
	MaxKeysPerUser int          `json:"max_keys_per_user"`
	DefaultTTLDays int          `json:"default_ttl_days"`
	MaxTTLDays     int          `json:"max_ttl_days"`
	NameMaxLength  int          `json:"name_max_length"`
	Scopes         []ValueLabel `json:"scopes"`
}

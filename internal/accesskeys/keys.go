// Package accesskeys issues, lists, revokes and authenticates personal access
// keys (#332). A key lets a program, such as an external MCP client, act as one
// person without a browser session. Only the SHA-256 of a key is stored; the
// plaintext is returned once, at creation. A key authenticates its owner's
// scope only: it never carries an administrator's permissions, and it can
// never manage keys (the management routes need a real browser session).
package accesskeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	// ErrInvalid is returned for input the Schema does not allow: a name that
	// is empty or too long, a scope the Schema does not declare, a lifetime
	// beyond the maximum.
	ErrInvalid = errors.New("access keys: invalid input")
	// ErrLimit is returned when the person already has the most active keys the
	// Schema allows.
	ErrLimit = errors.New("access keys: active key limit reached")
	// ErrNotFound is returned when no key of that person has the ID.
	ErrNotFound = errors.New("access keys: key not found")
	// ErrUnauthorized is the one answer for a key that cannot authenticate: it
	// is malformed, unknown, wrong, revoked or expired. The caller can never
	// tell which, so a probe learns nothing about the keys that exist.
	ErrUnauthorized = errors.New("access keys: unauthorized")
)

// The scopes a key can be issued with. The ids are the fixed vocabulary of the
// code that checks them; the Schema (application.access_keys.scopes) owns their
// labels and a catalog test keeps the two in step.
const (
	// ScopeRead is the right to query and download.
	ScopeRead = "read"
	// ScopeGenerate is the right to start generation and processing. It
	// includes ScopeRead: a caller that starts work has to be able to poll the
	// Tasks it started and fetch their files.
	ScopeGenerate = "generate"
)

// scopeIncludes names, per scope, the scopes it carries with it.
var scopeIncludes = map[string][]string{ScopeGenerate: {ScopeRead}}

// EffectiveScopes is what a key may do: the scopes it was issued with plus the
// ones those include, the issued ones first and in their order. The stored key
// and its listing keep the scopes it was issued with; the identity a key
// authenticates as carries the effective ones, so every check of a scope sees
// the implication. The result is a new slice.
func EffectiveScopes(issued []string) []string {
	effective := slices.Clone(issued)
	for _, scope := range issued {
		for _, included := range scopeIncludes[scope] {
			if !slices.Contains(effective, included) {
				effective = append(effective, included)
			}
		}
	}
	return effective
}

// idBytes and secretBytes size the two random parts of a key: an 80-bit
// public ID the store looks the key up by, and a 256-bit secret.
const (
	idBytes     = 10
	secretBytes = 32
)

// encoding is lowercase base32 without padding: a key is one word for a
// double-click and has no characters that need escaping in a header or a URL.
var encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Record is one stored key. The plaintext key is not part of it.
type Record struct {
	ID      string
	OwnerID string
	Name    string
	// Prefix is the part of the key that identifies it and holds no secret:
	// "<key prefix>_<id>".
	Prefix string
	// SecretHash is the hex SHA-256 of the whole key. It never leaves the
	// server.
	SecretHash string
	Scopes     []string
	CreatedAt  time.Time
	// RequestCount is how many authenticated requests the key has made. A
	// request that fails authentication is never counted.
	RequestCount int64
	// LastUsedAt, ExpiresAt and RevokedAt are the zero time while unset.
	LastUsedAt time.Time
	ExpiresAt  time.Time
	RevokedAt  time.Time
}

// Active reports whether the key can still authenticate: not revoked and not
// past its expiry.
func (r Record) Active(now time.Time) bool {
	if !r.RevokedAt.IsZero() {
		return false
	}
	return r.ExpiresAt.IsZero() || now.Before(r.ExpiresAt)
}

// Store persists keys.
type Store interface {
	// CreateAccessKey stores the key unless its owner already has maxActive
	// active keys, in which case it returns ErrLimit and stores nothing. The
	// count and the insert are one transaction.
	CreateAccessKey(ctx context.Context, record Record, maxActive int, now time.Time) error
	// AccessKey reads a key by ID, whoever owns it. Only authentication uses it.
	AccessKey(ctx context.Context, id string) (Record, bool, error)
	// ListAccessKeys lists the owner's keys, newest first.
	ListAccessKeys(ctx context.Context, ownerID string) ([]Record, error)
	// RevokeAccessKey revokes the owner's key. The result is the key and
	// whether this call revoked it (false when it was already revoked);
	// ErrNotFound when the owner has no such key.
	RevokeAccessKey(ctx context.Context, ownerID, id string, now time.Time) (Record, bool, error)
	// IncrementAccessKeyRequests adds one to the key's cumulative count. The
	// count is added atomically, so concurrent calls never lose one. The
	// write does not require the key to be active: it counts a request that
	// was authenticated before a revoke could land.
	IncrementAccessKeyRequests(ctx context.Context, id string) error
	// RecordAccessKeyLastUse sets the key's last use time. Authentication
	// calls it at most once per interval. A revoked key is left as it was.
	RecordAccessKeyLastUse(ctx context.Context, id string, at time.Time) error
}

// Config is the Schema's application.access_keys in the form the service uses.
type Config struct {
	// KeyPrefix starts every key, before its first underscore ("plk").
	KeyPrefix string
	// MaxKeysPerUser is the most active keys one person may hold.
	MaxKeysPerUser int
	// DefaultTTL and MaxTTL bound a key's lifetime. A key always expires.
	DefaultTTL time.Duration
	MaxTTL     time.Duration
	// NameMaxLength is the longest key name, in characters.
	NameMaxLength int
	// Scopes are the scopes a key may be issued with.
	Scopes []string
	// LastUsedInterval is the least time between two writes of one key's last
	// use; authentication in between writes none. The request count is
	// written for every authenticated request regardless of this interval.
	LastUsedInterval time.Duration
	// Clock is the time source; nil means time.Now.
	Clock func() time.Time
	// Random is the entropy source; nil means crypto/rand.
	Random io.Reader
	// Logger receives the warning of a failed use write; nil discards it.
	// Nothing logged contains a key.
	Logger *slog.Logger
	// Async runs each use write; nil starts a goroutine. Tests run it inline.
	Async func(func())
}

// Service is the access key use cases.
type Service struct {
	store  Store
	config Config

	mu   sync.Mutex
	uses map[string]time.Time
}

// The uses map holds, per key, when its last use time was last written. It is
// only the throttle of that one column: every request's count goes to the
// store at once, so nothing here is a count waiting to be flushed.

// New builds the service. Every bound must be set; the Schema validates them
// before a Catalog loads.
func New(store Store, config Config) (*Service, error) {
	if store == nil {
		return nil, errors.New("access keys: store is required")
	}
	if !validPrefix(config.KeyPrefix) || config.MaxKeysPerUser < 1 || config.NameMaxLength < 1 ||
		config.DefaultTTL <= 0 || config.MaxTTL < config.DefaultTTL || len(config.Scopes) == 0 ||
		config.LastUsedInterval < 0 {
		return nil, errors.New("access keys: invalid configuration")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	if config.Async == nil {
		config.Async = func(f func()) { go f() }
	}
	config.Scopes = slices.Clone(config.Scopes)
	return &Service{store: store, config: config, uses: map[string]time.Time{}}, nil
}

// validPrefix accepts a short lowercase word: it is followed by an underscore
// and the key's ID, so it cannot contain one.
func validPrefix(prefix string) bool {
	if prefix == "" || len(prefix) > 16 {
		return false
	}
	for _, r := range prefix {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// CreateRequest is one key to issue.
type CreateRequest struct {
	OwnerID string
	Name    string
	Scopes  []string
	// TTL is the key's lifetime; zero means the Schema default.
	TTL time.Duration
}

// Created is an issued key. Key is the plaintext, shown once: it is not stored
// and cannot be read again.
type Created struct {
	Record Record
	Key    string
}

// Create issues a key for the owner.
func (s *Service) Create(ctx context.Context, request CreateRequest) (Created, error) {
	name := strings.TrimSpace(request.Name)
	if request.OwnerID == "" || name == "" || utf8.RuneCountInString(name) > s.config.NameMaxLength ||
		strings.ContainsFunc(name, isControl) {
		return Created{}, ErrInvalid
	}
	scopes, err := s.normalizeScopes(request.Scopes)
	if err != nil {
		return Created{}, err
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = s.config.DefaultTTL
	}
	if ttl < 0 || ttl > s.config.MaxTTL {
		return Created{}, ErrInvalid
	}
	id, err := s.randomWord(idBytes)
	if err != nil {
		return Created{}, fmt.Errorf("access keys: generate id: %w", err)
	}
	secret, err := s.randomWord(secretBytes)
	if err != nil {
		return Created{}, fmt.Errorf("access keys: generate secret: %w", err)
	}
	key := s.config.KeyPrefix + "_" + id + "_" + secret
	now := s.config.Clock().UTC()
	record := Record{
		ID: id, OwnerID: request.OwnerID, Name: name, Prefix: s.config.KeyPrefix + "_" + id,
		SecretHash: hashKey(key), Scopes: scopes, CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	if err := s.store.CreateAccessKey(ctx, record, s.config.MaxKeysPerUser, now); err != nil {
		return Created{}, err
	}
	return Created{Record: record, Key: key}, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// normalizeScopes checks the scopes against the Schema and drops repeats. A
// key needs at least one.
func (s *Service) normalizeScopes(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, ErrInvalid
	}
	scopes := make([]string, 0, len(requested))
	for _, scope := range requested {
		if !slices.Contains(s.config.Scopes, scope) {
			return nil, ErrInvalid
		}
		if !slices.Contains(scopes, scope) {
			scopes = append(scopes, scope)
		}
	}
	return scopes, nil
}

func (s *Service) randomWord(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(s.config.Random, buffer); err != nil {
		return "", err
	}
	return encoding.EncodeToString(buffer), nil
}

func hashKey(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}

// List returns the owner's keys, newest first, with their state. No key
// plaintext or hash is among them.
func (s *Service) List(ctx context.Context, ownerID string) ([]Record, error) {
	if ownerID == "" {
		return nil, ErrInvalid
	}
	records, err := s.store.ListAccessKeys(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	for i := range records {
		records[i].SecretHash = ""
	}
	return records, nil
}

// Revoke ends the owner's key at once. The second result is false when the key
// was already revoked: a repeat is not a change.
func (s *Service) Revoke(ctx context.Context, ownerID, id string) (Record, bool, error) {
	if ownerID == "" || id == "" {
		return Record{}, false, ErrInvalid
	}
	record, changed, err := s.store.RevokeAccessKey(ctx, ownerID, id, s.config.Clock().UTC())
	if err != nil {
		return Record{}, false, err
	}
	// Only the next last-use deadline is forgotten: every request's count was
	// already handed to the store, so a revoke loses no counted request.
	s.mu.Lock()
	delete(s.uses, id)
	s.mu.Unlock()
	record.SecretHash = ""
	return record, changed, nil
}

// Authentication is a key that authenticated.
type Authentication struct {
	KeyID   string
	OwnerID string
	Scopes  []string
}

// dummyDigest stands in for the stored digest of a key that does not exist, so
// an unknown key costs the same comparison as a wrong one.
var dummyDigest [sha256.Size]byte

// Authenticate resolves a presented key to its owner. Every refusal is
// ErrUnauthorized: the key is malformed, unknown, wrong, revoked or expired.
// Any other error is a failure to look the key up. A success adds one to the
// key's request count at once and writes its last use at most once per
// interval, both after the fact and never failing the authentication.
func (s *Service) Authenticate(ctx context.Context, presented string) (Authentication, error) {
	id, wellFormed := s.parse(presented)
	digest := sha256.Sum256([]byte(presented))
	var record Record
	found := false
	if wellFormed {
		var err error
		if record, found, err = s.store.AccessKey(ctx, id); err != nil {
			return Authentication{}, fmt.Errorf("access keys: look up key: %w", err)
		}
	}
	stored := dummyDigest[:]
	if found {
		if decoded, err := hex.DecodeString(record.SecretHash); err == nil && len(decoded) == sha256.Size {
			stored = decoded
		}
	}
	match := subtle.ConstantTimeCompare(digest[:], stored) == 1
	now := s.config.Clock().UTC()
	if !wellFormed || !found || !match || !record.Active(now) {
		return Authentication{}, ErrUnauthorized
	}
	s.touch(record.ID, now)
	return Authentication{KeyID: record.ID, OwnerID: record.OwnerID, Scopes: slices.Clone(record.Scopes)}, nil
}

// parse splits "<prefix>_<id>_<secret>" and checks its shape. It reports the
// ID; the secret is only ever compared through the hash.
func (s *Service) parse(key string) (string, bool) {
	if len(key) > 256 {
		return "", false
	}
	rest, ok := strings.CutPrefix(key, s.config.KeyPrefix+"_")
	if !ok {
		return "", false
	}
	id, secret, ok := strings.Cut(rest, "_")
	if !ok || !isWord(id, encoding.EncodedLen(idBytes)) || !isWord(secret, encoding.EncodedLen(secretBytes)) {
		return "", false
	}
	return id, true
}

func isWord(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '2' || r > '7') {
			return false
		}
	}
	return true
}

// LooksLikeKey reports whether a Bearer token has the shape of an access key
// of this deployment. It reads no store.
func (s *Service) LooksLikeKey(token string) bool {
	_, ok := s.parse(token)
	return ok
}

// touch records one authenticated request of the key. The request count goes
// to the store for every request, in one atomic increment, so a request can
// never be left waiting for a later one to be counted; the last use time is
// written at most once per interval. Both writes run after the request and
// never fail it.
func (s *Service) touch(id string, now time.Time) {
	s.mu.Lock()
	last, written := s.uses[id]
	recordLastUse := !written || now.Sub(last) >= s.config.LastUsedInterval
	if recordLastUse {
		s.uses[id] = now
	}
	s.mu.Unlock()
	s.write(func(ctx context.Context) error { return s.store.IncrementAccessKeyRequests(ctx, id) },
		"access key request not recorded", id)
	if recordLastUse {
		s.write(func(ctx context.Context) error { return s.store.RecordAccessKeyLastUse(ctx, id, now) },
			"access key last use not recorded", id)
	}
}

// write runs one use write after the request. A failure is logged without the
// key and never reaches the caller.
func (s *Service) write(record func(context.Context) error, message, id string) {
	s.config.Async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := record(ctx); err != nil {
			s.config.Logger.Warn(message, "key_id", id, "error", err)
		}
	})
}

package authentication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

// SessionRecord is independent of cookie claims, allowing one session to be
// revoked without invalidating other browsers or rotating the signing key.
type SessionRecord struct {
	ID        string
	UserID    string
	IssuedAt  time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type SessionStore interface {
	CreateSessionRecord(context.Context, SessionRecord) error
	FindSessionRecord(context.Context, string) (SessionRecord, bool, error)
	RevokeSessionRecord(context.Context, string, time.Time) error
	// RevokeUserSessions 吊销该用户全部活跃会话（SEC-06）。
	RevokeUserSessions(context.Context, string, time.Time) error
}

type sessionClaims struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	User      User   `json:"user"`
	Mock      bool   `json:"mock,omitempty"`
}

func (s *Service) sessionClaims(ctx context.Context, cookie *http.Cookie) (sessionClaims, bool, error) {
	signed := decodeCookieValue(cookie.Value)
	payload, valid := s.verify(signed)
	if !valid {
		return sessionClaims{}, false, nil
	}
	var claims sessionClaims
	if json.Unmarshal([]byte(payload), &claims) != nil {
		return claims, false, nil
	}
	now := s.clock().UTC()
	if claims.Version == 0 {
		// Legacy cookies had no creation time. A fixed deployment-independent
		// cutoff bounds dual-read compatibility, including across restarts.
		if !now.Before(s.legacyAcceptUntil) {
			return claims, false, nil
		}
		if json.Unmarshal([]byte(payload), &claims.User) != nil || !validUser(claims.User) {
			return claims, false, nil
		}
		digest := sha256.Sum256([]byte(signed))
		claims.SessionID = "legacy:" + hex.EncodeToString(digest[:])
		claims.IssuedAt = now.Unix()
		claims.ExpiresAt = s.legacyAcceptUntil.Unix()
		if err := s.sessions.CreateSessionRecord(ctx, SessionRecord{ID: claims.SessionID, UserID: claims.User.ID, IssuedAt: now, ExpiresAt: s.legacyAcceptUntil}); err != nil {
			return claims, false, err
		}
	} else if claims.Version != 2 || claims.SessionID == "" || !validUser(claims.User) || claims.IssuedAt > now.Unix() || claims.ExpiresAt <= claims.IssuedAt || now.Unix() >= claims.ExpiresAt {
		return claims, false, nil
	}
	record, exists, err := s.sessions.FindSessionRecord(ctx, claims.SessionID)
	if err != nil {
		return claims, false, err
	}
	if !exists || record.RevokedAt != nil || record.UserID != claims.User.ID || !now.Before(record.ExpiresAt) {
		return claims, false, nil
	}
	if claims.Version == 2 && (record.IssuedAt.Unix() != claims.IssuedAt || record.ExpiresAt.Unix() != claims.ExpiresAt) {
		return claims, false, nil
	}
	return claims, true, nil
}

func (s *Service) RevokeSession(ctx context.Context, request *http.Request) error {
	cookie, err := request.Cookie(s.schema.Cookie.Name)
	if errors.Is(err, http.ErrNoCookie) {
		return nil
	}
	if err != nil {
		return err
	}
	claims, valid, err := s.sessionClaims(ctx, cookie)
	if err != nil || !valid {
		return err
	}
	return s.sessions.RevokeSessionRecord(ctx, claims.SessionID, s.clock().UTC())
}

// Only development/test directories may lack durable storage. The real
// runtime repository implements SessionStore; production never uses this map.
type memorySessionStore struct {
	mu      sync.Mutex
	records map[string]SessionRecord
}

func (m *memorySessionStore) CreateSessionRecord(_ context.Context, record SessionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.records[record.ID]; !exists {
		m.records[record.ID] = record
	}
	return nil
}
func (m *memorySessionStore) FindSessionRecord(_ context.Context, id string) (SessionRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	return r, ok, nil
}
func (m *memorySessionStore) RevokeSessionRecord(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.records[id]; ok {
		r.RevokedAt = &at
		m.records[id] = r
	}
	return nil
}

func (m *memorySessionStore) RevokeUserSessions(_ context.Context, userID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, record := range m.records {
		if record.UserID == userID && record.RevokedAt == nil {
			record.RevokedAt = &at
			m.records[id] = record
		}
	}
	return nil
}

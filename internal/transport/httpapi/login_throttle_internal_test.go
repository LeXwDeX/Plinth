package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

type countingLoginFailureStore struct {
	counts   int
	recorded int
	fail     error
}

func (s *countingLoginFailureStore) CountLoginFailures(
	context.Context,
	string,
	time.Time,
) (int, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	return s.counts, nil
}

func (s *countingLoginFailureStore) RecordLoginFailure(
	context.Context,
	string,
	time.Time,
	time.Time,
) error {
	if s.fail != nil {
		return s.fail
	}
	s.recorded++
	return nil
}

func (s *countingLoginFailureStore) PruneLoginFailures(
	context.Context,
	time.Time,
) error {
	return nil
}

// TestLoginFailureLimiterUsesStoreAndFailsClosed：配了 store 就必须走持久化，
// 且存储故障时 fail-closed——反过来等于在数据库抖动时把暴力破解的门敞开。
func TestLoginFailureLimiterUsesStoreAndFailsClosed(t *testing.T) {
	t.Parallel()
	store := &countingLoginFailureStore{}
	limiter := newLoginFailureLimiter(3, time.Minute).withLoginFailureStore(store)
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	if limiter.blocked(context.Background(), "k") {
		t.Fatal("未失败过就应放行")
	}
	for attempt := 0; attempt < 3; attempt++ {
		limiter.record(context.Background(), "k")
	}
	if store.recorded != 3 {
		t.Fatalf("未走持久化记录：recorded=%d", store.recorded)
	}
	store.counts = 3
	if !limiter.blocked(context.Background(), "k") {
		t.Fatal("达到阈值后应限流")
	}
	store.fail = errors.New("database unavailable")
	if !limiter.blocked(context.Background(), "k") {
		t.Fatal("存储故障必须 fail-closed，否则等于放开暴力破解")
	}
}

var _ LoginFailureStore = (*countingLoginFailureStore)(nil)

func TestPeerAddressCanonicalizesIPv6WithoutPort(t *testing.T) {
	t.Parallel()
	for _, peer := range []string{"[::ffff:203.0.113.7]:1234", "203.0.113.7:5678", "203.0.113.7"} {
		request := httptest.NewRequest("POST", "/api/auth/login", nil)
		request.RemoteAddr = peer
		if got := loginClientKey(request, "Account"); got != "203.0.113.7\x00account" {
			t.Fatalf("peer %q key=%q", peer, got)
		}
	}
	for _, peer := range []string{"[2001:db8::a]:1234", "[2001:0DB8:0:0:0:0:0:a]:5678"} {
		request := httptest.NewRequest("POST", "/api/auth/login", nil)
		request.RemoteAddr = peer
		// IPv6 clients count per /64 (rateLimitClientKey): rotating
		// addresses inside one delegated prefix does not reset the throttle.
		if got := loginClientKey(request, "Account"); got != "2001:db8::/64\x00account" {
			t.Fatalf("peer %q key=%q", peer, got)
		}
	}
}

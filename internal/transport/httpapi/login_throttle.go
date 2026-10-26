package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// LoginFailureStore 是登录失败限流的持久化契约。此前计数只在进程内存里，
// 重启即清零——攻击者可以"撞满阈值 → 等一次重启 → 再撞"绕开限流，多副本
// 部署时每个进程更是各算各的。落库后限流随部署存活。
//
// 端口留在传输层：限流是 HTTP 入口的职责，不进 authentication 业务模块，
// 也不让 adapters 反向依赖 httpapi。
type LoginFailureStore interface {
	// CountLoginFailures 返回该键在 cutoff 之后的失败次数。
	CountLoginFailures(
		context.Context,
		string,
		time.Time,
	) (int, error)
	// RecordLoginFailure 记一次失败，并顺带清掉该键在 pruneBefore 之前的记录。
	// pruneBefore 由限流窗口决定：store 不知道窗口，若拿"本次时间戳"当清理线，
	// 会把上一条刚记的失败一起删掉。
	RecordLoginFailure(
		context.Context,
		string,
		time.Time,
		time.Time,
	) error
	// PruneLoginFailures 清理全局过期记录，防止表无限增长。
	PruneLoginFailures(context.Context, time.Time) error
}

// loginFailureLimiter 以滑动窗口统计登录失败。
//
// 配了 store 就走持久化（重启不丢）；没配则退化为进程内实现，仅供测试与
// 无数据库的装配使用——此时限流语义明确弱于生产，不得当成等价替代。
type loginFailureLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	maxEvents int
	store     LoginFailureStore
	events    map[string][]time.Time
	now       func() time.Time
}

func newLoginFailureLimiter(maxEvents int, window time.Duration) *loginFailureLimiter {
	return &loginFailureLimiter{
		window:    window,
		maxEvents: maxEvents,
		events:    map[string][]time.Time{},
		now:       time.Now,
	}
}

// withLoginFailureStore 接入持久化限流。必须在校验之前注入，否则第一次
// 记录就落在内存里，重启后照样被清零。
func (l *loginFailureLimiter) withLoginFailureStore(
	store LoginFailureStore,
) *loginFailureLimiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = store
	return l
}

// blocked 报告该键当前是否已被限流（不记录新事件）。
func (l *loginFailureLimiter) blocked(
	ctx context.Context,
	key string,
) bool {
	if l.maxEvents <= 0 || l.window <= 0 {
		return false
	}
	l.mu.Lock()
	store, now := l.store, l.now
	l.mu.Unlock()
	if store != nil {
		// 限流判断失败时必须 fail-closed（当作已限流）：宁可多挡一次登录，
		// 也不能在存储故障时把限流整个放开。
		count, err := store.CountLoginFailures(
			ctx, key, now().Add(-l.window),
		)
		if err != nil {
			return true
		}
		return count >= l.maxEvents
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	return len(l.events[key]) >= l.maxEvents
}

// record 记录一次失败尝试。
func (l *loginFailureLimiter) record(
	ctx context.Context,
	key string,
) {
	if l.maxEvents <= 0 || l.window <= 0 {
		return
	}
	l.mu.Lock()
	store, now := l.store, l.now
	l.mu.Unlock()
	if store != nil {
		// 记录失败不影响本次登录的正确性（登录本身仍会因凭证错误被拒），
		// 也不能因此让请求失败——只记日志，故障交由 blocked 的 fail-closed 兜。
		at := now()
		_ = store.RecordLoginFailure(ctx, key, at, at.Add(-l.window))
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	if len(l.events[key]) >= l.maxEvents {
		return
	}
	l.events[key] = append(l.events[key], l.now())
	if len(l.events) > 4096 {
		l.evictStaleLocked()
	}
}

func (l *loginFailureLimiter) prune(key string) {
	cutoff := l.now().Add(-l.window)
	kept := l.events[key][:0]
	for _, at := range l.events[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(l.events, key)
		return
	}
	l.events[key] = kept
}

func (l *loginFailureLimiter) evictStaleLocked() {
	for key := range l.events {
		l.prune(key)
	}
}

// loginClientKey binds failed logins to the peer IP (an IPv6 /64, see
// rateLimitClientKey) and account name. Forwarding headers count only from a
// configured trusted proxy (requestPeerAddress).
func loginClientKey(request *http.Request, username string) string {
	return rateLimitClientKey(requestPeerAddress(request)) + "\x00" + strings.ToLower(username)
}

// Password guesses in an authenticated session use an independent bucket and
// the immutable account ID. Display-name changes cannot reset that bucket.
func passwordChangeClientKey(request *http.Request, userID string) string {
	return "password-change\x00" + rateLimitClientKey(requestPeerAddress(request)) + "\x00" + userID
}

// requestPeerAddress is the client resolved by clientAddressMiddleware: the
// TCP peer, or the forwarded client when the peer is a trusted proxy (#256).
func requestPeerAddress(request *http.Request) string {
	if client, ok := request.Context().Value(clientAddressKey{}).(string); ok && client != "" {
		return client
	}
	return peerAddress(request)
}

func peerAddress(request *http.Request) string {
	host := strings.TrimSpace(request.RemoteAddr)
	if address, _, err := net.SplitHostPort(host); err == nil {
		host = address
	}
	if address, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return address.Unmap().String()
	}
	return host
}

package httpapi_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/transport/httpapi"
)

// TestSQLiteLoginFailureStoreSurvivesRestart：限流此前只在进程内存里，重启
// 即清零——攻击者可以"撞满阈值 → 等一次重启 → 再撞"绕开限流。两个独立的
// store 实例（模拟两次部署）共享同一个数据库文件时，第二个必须仍认得第一个
// 记下的失败。
func TestSQLiteLoginFailureStoreSurvivesRestart(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "login-throttle.db")
	first := openLoginFailureRepository(t, databasePath)
	second := openLoginFailureRepository(t, databasePath)
	ctx := context.Background()
	key := "198.51.100.10\x00victim"
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)

	for attempt := 0; attempt < 3; attempt++ {
		at := now.Add(time.Duration(attempt) * time.Second)
		if err := first.RecordLoginFailure(
			ctx, key, at, now.Add(-time.Minute),
		); err != nil {
			t.Fatal(err)
		}
	}
	count, err := second.CountLoginFailures(
		ctx, key, now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("重启后丢失失败记录：count=%d want=3", count)
	}
	// 窗口之外不计数：限流是滑动窗口，不是永久累积。
	inside, err := second.CountLoginFailures(
		ctx, key, now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if inside != 0 {
		t.Fatalf("窗口外仍在计数：count=%d", inside)
	}
	// 另一个键不受牵连。
	other, err := second.CountLoginFailures(
		ctx, "198.51.100.10\x00other", now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if other != 0 {
		t.Fatalf("限流键串了：count=%d", other)
	}
}

// TestSQLiteLoginFailureStorePrunesExpiredRows：记录时顺带清掉过期行，否则
// 该表会随尝试次数线性增长，而限流只需要窗口内的计数。
func TestSQLiteLoginFailureStorePrunesExpiredRows(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "login-throttle.db")
	repository := openLoginFailureRepository(t, databasePath)
	ctx := context.Background()
	key := "203.0.113.5\x00victim"
	base := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)

	if err := repository.RecordLoginFailure(
		ctx, key, base.Add(-2*time.Hour), base.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordLoginFailure(
		ctx, key, base, base.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	// 截止点取在窗口内那条记录之前：它必须还在，两小时前那条必须已被清掉。
	count, err := repository.CountLoginFailures(
		ctx, key, base.Add(-time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("过期记录未被清理：count=%d want=1", count)
	}
	// 全局清理同样可用。
	if err := repository.PruneLoginFailures(ctx, base); err != nil {
		t.Fatal(err)
	}
}

func openLoginFailureRepository(
	t *testing.T,
	databasePath string,
) *sqlite.Repository {
	t.Helper()
	repository, err := httpapi.OpenMigratedTestRepository(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}

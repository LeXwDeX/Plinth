package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// W4 §4.2：application.runtime.coordinator.dispatch_checkpoint_max_bytes
// 声明与校验；该值是服务端内部调度参数，禁止进入公开投影（§8.1）。

func TestDispatchCheckpointMaxBytesDeclaredWithDefault(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	coordinator := taskCatalog.Application().Runtime.Coordinator
	if coordinator.DispatchCheckpointMaxBytes != 65536 {
		t.Fatalf(
			"dispatch_checkpoint_max_bytes = %d; want 65536",
			coordinator.DispatchCheckpointMaxBytes,
		)
	}
}

func TestDispatchCheckpointMaxBytesValidation(t *testing.T) {
	t.Run("负值拒绝", func(t *testing.T) {
		document := defaultCatalogDocument(t)
		runtime := applicationRuntimeOf(t, document)
		runtime["dispatch_checkpoint_max_bytes"] = -1
		if _, err := loadDocument(t, document); err == nil {
			t.Fatal("负值必须被加载期校验拒绝")
		} else if !strings.Contains(err.Error(), "dispatch_checkpoint_max_bytes") {
			t.Fatalf("err = %v; want 指向 dispatch_checkpoint_max_bytes", err)
		}
	})
	t.Run("缺省合法（运行时按默认处理）", func(t *testing.T) {
		document := defaultCatalogDocument(t)
		runtime := applicationRuntimeOf(t, document)
		delete(runtime, "dispatch_checkpoint_max_bytes")
		loaded, err := loadDocument(t, document)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Application().Runtime.Coordinator.DispatchCheckpointMaxBytes != 0 {
			t.Fatal("缺省时字段应为 0（由运行时按默认值处理）")
		}
	})
	t.Run("显式正值合法并可达服务端消费者", func(t *testing.T) {
		document := defaultCatalogDocument(t)
		runtime := applicationRuntimeOf(t, document)
		runtime["dispatch_checkpoint_max_bytes"] = 2048
		loaded, err := loadDocument(t, document)
		if err != nil {
			t.Fatal(err)
		}
		if got := loaded.Application().Runtime.Coordinator.DispatchCheckpointMaxBytes; got != 2048 {
			t.Fatalf("dispatch_checkpoint_max_bytes = %d; want 2048", got)
		}
	})
}

// TestDispatchCheckpointMaxBytesNeverReachesPublicProjection 负面哨兵：
// Public() 序列化不得出现检查点上限（runtime.coordinator 内部）。
func TestDispatchCheckpointMaxBytesNeverReachesPublicProjection(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(taskCatalog.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	public := string(encoded)
	if strings.Contains(public, "dispatch_checkpoint_max_bytes") {
		t.Fatalf("Public() 泄漏服务端内部调度参数: %s", public[:min(len(public), 400)])
	}
	if strings.Contains(public, `"coordinator"`) ||
		strings.Contains(public, `"poll_interval_milliseconds"`) {
		t.Fatal("Public() 不应暴露服务端调度策略")
	}
}

func applicationRuntimeOf(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	runtime := applicationOf(t, document)["runtime"].(map[string]any)
	coordinator, ok := runtime["coordinator"].(map[string]any)
	if !ok {
		t.Fatal("默认目录缺少 application.runtime.coordinator")
	}
	return coordinator
}

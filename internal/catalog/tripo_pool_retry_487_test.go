package catalog_test

// tripo_pool_retry_487_test.go 锁定 #487 的 Catalog 声明：并发池按 task
// category 声明（rate-limits 页）、Offering 声明自己走哪个池、可重试的 429 与
// 5xx 按官方 1s 起倍增至 32s 最多 5 次，以及任务级 error_code 的独立词表。
//
// 官方依据（读取日期 2026-11-23）：
//   - https://developers.tripo3d.ai/en/docs/rate-limits（并发按 account 分池，
//     3D Generation H 10 / P 5、Image 1、Animation 10、Model Processing 5、
//     Mesh Operations 10；退避 1s 起倍增至 32s）
//   - https://developers.tripo3d.ai/en/docs/error-handling（Retry-After /
//     X-RateLimit-Reset；最多 5 次；请求级 code 与任务级 error_code 是两套
//     语义，同一个数字可能含义不同）

import (
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

func TestTripoConnectionDeclaresEveryOfficialConcurrencyPool487(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var connection catalog.ProviderConnectionSchema
	for _, c := range product.Application().Runtime.ProviderConnections {
		if c.AdapterKey == "tripo.v3" {
			connection = c
		}
	}
	if connection.Concurrency == nil {
		t.Fatal("tripo connection declares no concurrency")
	}
	for name, want := range map[string]int{
		"generation-h":     10,
		"generation-p":     5,
		"image-generation": 1,
		"animation":        10,
		"model-processing": 5,
		"mesh-operations":  10,
	} {
		pool, declared := connection.Concurrency.Pools[name]
		if !declared {
			t.Errorf("pool %q is missing: %+v", name, connection.Concurrency.Pools)
			continue
		}
		if pool.Default != want {
			t.Errorf("pool %q limit = %d, want the official %d", name, pool.Default, want)
		}
	}
	if len(connection.Concurrency.Pools) != 6 {
		t.Errorf("pools = %+v, want exactly the six official categories", connection.Concurrency.Pools)
	}
}

// #487: every Tripo Offering says which category pool its dispatches draw from,
// and that pool is registered. A missing or misspelled declaration would queue
// nothing at all.
func TestEveryTripoOfferingDeclaresARegisteredPool487(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, c := range product.Application().Runtime.ProviderConnections {
		if c.AdapterKey == "tripo.v3" && c.Concurrency != nil {
			for name := range c.Concurrency.Pools {
				registered[name] = true
			}
		}
	}
	count := 0
	for _, offering := range product.List("") {
		if offering.AdapterKey != "tripo.v3" {
			continue
		}
		count++
		pool, err := tripo.OfferingPool(offering)
		if err != nil {
			t.Errorf("%s provider_options.pool: %v", offering.ID, err)
			continue
		}
		if pool == "" {
			t.Errorf("%s declares no upstream pool", offering.ID)
			continue
		}
		if !registered[pool] {
			t.Errorf("%s declares unregistered pool %q", offering.ID, pool)
		}
	}
	if count == 0 {
		t.Fatal("no tripo offering found")
	}
}

// #487: the generation and processing offerings retry the retryable classes with
// the official 1s..32s backoff and at most five attempts.
func TestTripoOfferingsRetryAtOfficialBackoff487(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, offering := range product.List("") {
		if offering.AdapterKey != "tripo.v3" {
			continue
		}
		count++
		if offering.Retry.MaxAttempts > 5 {
			t.Errorf("%s max_attempts = %d, want at most 5", offering.ID, offering.Retry.MaxAttempts)
		}
		if offering.Retry.MaxAttempts < 2 {
			t.Errorf("%s max_attempts = %d: a 429 or 5xx must be retryable", offering.ID, offering.Retry.MaxAttempts)
		}
		if offering.Retry.InitialBackoff != time.Second {
			t.Errorf("%s initial backoff = %v, want the official 1s", offering.ID, offering.Retry.InitialBackoff)
		}
		if offering.Retry.MaxBackoff != 32*time.Second {
			t.Errorf("%s max backoff = %v, want the official 32s cap", offering.ID, offering.Retry.MaxBackoff)
		}
		allowed := map[generation.FailureClass]bool{}
		for _, class := range offering.Retry.Retryable {
			allowed[generation.FailureClass(class)] = true
		}
		if !allowed[generation.FailureRateLimited] || !allowed[generation.FailureUpstreamUnavailable] {
			t.Errorf("%s retryable = %v, want rate_limited and upstream_unavailable", offering.ID, offering.Retry.Retryable)
		}
	}
	if count == 0 {
		t.Fatal("no tripo offering found")
	}
}

// #487: the task-level error_code vocabulary is declared separately from the
// request-level one, and only the transient codes are retryable.
func TestTripoConnectionDeclaresATaskLevelCodeTable487(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range product.Application().Runtime.ProviderConnections {
		if c.AdapterKey != "tripo.v3" {
			continue
		}
		if c.ErrorMapping.RateLimitResetHeader != "X-RateLimit-Reset" {
			t.Errorf("rate_limit_reset_header = %q, want X-RateLimit-Reset", c.ErrorMapping.RateLimitResetHeader)
		}
		for code := range map[string]bool{"1000": true, "1001": true} {
			declared, ok := c.ErrorMapping.TaskErrorCodeMap[code]
			if !ok || !declared.Retryable {
				t.Errorf("task error code %q = %+v, want it declared retryable", code, declared)
			}
		}
		// 2018 is only a request-level code ("model too complex to remesh");
		// as a task-level code the official pages publish no meaning, so it must
		// not be declared there.
		if _, declared := c.ErrorMapping.TaskErrorCodeMap["2018"]; declared {
			t.Error("2018 must stay request-level only")
		}
		return
	}
	t.Fatal("the tripo connection is missing")
}

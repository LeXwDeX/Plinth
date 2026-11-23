package tripo

// pool_enabled_478_test.go 锁定 #478 P1-11：池启用判定必须看连接声明是否真的
// 注册了这个池（limit > 0），而不是只看 manager 与 poolID 非空。未注册或
// limit=0 的池不是「启用」，不得把子作业串行化成并发 1。

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/upstreampool"
)

func TestPoolEnabledReflectsRegistration478(t *testing.T) {
	offering := generation.Offering{AdapterKey: "tripo.v3"}

	// 未注册的池：manager 与 poolID 非空但连接没声明 → 不启用。
	unregistered := &Adapter{pool: upstreampool.New(upstreampool.Options{}), poolID: "model-processing"}
	if unregistered.poolEnabled(offering) {
		t.Fatal("未注册的池不应视为启用")
	}

	// 注册且 limit > 0 → 启用。
	registered := &Adapter{pool: upstreampool.New(upstreampool.Options{}), poolID: "model-processing"}
	registered.pool.Register("tripo.v3", "model-processing", upstreampool.PoolConfig{Limit: 5})
	if !registered.poolEnabled(offering) {
		t.Fatal("已注册且 limit>0 的池应视为启用")
	}

	// limit=0（显式禁用）→ 不启用。
	disabled := &Adapter{pool: upstreampool.New(upstreampool.Options{}), poolID: "zero-pool"}
	disabled.pool.Register("tripo.v3", "zero-pool", upstreampool.PoolConfig{Limit: 0})
	if disabled.poolEnabled(offering) {
		t.Fatal("limit=0 的池不应视为启用")
	}

	// conversionConcurrency 随池启用与否切换：启用时每 Task 只 Keep 一个在途
	// 子作业；未启用时沿用声明并发。
	options := providerOptions{FormatConversion: &formatConversionOptions{ConcurrencyLimit: 3}}
	if got := registered.conversionConcurrency(offering, options); got != 1 {
		t.Fatalf("启用池时 conversionConcurrency = %d，want 1", got)
	}
	if got := unregistered.conversionConcurrency(offering, options); got != 3 {
		t.Fatalf("未启用池时 conversionConcurrency = %d，want 声明的 3", got)
	}
}
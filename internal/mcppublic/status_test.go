package mcppublic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

type fakeSettings struct {
	settings accountsettings.Settings
	err      error
	reads    int
}

func (f *fakeSettings) Get(context.Context, string) (accountsettings.Settings, error) {
	f.reads++
	return f.settings, f.err
}

type fakeUsage struct {
	mu       sync.Mutex
	total    int
	asset    map[string]int
	totalErr error
	assetErr error
	extra    map[string]int
	extraErr error
	titles   map[string]string
	titleErr error
}

func (f *fakeUsage) AssetStepsUsed(_ context.Context, _, session string, _ []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asset[session], f.assetErr
}

func (f *fakeUsage) AssetExtraSteps(_ context.Context, _, session string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.extra[session], f.extraErr
}

func (f *fakeUsage) AssetTitle(_ context.Context, _, session string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	title, found := f.titles[session]
	return title, found, f.titleErr
}

func (f *fakeUsage) AccessKeyStepsTotal(context.Context, string, []string) (int, error) {
	return f.total, f.totalErr
}

func (f *fakeUsage) AccessKeyAssets(context.Context, string, []string, int) ([]executiongrant.AccessKeyAsset, bool, error) {
	return nil, false, nil
}

type fakeBalances struct {
	readings map[string]provideraccounts.Reading
}

func (f fakeBalances) Read(_ context.Context, ids []string) []provideraccounts.Reading {
	result := make([]provideraccounts.Reading, len(ids))
	for index, id := range ids {
		if reading, found := f.readings[id]; found {
			result[index] = reading
			continue
		}
		result[index] = provideraccounts.Reading{AccountID: id}
	}
	return result
}

type fakeInspections struct {
	inspection *mcppublic.Inspection
	found      bool
	err        error
	calls      int
}

func (f *fakeInspections) Inspection(context.Context, string, string) (*mcppublic.Inspection, bool, error) {
	f.calls++
	return f.inspection, f.found, f.err
}

var readTime = time.Date(2026, 11, 17, 8, 30, 0, 0, time.UTC)

func known(id, amount, unit, label string) provideraccounts.Reading {
	return provideraccounts.Reading{AccountID: id, Known: true, ReadAt: readTime,
		Native: &schema.NativeAmount{Unit: unit, UnitLabel: label, Amount: amount}}
}

type harness struct {
	// withoutAccount names a provider account the deployment does not declare.
	withoutAccount string
	builder        *mcppublic.Builder
	settings       *fakeSettings
	usage          *fakeUsage
	inspections    *fakeInspections
	logs           *bytes.Buffer
	mcp            *catalog.MCPPublicSchema
}

func newHarness(t *testing.T, mutate func(*harness)) *harness {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	h := &harness{
		settings: &fakeSettings{settings: accountsettings.Settings{MCP: approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeAgent, Cap: 20}}},
		usage:    &fakeUsage{total: 7, asset: map[string]int{"asset-1": 3}, titles: map[string]string{"asset-1": "MCP · 木桶"}},
		logs:     &bytes.Buffer{},
		mcp:      application.MCPPublic,
	}
	balances := fakeBalances{readings: map[string]provideraccounts.Reading{
		"meshy": known("meshy", "1000", "meshy.credit", "Meshy credits"),
		"tripo": known("tripo", "50.5", "tripo.credit", "Tripo credits"),
		// hyper3d has no reading: it could not be read.
	}}
	if mutate != nil {
		mutate(h)
	}
	var inspections mcppublic.InspectionReader
	if h.inspections != nil {
		inspections = h.inspections
	}
	h.builder, err = mcppublic.New(mcppublic.Config{
		StatusBlock: h.mcp.StatusBlock, StepPolicy: *application.StepPolicy, ProviderAccounts: h.accounts(application),
		AccountLabels: map[string]string{"meshy": "MeshyAI", "tripo": "Tripo", "hyper3d": "Hyper3D"},
		Settings:      h.settings, Usage: h.usage, Balances: balances, Inspections: inspections,
		Localize: func(locale, text string) string {
			if locale == "en" && text == "Agent 自己管理" {
				return "Managed by the Agent"
			}
			return text
		},
		Logger: slog.New(slog.NewTextHandler(h.logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// declaredAccounts is what the harness declares as the deployment's provider
// accounts; a test can drop one.
func (h *harness) accounts(application catalog.ApplicationSchema) map[string]catalog.ProviderAccountSchema {
	accounts := map[string]catalog.ProviderAccountSchema{}
	for id, account := range application.Runtime.ProviderAccounts {
		// "*" is a deployment that declares no provider account at all.
		if id != h.withoutAccount && h.withoutAccount != "*" {
			accounts[id] = account
		}
	}
	return accounts
}

// conforms checks the block against the JSON Schema the Catalog declares for
// it: the code's vocabulary and the declared contract cannot drift apart.
func (h *harness) conforms(t *testing.T, block mcppublic.Block) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	declared, err := toolschema.Parse(h.mcp.StatusBlock.Schema, toolschema.Options{RootObject: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := declared.Validate(encoded); err != nil {
		t.Fatalf("the block breaks its declared schema: %v\n%s", err, encoded)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestBlockInTheDefaultAgentModeShowsUsedStepsAndBalances(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	// The asset's title is read from the owner's own session list.
	block := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a", AssetID: "asset-1"})
	decoded := h.conforms(t, block)
	if block.StepMode != "agent" || block.StepModeLabel != "Agent 自己管理" || block.StepCap != nil {
		t.Fatalf("mode = %q %q cap=%v", block.StepMode, block.StepModeLabel, block.StepCap)
	}
	if block.TotalUsedSteps == nil || *block.TotalUsedSteps != 7 {
		t.Fatalf("total = %v", block.TotalUsedSteps)
	}
	if block.Asset == nil || block.Asset.ID != "asset-1" || block.Asset.UsedSteps == nil || *block.Asset.UsedSteps != 3 ||
		block.Asset.StepsLeft != nil || block.Asset.Title == nil || *block.Asset.Title != "MCP · 木桶" {
		t.Fatalf("asset = %+v", block.Asset)
	}
	if len(block.Balances) != 3 {
		t.Fatalf("balances = %+v", block.Balances)
	}
	meshy, tripo, hyper := block.Balances[0], block.Balances[1], block.Balances[2]
	if meshy.Provider != "meshy" || meshy.Label != "MeshyAI" || meshy.State != "ok" || *meshy.Amount != "1000" ||
		*meshy.Unit != "meshy.credit" || *meshy.UnitLabel != "Meshy credits" || *meshy.ReadAt != "2026-11-17T08:30:00Z" {
		t.Fatalf("meshy = %+v", meshy)
	}
	if tripo.Provider != "tripo" || *tripo.Amount != "50.5" {
		t.Fatalf("tripo = %+v", tripo)
	}
	// A balance that could not be read is unknown: no amount, never a zero.
	if hyper.State != "unknown" || hyper.Amount != nil || hyper.Unit != nil || hyper.ReadAt != nil {
		t.Fatalf("hyper3d = %+v", hyper)
	}
	if decoded["failure"] != nil || len(decoded["step_costs"].([]any)) != 0 {
		t.Fatalf("a block without a failure or costs = %v", decoded)
	}
}

func TestBlockUnderTheCapModeCountsTheStepsLeft(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		used, cap, left int
	}{"under the cap": {3, 5, 2}, "at the cap": {5, 5, 0}, "over the cap": {9, 5, 0}} {
		h := newHarness(t, func(h *harness) {
			h.settings.settings.MCP = approvalpolicy.StepSetting{Mode: approvalpolicy.StepModeCap, Cap: test.cap}
			h.usage.asset["asset-1"] = test.used
		})
		block := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a", AssetID: "asset-1"})
		h.conforms(t, block)
		if block.StepMode != "cap" || block.StepCap == nil || *block.StepCap != test.cap ||
			block.Asset.StepsLeft == nil || *block.Asset.StepsLeft != test.left || *block.Asset.UsedSteps != test.used {
			t.Errorf("%s: block = %+v asset = %+v", name, block, block.Asset)
		}
	}
}

func TestBlockWithoutAnAssetOrWithUnreadableStepsNeverFailsAndNeverInventsAZero(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) {
		h.usage.totalErr = errors.New("database is locked")
		h.usage.assetErr = errors.New("database is locked")
	})
	none := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a"})
	h.conforms(t, none)
	if none.Asset != nil || none.TotalUsedSteps != nil {
		t.Fatalf("no asset, unreadable total: asset=%+v total=%v", none.Asset, none.TotalUsedSteps)
	}
	with := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a", AssetID: "asset-1"})
	h.conforms(t, with)
	if with.Asset == nil || with.Asset.UsedSteps != nil || with.Asset.StepsLeft != nil || with.TotalUsedSteps != nil {
		t.Fatalf("unreadable steps = asset %+v total %v; they must be null, not zero", with.Asset, with.TotalUsedSteps)
	}
	if !strings.Contains(h.logs.String(), "database is locked") {
		t.Fatal("the failed reads were not logged")
	}
	line := h.builder.Summary("", with)
	if !strings.Contains(line, "本资产已用 未知 步") || !strings.Contains(line, "累计 未知 步") {
		t.Fatalf("summary = %q; unknown numbers must say unknown", line)
	}
}

func TestBlockFallsBackToTheSchemaDefaultWhenTheSettingCannotBeRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.settings.err = errors.New("settings are down") })
	block := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a", AssetID: "asset-1"})
	h.conforms(t, block)
	if block.StepMode != "agent" || block.StepCap != nil {
		t.Fatalf("mode = %q cap=%v; want the Schema default", block.StepMode, block.StepCap)
	}
}

func TestBlockCarriesCostsAndTheFailureAttribution(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	block := h.builder.Build(context.Background(), "", mcppublic.Subject{
		OwnerID: "owner-a", AssetID: "asset-1",
		Costs: []mcppublic.StepCost{{OfferingID: "meshy/meshy-7.1/image-to-3d", Provider: "meshy", Capability: "image-to-3d", Amount: "30", Unit: "meshy.credit", UnitLabel: "Meshy credits"}},
		Failure: &mcppublic.Failure{TaskID: "task-9", Category: "upstream_capacity", NextAction: "retry_later",
			Retryable: true, UserDecisionRequired: false, Advice: "Wait for the provider and retry later."},
	})
	decoded := h.conforms(t, block)
	costs := decoded["step_costs"].([]any)
	failure := decoded["failure"].(map[string]any)
	if len(costs) != 1 || costs[0].(map[string]any)["amount"] != "30" || failure["task_id"] != "task-9" ||
		failure["category"] != "upstream_capacity" || failure["next_action"] != "retry_later" || failure["retryable"] != true {
		t.Fatalf("costs = %v failure = %v", costs, failure)
	}
}

func TestSummaryIsOneShortLineInTheRequestLanguage(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	with := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a", AssetID: "asset-1"})
	line := h.builder.Summary("", with)
	for _, want := range []string{"Agent 自己管理", "本资产已用 3 步", "累计 7 步", "MeshyAI 1000 Meshy credits", "Tripo 50.5 Tripo credits", "Hyper3D 未知"} {
		if !strings.Contains(line, want) {
			t.Errorf("summary %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "\n") || strings.Contains(line, "{") {
		t.Fatalf("summary %q is not one finished line", line)
	}
	without := h.builder.Summary("", h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a"}))
	if strings.Contains(without, "本资产") || !strings.Contains(without, "累计 7 步") {
		t.Fatalf("summary without an asset = %q", without)
	}
	if english := h.builder.Summary("en", h.builder.Build(context.Background(), "en", mcppublic.Subject{OwnerID: "owner-a"})); !strings.Contains(english, "Managed by the Agent") {
		t.Fatalf("english summary = %q", english)
	}
}

func TestBuilderNeedsItsParts(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	good := mcppublic.Config{
		StatusBlock: application.MCPPublic.StatusBlock, StepPolicy: *application.StepPolicy, ProviderAccounts: application.Runtime.ProviderAccounts,
		Settings: &fakeSettings{}, Usage: &fakeUsage{}, Balances: fakeBalances{},
	}
	if _, err := mcppublic.New(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*mcppublic.Config){
		"no settings": func(c *mcppublic.Config) { c.Settings = nil },
		"no usage":    func(c *mcppublic.Config) { c.Usage = nil },
		"no balances": func(c *mcppublic.Config) { c.Balances = nil },
	} {
		config := good
		mutate(&config)
		if _, err := mcppublic.New(config); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A provider account the deployment does not declare is left out of the block:
// the Schema lists the 3D providers, and a Catalog may leave one out.
func TestBlockSkipsABalanceAccountTheDeploymentDoesNotDeclare(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(h *harness) { h.withoutAccount = "hyper3d" })
	block := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a"})
	h.conforms(t, block)
	if len(block.Balances) != 2 || block.Balances[0].Provider != "meshy" || block.Balances[1].Provider != "tripo" {
		t.Fatalf("balances = %+v", block.Balances)
	}
	if line := h.builder.Summary("", block); strings.Contains(line, "Hyper3D") {
		t.Fatalf("summary = %q", line)
	}
	none := newHarness(t, func(h *harness) { h.withoutAccount = "*" })
	empty := none.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a"})
	none.conforms(t, empty)
	if len(empty.Balances) != 0 {
		t.Fatalf("balances = %+v", empty.Balances)
	}
}

func TestBlockReportsTheSubjectTaskInspection(t *testing.T) {
	t.Parallel()
	when := "2026-11-17T08:30:00Z"
	reader := &fakeInspections{found: true, inspection: &mcppublic.Inspection{
		TaskID: "task-1", Source: mcppublic.InspectionSourcePlatformSelfCheck, Verdict: "passed",
		Purpose:   &mcppublic.InspectionPurpose{ID: "display", Label: "展示/渲染", Source: "default_profile"},
		Waiver:    true,
		CheckedAt: &when,
	}}
	h := newHarness(t, func(h *harness) { h.inspections = reader })
	block := h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a", TaskID: "task-1"})
	decoded := h.conforms(t, block)
	inspection, ok := decoded["inspection"].(map[string]any)
	if !ok || inspection["task_id"] != "task-1" || inspection["source"] != "platform_self_check" ||
		inspection["verdict"] != "passed" || inspection["waiver"] != true || inspection["checked_at"] != when {
		t.Fatalf("inspection = %v", decoded["inspection"])
	}
	// failed is an array even when the reader left it nil.
	if failed, ok := inspection["failed"].([]any); !ok || len(failed) != 0 {
		t.Fatalf("failed = %v", inspection["failed"])
	}
	purpose := inspection["purpose"].(map[string]any)
	if purpose["id"] != "display" || purpose["label"] != "展示/渲染" || purpose["source"] != "default_profile" {
		t.Fatalf("purpose = %v", purpose)
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls = %d", reader.calls)
	}

	// No task id (the other tools) never reads.
	reader.calls = 0
	decoded = h.conforms(t, h.builder.Build(context.Background(), "", mcppublic.Subject{OwnerID: "owner-a"}))
	if reader.calls != 0 || decoded["inspection"] != nil {
		t.Fatalf("a subject without a task: calls=%d inspection=%v", reader.calls, decoded["inspection"])
	}
}

func TestBlockInspectionIsNullWhenUnreadableOrMissing(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		reader *fakeInspections
		warn   string
	}{
		"not found": {reader: &fakeInspections{}},
		"read failed": {
			reader: &fakeInspections{err: errors.New("self-check store is down")},
			warn:   "自检结论读取失败",
		},
		"no reader": {},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, func(h *harness) { h.inspections = test.reader })
			decoded := h.conforms(t, h.builder.Build(context.Background(), "",
				mcppublic.Subject{OwnerID: "owner-a", TaskID: "task-1"}))
			if decoded["inspection"] != nil {
				t.Fatalf("inspection = %v", decoded["inspection"])
			}
			if test.warn != "" {
				if !strings.Contains(h.logs.String(), test.warn) {
					t.Fatalf("logs = %q", h.logs.String())
				}
			} else if strings.Contains(h.logs.String(), "自检") {
				// A missing record is normal and must not raise a warning.
				t.Fatalf("a missing record was logged: %q", h.logs.String())
			}
		})
	}
}

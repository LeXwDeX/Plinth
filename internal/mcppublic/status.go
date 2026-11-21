// Package mcppublic builds what the external MCP service (#332) tells its
// caller besides the result of a tool: the status block of every response.
//
// The external caller is an Agent that is not part of the platform, and in the
// default step mode it decides for itself whether to go on with another 3D
// step. The platform therefore hands it the facts that decision needs, in one
// block under one fixed key of structuredContent, plus one short line of text:
// the step mode, the 3D steps the asset and the person have used through the
// service, the balances of the 3D providers, the approximate cost of a step,
// and, for a failed task, why it failed and what to do.
//
// Reading the block counts no step and charges nothing. It never fails the
// call: a number that cannot be read is reported as unknown (null), never as
// zero. Which balance accounts, which words and which property names it uses
// come from the Product Schema (application.mcp_public.status_block).
//
// The package also keeps the external service's asset names (assets.go): the
// mapping from a caller's name to one of the person's chat sessions, which is
// the asset every step count in the block is about.
package mcppublic

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
)

// Balance states of one provider in the block.
const (
	BalanceOK      = "ok"
	BalanceUnknown = "unknown"
)

// Block is the status block. The JSON names are the vocabulary the Schema's
// status block declares (catalog.MCPStatus*); a test keeps the two in step.
type Block struct {
	StepMode       string       `json:"step_mode"`
	StepModeLabel  string       `json:"step_mode_label"`
	StepCap        *int         `json:"step_cap"`
	Asset          *Asset       `json:"asset"`
	TotalUsedSteps *int         `json:"total_used_steps"`
	Balances       []Balance    `json:"balances"`
	StepCosts      []StepCost   `json:"step_costs"`
	Failure        *Failure     `json:"failure"`
	Inspection     *Inspection  `json:"inspection"`
	summaryParts   summaryFacts `json:"-"`
}

// Asset is the asset (one chat session) the call concerned.
type Asset struct {
	ID        string  `json:"id"`
	Title     *string `json:"title"`
	UsedSteps *int    `json:"used_steps"`
	StepsLeft *int    `json:"steps_left"`
}

// Balance is one 3D provider's balance, in the provider's own unit. A balance
// that could not be read has State unknown and no amount.
type Balance struct {
	Provider  string  `json:"provider"`
	Label     string  `json:"label"`
	State     string  `json:"state"`
	Amount    *string `json:"amount"`
	Unit      *string `json:"unit"`
	UnitLabel *string `json:"unit_label"`
	ReadAt    *string `json:"read_at"`
}

// StepCost is what one step of a service costs, roughly, in its provider's
// own unit.
type StepCost struct {
	OfferingID string `json:"offering_id"`
	Provider   string `json:"provider"`
	Capability string `json:"capability"`
	Amount     string `json:"amount"`
	Unit       string `json:"unit"`
	UnitLabel  string `json:"unit_label"`
}

// Failure is the attribution of a failed task and what to do about it.
type Failure struct {
	TaskID               string `json:"task_id"`
	Category             string `json:"category"`
	NextAction           string `json:"next_action"`
	Retryable            bool   `json:"retryable"`
	UserDecisionRequired bool   `json:"user_decision_required"`
	Advice               string `json:"advice"`
}

// InspectionSourcePlatformSelfCheck is the only source the block reports: the
// platform's own 3D self-check record. An external inspection (A2) would add
// another source.
const InspectionSourcePlatformSelfCheck = "platform_self_check"

// Inspection is the platform's last self-check of a finished 3D Task. It is
// evidence for the caller, never a delivery approval: a passed verdict may
// carry a waiver, and a task without a record has no inspection (null). The
// platform's delivery-state roles are passed, issues, unchecked and
// uncheckable; the values are not invented here.
type Inspection struct {
	TaskID    string                  `json:"task_id"`
	Source    string                  `json:"source"`
	Verdict   string                  `json:"verdict"`
	Purpose   *InspectionPurpose      `json:"purpose"`
	Failed    []InspectionFailedCheck `json:"failed"`
	Waiver    bool                    `json:"waiver"`
	CheckedAt *string                 `json:"checked_at"`
}

// InspectionPurpose names the purpose the inspection judged the model against;
// nil when no inspection recorded one.
type InspectionPurpose struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Source string `json:"source"`
}

// InspectionFailedCheck names one purpose check the latest successful
// inspection judged as failed. It is a check id, never free text.
type InspectionFailedCheck struct {
	Check string `json:"check"`
}

// InspectionReader reads the platform's last self-check of one task. The
// returned Inspection is nil with found false when the platform recorded none.
// The block never fails a call: an error is logged and shown as null.
type InspectionReader interface {
	Inspection(ctx context.Context, ownerID, taskID string) (*Inspection, bool, error)
}

type summaryFacts struct {
	hasAsset   bool
	assetSteps *int
}

// Settings reads the person's 3D step settings.
type Settings interface {
	Get(ctx context.Context, ownerID string) (accountsettings.Settings, error)
}

// Balances reads the provider balances through the short shared cache.
type Balances interface {
	Read(ctx context.Context, accountIDs []string) []provideraccounts.Reading
}

// Config assembles a Builder. Everything that is copy or a name comes from the
// Schema sections it is given.
type Config struct {
	StatusBlock catalog.MCPPublicStatusBlock
	StepPolicy  catalog.StepPolicySchema
	// ProviderAccounts are the accounts the deployment declares
	// (runtime.provider_accounts). The block shows the balance accounts of the
	// Schema that are declared here, in the Schema's order.
	ProviderAccounts map[string]catalog.ProviderAccountSchema
	// AccountLabels names the balance accounts (account ID to label); an
	// account without a label is shown by its ID.
	AccountLabels map[string]string
	Settings      Settings
	Usage         executiongrant.AccessKeyUsageStore
	Balances      Balances
	// Inspections reads the platform self-check of a succeeded 3D task; nil
	// keeps every block's inspection null.
	Inspections InspectionReader
	// Localize turns Schema copy into the request's language; nil keeps it.
	Localize func(locale, text string) string
	Logger   *slog.Logger
}

// Builder builds the status block.
type Builder struct {
	config   Config
	counted  []string
	modes    map[string]string
	accounts []string
}

// New builds the Builder. The settings, the usage reader and the balance
// reader are required.
func New(config Config) (*Builder, error) {
	if config.Settings == nil || config.Usage == nil || config.Balances == nil || len(config.StepPolicy.CountedCapabilities) == 0 {
		return nil, ErrInvalidConfig
	}
	if config.Localize == nil {
		config.Localize = func(_, text string) string { return text }
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	builder := &Builder{config: config, modes: map[string]string{}}
	for _, capability := range config.StepPolicy.CountedCapabilities {
		builder.counted = append(builder.counted, string(capability))
	}
	for _, account := range config.StatusBlock.BalanceAccounts {
		if _, declared := config.ProviderAccounts[account]; declared {
			builder.accounts = append(builder.accounts, account)
		}
	}
	for _, mode := range config.StepPolicy.Modes {
		builder.modes[mode.Value] = mode.Label
	}
	return builder, nil
}

// ErrInvalidConfig means the builder was assembled without a part it needs.
var ErrInvalidConfig = errors.New("mcppublic: invalid status block configuration")

// Key is the fixed key of structuredContent that holds the block.
func (b *Builder) Key() string { return b.config.StatusBlock.Key }

// Counted lists the capabilities whose Tasks are 3D steps.
func (b *Builder) Counted() []string { return append([]string(nil), b.counted...) }

// Subject says what a call concerned.
type Subject struct {
	OwnerID string
	// AssetID is the asset (chat session) the call concerned; empty when it
	// concerned none (a listing of services, the person's recent tasks).
	AssetID    string
	AssetTitle string
	// Costs are the services whose per-step cost to show; the caller derives
	// them where the cost is cheap to know.
	Costs []StepCost
	// Failure is the attribution of the task the call concerned, if it failed.
	Failure *Failure
	// TaskID is the succeeded task whose platform self-check the block reports.
	// Only get_task and get_task_assets set it; a task without a record keeps
	// the inspection null.
	TaskID string
}

// Build reads the facts of the subject. It never fails: what cannot be read is
// null (a number) or unknown (a balance), and the cause is logged.
func (b *Builder) Build(ctx context.Context, locale string, subject Subject) Block {
	block := Block{Balances: []Balance{}, StepCosts: []StepCost{}, Failure: subject.Failure}
	mode, capValue := b.config.StepPolicy.DefaultMode.MCP, b.config.StepPolicy.Cap.Default
	if settings, err := b.config.Settings.Get(ctx, subject.OwnerID); err != nil {
		b.config.Logger.Warn("MCP 状态块：步数设置读取失败，按 Schema 默认值给出", "owner_id", subject.OwnerID, "error", err)
	} else {
		mode, capValue = settings.MCP.Mode, settings.MCP.Cap
	}
	block.StepMode = mode
	block.StepModeLabel = b.config.Localize(locale, b.modes[mode])
	if mode == approvalpolicy.StepModeCap {
		block.StepCap = &capValue
	}
	if total, err := b.config.Usage.AccessKeyStepsTotal(ctx, subject.OwnerID, b.counted); err != nil {
		b.config.Logger.Warn("MCP 状态块：累计步数读取失败", "owner_id", subject.OwnerID, "error", err)
	} else {
		block.TotalUsedSteps = &total
	}
	if subject.AssetID != "" {
		asset := &Asset{ID: subject.AssetID}
		title := subject.AssetTitle
		if title == "" {
			if read, found, err := b.config.Usage.AssetTitle(ctx, subject.OwnerID, subject.AssetID); err != nil {
				b.config.Logger.Warn("MCP 状态块：资产标题读取失败", "owner_id", subject.OwnerID, "asset_id", subject.AssetID, "error", err)
			} else if found {
				title = read
			}
		}
		if title != "" {
			asset.Title = &title
		}
		if used, err := b.config.Usage.AssetStepsUsed(ctx, subject.OwnerID, subject.AssetID, b.counted); err != nil {
			b.config.Logger.Warn("MCP 状态块：资产步数读取失败", "owner_id", subject.OwnerID, "asset_id", subject.AssetID, "error", err)
		} else {
			asset.UsedSteps = &used
			if mode == approvalpolicy.StepModeCap {
				// The asset may have been granted extra steps by the person
				// (#330 step 5C); an unreadable grant is treated as none, and
				// the number stays a fact about the stored setting.
				extra := 0
				if granted, err := b.config.Usage.AssetExtraSteps(ctx, subject.OwnerID, subject.AssetID); err != nil {
					b.config.Logger.Warn("MCP 状态块：资产追加步数读取失败，按 0 处理",
						"owner_id", subject.OwnerID, "asset_id", subject.AssetID, "error", err)
				} else {
					extra = granted
				}
				left := max(capValue+extra-used, 0)
				asset.StepsLeft = &left
			}
		}
		block.Asset = asset
		block.summaryParts = summaryFacts{hasAsset: true, assetSteps: asset.UsedSteps}
	}
	block.Balances = b.balances(ctx)
	if len(subject.Costs) > 0 {
		block.StepCosts = append(block.StepCosts, subject.Costs...)
	}
	if subject.TaskID != "" && b.config.Inspections != nil {
		inspection, found, err := b.config.Inspections.Inspection(ctx, subject.OwnerID, subject.TaskID)
		switch {
		case err != nil:
			b.config.Logger.Warn("MCP 状态块：自检结论读取失败，inspection 为 null",
				"owner_id", subject.OwnerID, "task_id", subject.TaskID, "error", err)
		case !found || inspection == nil:
			// No record is normal: 2D, MCP-only and never-reviewed tasks have
			// no platform verdict. It is reported as null and not logged.
		default:
			// failed is always an array, never null.
			if inspection.Failed == nil {
				inspection.Failed = []InspectionFailedCheck{}
			}
			block.Inspection = inspection
		}
	}
	return block
}

func (b *Builder) balances(ctx context.Context) []Balance {
	accounts := b.accounts
	if len(accounts) == 0 {
		return []Balance{}
	}
	readings := b.config.Balances.Read(ctx, accounts)
	result := make([]Balance, 0, len(accounts))
	for index, account := range accounts {
		label := b.config.AccountLabels[account]
		if label == "" {
			label = account
		}
		entry := Balance{Provider: account, Label: label, State: BalanceUnknown}
		if index < len(readings) && readings[index].Known {
			reading := readings[index]
			readAt := reading.ReadAt.UTC().Format(time.RFC3339)
			entry.State, entry.ReadAt = BalanceOK, &readAt
			if reading.Native != nil {
				amount, unit, unitLabel := reading.Native.Amount, reading.Native.Unit, reading.Native.UnitLabel
				entry.Amount, entry.Unit, entry.UnitLabel = &amount, &unit, &unitLabel
			} else {
				amount := strconv.FormatFloat(reading.Value, 'f', -1, 64)
				entry.Amount = &amount
			}
		}
		result = append(result, entry)
	}
	return result
}

// Assets lists the person's assets (chat sessions) that access keys worked on,
// the most recently active first, bounded by the Schema's max_assets. More is
// true when older ones are left out.
func (b *Builder) Assets(ctx context.Context, ownerID string) ([]executiongrant.AccessKeyAsset, bool, error) {
	return b.config.Usage.AccessKeyAssets(ctx, ownerID, b.counted, b.config.StatusBlock.MaxAssets)
}

// Summary is the one short line appended to the text content of a response.
func (b *Builder) Summary(locale string, block Block) string {
	summary := b.config.StatusBlock.Summary
	local := func(text string) string { return b.config.Localize(locale, text) }
	unknown := local(summary.Unknown)
	number := func(value *int) string {
		if value == nil {
			return unknown
		}
		return strconv.Itoa(*value)
	}
	items := make([]string, 0, len(block.Balances))
	for _, balance := range block.Balances {
		if balance.State != BalanceOK || balance.Amount == nil {
			items = append(items, strings.NewReplacer("{label}", balance.Label).Replace(local(summary.BalanceUnknown)))
			continue
		}
		unitLabel := ""
		if balance.UnitLabel != nil {
			unitLabel = *balance.UnitLabel
		}
		items = append(items, strings.TrimSpace(strings.NewReplacer(
			"{label}", balance.Label, "{amount}", *balance.Amount, "{unit_label}", unitLabel,
		).Replace(local(summary.BalanceItem))))
	}
	template := local(summary.TemplateNoAsset)
	if block.summaryParts.hasAsset {
		template = local(summary.Template)
	}
	return strings.NewReplacer(
		"{mode}", block.StepModeLabel,
		"{asset_steps}", number(block.summaryParts.assetSteps),
		"{total_steps}", number(block.TotalUsedSteps),
		"{balances}", strings.Join(items, summary.Separator),
	).Replace(template)
}

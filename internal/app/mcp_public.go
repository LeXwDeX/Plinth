package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/mcppublic"
	"github.com/LeXwDeX/Plinth/internal/provideraccounts"
)

// The external MCP listener (#332) is a second http.Server of the process. It
// is started only when the Product Schema enables application.mcp_public and
// the runtime configuration names an address (mcp_public_listen_addr); with
// either missing nothing listens. It serves the MCP routes and nothing else:
// not the pages, not /api/v1/*, and it is not behind the reverse proxy that
// fronts the main server. It has no TLS: it is meant for a LAN or the local
// host, and a deployment that exposes it publicly configures the certificate
// in the process or terminates TLS in front of it.

// mcpStatusFromSchema builds the builder of the status block every external MCP
// response carries. It is nil when the Catalog does not declare the service.
func mcpStatusFromSchema(
	application catalog.ApplicationSchema,
	settings *accountsettings.Service,
	usage executiongrant.AccessKeyUsageStore,
	balances *provideraccounts.Cache,
	chatService *chat.Service,
	texts *catalog.TextLocalizer,
	logger *slog.Logger,
) (*mcppublic.Builder, error) {
	service := application.MCPPublic
	if service == nil {
		return nil, nil
	}
	if settings == nil || application.StepPolicy == nil || balances == nil {
		return nil, errors.New("对外 MCP 需要 3D 步数设置与共享余额缓存")
	}
	labels := providerBalanceLabels(application)
	var inspections mcppublic.InspectionReader
	if chatService != nil {
		inspections = mcpInspectionReader{chat: chatService, schema: application.Chat.SelfCheck}
	}
	return mcppublic.New(mcppublic.Config{
		StatusBlock: service.StatusBlock, StepPolicy: *application.StepPolicy, ProviderAccounts: application.Runtime.ProviderAccounts,
		AccountLabels: labels,
		Settings:      settings, Usage: usage, Balances: balances, Inspections: inspections,
		Localize: texts.Text, Logger: logger,
	})
}

// mcpInspectionReader adapts the persisted self-check record to the status
// block's inspection. mcppublic never sees chat.SelfCheckRecord.
type mcpInspectionReader struct {
	chat   *chat.Service
	schema catalog.ChatSelfCheckSchema
}

// Inspection returns the platform's last self-check of one task. The chat
// service reads across the owner's sessions, so a record written in a branch
// or ancestor session is found too.
func (reader mcpInspectionReader) Inspection(ctx context.Context, ownerID, taskID string) (*mcppublic.Inspection, bool, error) {
	record, found, err := reader.chat.SelfCheckByTask(ctx, ownerID, taskID)
	if err != nil || !found {
		return nil, false, err
	}
	return mcpInspectionFromRecord(reader.schema, record), true, nil
}

// mcpInspectionFromRecord maps the record to the block's shape. The verdict is
// the platform's derived delivery-state role (passed, issues, unchecked or
// uncheckable), never a word invented here; free text, counts and remedies
// stay out of the block. Only failed check ids, the waiver boolean and the
// time are reported.
func mcpInspectionFromRecord(schema catalog.ChatSelfCheckSchema, record chat.SelfCheckRecord) *mcppublic.Inspection {
	inspection := &mcppublic.Inspection{
		TaskID:  record.TaskID,
		Source:  mcppublic.InspectionSourcePlatformSelfCheck,
		Verdict: chat.SelfCheckDerivedStatus(schema, record).Role,
		Failed:  []mcppublic.InspectionFailedCheck{},
	}
	if conclusion := record.Conclusion; conclusion != nil {
		inspection.Waiver = strings.TrimSpace(conclusion.Waiver) != ""
		inspection.CheckedAt = mcpInspectionTime(conclusion.At)
		inspection.Purpose = mcpInspectionPurpose(conclusion.Purpose, conclusion.PurposeLabel, conclusion.PurposeSource)
	}
	_, latestOK := chat.SelfCheckLatestInspect(record)
	if latestOK != nil {
		for _, check := range latestOK.UsageFailed {
			inspection.Failed = append(inspection.Failed, mcppublic.InspectionFailedCheck{Check: check})
		}
		if inspection.Purpose == nil {
			inspection.Purpose = mcpInspectionPurpose(latestOK.Purpose, latestOK.PurposeLabel, latestOK.PurposeSource)
		}
		if inspection.CheckedAt == nil {
			inspection.CheckedAt = mcpInspectionTime(latestOK.At)
		}
	}
	return inspection
}

func mcpInspectionPurpose(id, label, source string) *mcppublic.InspectionPurpose {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return &mcppublic.InspectionPurpose{ID: id, Label: label, Source: source}
}

func mcpInspectionTime(at time.Time) *string {
	if at.IsZero() {
		return nil
	}
	formatted := at.UTC().Format(time.RFC3339)
	return &formatted
}

// mcpAssetsFromSchema builds the service that maps an external caller's asset
// names to chat sessions (#332). It is nil when the Catalog does not declare the
// external service.
func mcpAssetsFromSchema(
	application catalog.ApplicationSchema,
	sessions mcppublic.AssetSessions,
	store chat.MCPAssetStore,
	texts *catalog.TextLocalizer,
) (*mcppublic.Assets, error) {
	service := application.MCPPublic
	if service == nil {
		return nil, nil
	}
	return mcppublic.NewAssets(mcppublic.AssetsConfig{
		Schema: service.Assets, Sessions: sessions, Store: store, Localize: texts.Text,
	})
}

// validMCPListenAddress checks the runtime setting: host:port with a port from
// 0 (an ephemeral one, for tests) to 65535, never the main server's address.
func validMCPListenAddress(address, mainAddress string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("运行设置 mcp_public_listen_addr 必须是 host:port: %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return fmt.Errorf("运行设置 mcp_public_listen_addr 的端口无效: %q", port)
	}
	// Port 0 asks the system for a free port, so two of them never collide.
	if number != 0 && address == strings.TrimSpace(mainAddress) {
		return errors.New("运行设置 mcp_public_listen_addr 不能与 address 相同：对外 MCP 用独立的监听")
	}
	return nil
}

// newMCPServer builds the listener's http.Server from the Schema's limits.
func newMCPServer(address string, service catalog.MCPPublicSchema, handler http.Handler) *http.Server {
	seconds := func(value int) time.Duration { return time.Duration(value) * time.Second }
	return &http.Server{
		Addr:              strings.TrimSpace(address),
		Handler:           handler,
		ReadHeaderTimeout: seconds(service.ReadHeaderTimeoutSeconds),
		ReadTimeout:       seconds(service.ReadTimeoutSeconds),
		WriteTimeout:      seconds(service.WriteTimeoutSeconds),
		IdleTimeout:       seconds(service.IdleTimeoutSeconds),
	}
}

// MCPAddress is the address the external MCP listener is bound to, "" while
// the application does not run it. It is the real address once Run has bound
// it, which matters for an address with port 0.
func (a *Application) MCPAddress() string {
	a.mcpMutex.Lock()
	defer a.mcpMutex.Unlock()
	return a.mcpBound
}

// bindMCP opens the external listener's socket, if the application has one.
func (a *Application) bindMCP() (net.Listener, error) {
	if a.mcpServer == nil {
		return nil, nil
	}
	listener, err := net.Listen("tcp", a.mcpServer.Addr)
	if err != nil {
		return nil, fmt.Errorf("监听对外 MCP %s: %w", a.mcpServer.Addr, err)
	}
	a.mcpMutex.Lock()
	a.mcpBound = listener.Addr().String()
	a.mcpMutex.Unlock()
	return listener, nil
}

// serveMCP runs the external listener until it is shut down.
func (a *Application) serveMCP(listener net.Listener) error {
	a.logger.Info("对外 MCP 监听已启动（只挂 MCP 路由，没有 TLS，仅限内网或本机）",
		"address", listener.Addr().String(), "path", a.schema.MCPPublic.Path)
	return a.mcpServer.Serve(listener)
}

// providerBalanceLabels names the balance accounts; an account without a
// label is shown by its ID.
func providerBalanceLabels(application catalog.ApplicationSchema) map[string]string {
	labels := map[string]string{}
	for _, account := range application.Runtime.HTTP.LegacyProviderBalance.Accounts {
		labels[account.AccountID] = account.Label
	}
	return labels
}

// stepBalanceAccounts lists the balance accounts the platform Agent's 3D step
// segment shows. It is the external MCP status block's list when that service
// is declared, and the top bar's list otherwise; only accounts the runtime
// declares are kept, in Schema order.
func stepBalanceAccounts(application catalog.ApplicationSchema) []string {
	var accounts []string
	if application.MCPPublic != nil {
		accounts = application.MCPPublic.StatusBlock.BalanceAccounts
	} else {
		for _, account := range application.Runtime.HTTP.LegacyProviderBalance.Accounts {
			accounts = append(accounts, account.AccountID)
		}
	}
	declared := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if _, exists := application.Runtime.ProviderAccounts[account]; exists {
			declared = append(declared, account)
		}
	}
	return declared
}

// stepBalancesFromSchema builds the platform Agent's balance reader on the
// shared cache (#330). A nil cache, or a deployment without balance accounts,
// leaves the segment saying the balances are unknown.
func stepBalancesFromSchema(
	application catalog.ApplicationSchema,
	cache *provideraccounts.Cache,
) func(context.Context) []chat.StepBalance {
	if cache == nil {
		return nil
	}
	accounts := stepBalanceAccounts(application)
	if len(accounts) == 0 {
		return nil
	}
	labels := providerBalanceLabels(application)
	return func(ctx context.Context) []chat.StepBalance {
		readings := cache.Read(ctx, accounts)
		balances := make([]chat.StepBalance, 0, len(accounts))
		for index, account := range accounts {
			label := labels[account]
			if label == "" {
				label = account
			}
			balance := chat.StepBalance{Label: label}
			if index < len(readings) && readings[index].Known {
				reading := readings[index]
				balance.Known = true
				if reading.Native != nil {
					balance.Amount, balance.UnitLabel = reading.Native.Amount, reading.Native.UnitLabel
				} else {
					balance.Amount = strconv.FormatFloat(reading.Value, 'f', -1, 64)
				}
			}
			balances = append(balances, balance)
		}
		return balances
	}
}

// defaultStepCostTaskWindow bounds how many of an asset's latest Tasks the
// platform Agent's per-step cost read scans when the external MCP status block
// is not declared. Its window lives in that Schema section; without it the
// same bounded walk uses this default.
const defaultStepCostTaskWindow = 50

// stepCostTaskWindow is the Task window for the platform Agent's per-step
// costs, shared with the external MCP status block when declared.
func stepCostTaskWindow(application catalog.ApplicationSchema) int {
	if application.MCPPublic != nil && application.MCPPublic.StatusBlock.CostTaskWindow > 0 {
		return application.MCPPublic.StatusBlock.CostTaskWindow
	}
	return defaultStepCostTaskWindow
}

package app

import (
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
	accounts *provideraccounts.Service,
	texts *catalog.TextLocalizer,
	logger *slog.Logger,
) (*mcppublic.Builder, error) {
	service := application.MCPPublic
	if service == nil {
		return nil, nil
	}
	if settings == nil || application.StepPolicy == nil || accounts == nil {
		return nil, errors.New("对外 MCP 需要 3D 步数设置与 Provider 账户模块")
	}
	cache, err := provideraccounts.NewCache(accounts, provideraccounts.CacheConfig{
		TTL:     time.Duration(application.StepPolicy.BalanceCacheSeconds) * time.Second,
		Timeout: time.Duration(application.StepPolicy.BalanceReadTimeoutSeconds) * time.Second,
		Logger:  logger,
	})
	if err != nil {
		return nil, fmt.Errorf("余额读取缓存: %w", err)
	}
	labels := map[string]string{}
	for _, account := range application.Runtime.HTTP.LegacyProviderBalance.Accounts {
		labels[account.AccountID] = account.Label
	}
	return mcppublic.New(mcppublic.Config{
		StatusBlock: service.StatusBlock, StepPolicy: *application.StepPolicy, ProviderAccounts: application.Runtime.ProviderAccounts,
		AccountLabels: labels,
		Settings:      settings, Usage: usage, Balances: cache,
		Localize: texts.Text, Logger: logger,
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

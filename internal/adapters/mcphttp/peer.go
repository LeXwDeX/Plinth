// Package mcphttp implements MCP JSON-RPC framing over Streamable HTTP and a
// local request/response transport. Product registrations remain in Catalog.
package mcphttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/LeXwDeX/Plinth/internal/mcp"
)

// ProtocolVersion is a wire-format revision, not a mutable product default.
const ProtocolVersion = "2026-07-28"

type Policy struct {
	MaxResponseBytes int64
	MaxTools         int
}
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}
type Peer struct {
	endpoint string
	protocol string
	policy   Policy
	doer     Doer
	bearer   string
	sequence atomic.Int64
}

// NewHTTP accepts only an explicitly configured exact origin. Runtime callers
// cannot supply URLs or headers through tool arguments. Redirects are refused.
func NewHTTP(endpoint, protocol, bearer string, allowedOrigins []string, policy Policy) (*Peer, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, mcp.ErrUnavailable
	}
	allowed := false
	for _, candidate := range allowedOrigins {
		if candidate == u.Scheme+"://"+u.Host {
			allowed = true
			break
		}
	}
	if !allowed || strings.ContainsAny(bearer, "\r\n") {
		return nil, mcp.ErrUnavailable
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return newPeer(endpoint, protocol, bearer, client, policy)
}

func newPeer(endpoint, protocol, bearer string, doer Doer, policy Policy) (*Peer, error) {
	if protocol != ProtocolVersion || doer == nil || policy.MaxResponseBytes <= 0 || policy.MaxTools <= 0 {
		return nil, mcp.ErrUnavailable
	}
	return &Peer{endpoint: endpoint, protocol: protocol, bearer: bearer, doer: doer, policy: policy}, nil
}

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int64          `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

func (p *Peer) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := p.sequence.Add(1)
	params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": p.protocol, "io.modelcontextprotocol/clientInfo": map[string]string{"name": "Plinth", "version": "1"}, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, mcp.ErrProtocol
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, mcp.ErrUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", p.protocol)
	request.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		request.Header.Set("Mcp-Name", name)
	}
	if p.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+p.bearer)
	}
	response, err := p.doer.Do(request)
	if err != nil {
		return nil, mcp.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, mcp.ErrUnavailable
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, p.policy.MaxResponseBytes+1))
	if err != nil || int64(len(contents)) > p.policy.MaxResponseBytes {
		return nil, mcp.ErrProtocol
	}
	// A peer echoing its configured authentication value is never deliverable.
	if p.bearer != "" && bytes.Contains(contents, []byte(p.bearer)) {
		return nil, mcp.ErrProtocol
	}
	mediaType := strings.Split(response.Header.Get("Content-Type"), ";")[0]
	if mediaType == "text/event-stream" {
		return parseSSE(contents, id)
	}
	if mediaType != "application/json" {
		return nil, mcp.ErrProtocol
	}
	return parseResponse(contents, id)
}

func parseResponse(contents []byte, id int64) (json.RawMessage, error) {
	var value rpcResponse
	if json.Unmarshal(contents, &value) != nil || value.JSONRPC != "2.0" || string(value.ID) != strconv.FormatInt(id, 10) || len(value.Result) == 0 || string(value.Result) == "null" || len(value.Error) != 0 {
		return nil, mcp.ErrProtocol
	}
	return value.Result, nil
}

func parseSSE(contents []byte, id int64) (json.RawMessage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 4096), len(contents)+1)
	var lines []string
	var result json.RawMessage
	dispatch := func() error {
		if len(lines) == 0 {
			return nil
		}
		data := []byte(strings.Join(lines, "\n"))
		lines = nil
		var envelope map[string]json.RawMessage
		if json.Unmarshal(data, &envelope) != nil {
			return mcp.ErrProtocol
		}
		if len(envelope["id"]) == 0 {
			if string(envelope["jsonrpc"]) != `"2.0"` || len(envelope["method"]) == 0 {
				return mcp.ErrProtocol
			}
			return nil
		}
		if result != nil {
			return mcp.ErrProtocol
		}
		value, err := parseResponse(data, id)
		if err != nil {
			return err
		}
		result = value
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if scanner.Err() != nil {
		return nil, mcp.ErrProtocol
	}
	if err := dispatch(); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, mcp.ErrProtocol
	}
	return result, nil
}

func (p *Peer) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	var tools []mcp.Tool
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < p.policy.MaxTools; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := p.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var response struct {
			ResultType string     `json:"resultType"`
			Tools      []mcp.Tool `json:"tools"`
			NextCursor string     `json:"nextCursor"`
		}
		if json.Unmarshal(result, &response) != nil || response.ResultType != "complete" || response.Tools == nil || len(tools)+len(response.Tools) > p.policy.MaxTools {
			return nil, mcp.ErrProtocol
		}
		tools = append(tools, response.Tools...)
		if response.NextCursor == "" {
			return tools, nil
		}
		if seen[response.NextCursor] {
			return nil, mcp.ErrProtocol
		}
		seen[response.NextCursor] = true
		cursor = response.NextCursor
	}
	return nil, mcp.ErrProtocol
}
func (p *Peer) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	var values map[string]any
	if json.Unmarshal(arguments, &values) != nil || values == nil {
		return nil, mcp.ErrInvalidArguments
	}
	return p.call(ctx, "tools/call", map[string]any{"name": name, "arguments": values})
}
func (p *Peer) Discover(ctx context.Context) (json.RawMessage, error) {
	return p.call(ctx, "server/discover", map[string]any{})
}

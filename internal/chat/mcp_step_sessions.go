package chat

import "context"

// MCPStepAssetReader names the owner's sessions that are external MCP assets
// (#332). It is the narrow reader the application injects so the chat service
// can tell a steps session's entry (approvalpolicy.StepEntryMCP) without
// importing the mcppublic package, which already imports chat for the asset
// mapping.
type MCPStepAssetReader interface {
	// MCPAssetsOfSessions returns, by session ID, the owner's external MCP
	// assets among the sessions; a plain session or another owner's session is
	// left out.
	MCPAssetsOfSessions(ctx context.Context, owner string, sessionIDs []string) (map[string]MCPAsset, error)
}

// SetMCPStepSessions installs the reader that marks external MCP asset
// sessions (#330). The application passes it only when the Catalog declares
// both application.step_policy and application.mcp_public; nil keeps every
// session on its stored approval mode, the stricter result.
func (service *Service) SetMCPStepSessions(reader MCPStepAssetReader) {
	service.mcpStepSessions = reader
}

// isMCPStepSession reports whether the session is an external MCP asset. A
// read failure is returned: no caller may guess a session's role.
func (service *Service) isMCPStepSession(
	ctx context.Context,
	ownerID string,
	sessionID string,
) (bool, error) {
	if service.mcpStepSessions == nil || sessionID == "" {
		return false, nil
	}
	assets, err := service.mcpStepSessions.MCPAssetsOfSessions(ctx, ownerID, []string{sessionID})
	if err != nil {
		return false, err
	}
	_, marked := assets[sessionID]
	return marked, nil
}

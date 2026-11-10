package generationtool

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// commandReferenceScope is the server-held owner, session and frozen project
// a generation reference is resolved for. It never comes from tool arguments.
func commandReferenceScope(command chat.ToolCommand) references.ReferenceScope {
	return references.ReferenceScope{
		Owner:     generation.OwnerID(command.OwnerID),
		SessionID: command.SessionID,
		ProjectID: command.Options.ProjectID,
	}
}

// projectHandlePrefix is the Schema's @projectN prefix. An empty or absent
// project contract leaves project handles unrecognised.
func (executor *Executor) projectHandlePrefix() string {
	if executor.project == nil || executor.project.Agent == nil {
		return ""
	}
	return strings.TrimSpace(executor.project.Agent.HandlePrefix)
}

// projectHandleURLs resolves one @projectN token against the project snapshot
// frozen with the turn. It never reads a mutable project row and never accepts
// a URL or ID from the model: only the server-held context can name an asset.
func (executor *Executor) projectHandleURLs(command chat.ToolCommand, token string) (urls []string, matched bool, valid bool) {
	prefix := executor.projectHandlePrefix()
	if prefix == "" || !strings.HasPrefix(token, prefix) {
		return nil, false, false
	}
	asset := command.Options.ProjectContext.ProjectAssetByHandle(token)
	if asset == nil || len(asset.Images) == 0 {
		return nil, true, false
	}
	return append([]string(nil), asset.Images...), true, true
}

// projectHandleFailure words an unknown @projectN handle with the project
// Agent copy. The model can call list_project_assets and try again.
func (executor *Executor) projectHandleFailure(command chat.ToolCommand, handle string) error {
	template := "项目资产 {handle} 不存在或不属于当前项目。请调用 {tool} 查看可用句柄。"
	tool := "list_project_assets"
	if executor.project != nil && executor.project.Agent != nil {
		if declared := executor.project.Agent.UnknownAssetTemplate; declared != "" {
			template = declared
		}
		if executor.project.Agent.ToolName != "" {
			tool = executor.project.Agent.ToolName
		}
	}
	if localize := executor.localizeText(command); localize != nil {
		template = localize(template)
	}
	message := strings.NewReplacer("{handle}", handle, "{tool}", tool).Replace(template)
	// localizeText is already the turn's locale; project copy is Schema-owned
	// and may itself be localized for the projected Catalog.
	return &referenceRefusal{message: message}
}

// projectStyleSources names the project assets whose pictures this step
// really injected, after trimming and de-duplication. The list is empty when
// no project asset picture reached the submitted input; a style guide picture
// that is also an asset picture names that asset.
func (executor *Executor) projectStyleSources(command chat.ToolCommand, referenceURLs []string) []string {
	context := command.Options.ProjectContext
	if context == nil || len(context.Assets) == 0 || len(referenceURLs) == 0 {
		return nil
	}
	used := make(map[string]bool, len(referenceURLs))
	for _, referenceURL := range referenceURLs {
		if referenceURL != "" {
			used[referenceURL] = true
		}
	}
	names := make([]string, 0, len(context.Assets))
	seen := make(map[string]bool, len(context.Assets))
	for _, asset := range context.Assets {
		if asset.Name == "" || seen[asset.Name] {
			continue
		}
		for _, image := range asset.Images {
			if image != "" && used[image] {
				seen[asset.Name] = true
				names = append(names, asset.Name)
				break
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

package generationtool

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var ErrStyleLockReferenceURL = errors.New("generation tool: style lock reference URL is unavailable")

func styleLockImageOffering(offering generation.Offering) bool {
	if offering.Domain != "" {
		return offering.Domain == generation.DomainImage
	}
	// Historical test fixtures and old Offering snapshots may not carry Domain;
	// use the finite 2D capability vocabulary only as a compatibility fallback.
	switch offering.Capability {
	case generation.TextToImage, generation.ImageToImage, generation.ImageToMultiview, generation.BackgroundRemove:
		return true
	default:
		return false
	}
}

// styleLockImageTool reports whether either capability selected by a chat tool
// belongs to the image domain. This keeps style-lock input out of 3D tools
// without maintaining a second capability list in chat.
func (executor *Executor) styleLockImageTool(command chat.ToolCommand) bool {
	for _, capability := range []generation.Capability{
		command.Tool.Capability,
		command.Tool.FallbackCapability,
	} {
		if capability == "" {
			continue
		}
		for _, offering := range executor.gateway.Offerings(capability) {
			if styleLockImageOffering(offering) {
				return true
			}
		}
	}
	return false
}

// styleLockReferenceURLs turns the stored input-asset IDs into the same
// canonical input-asset URLs used by uploaded attachments. The resolver later
// proves ownership and converts those URLs back to asset IDs before prepare.
func (executor *Executor) styleLockReferenceURLs(command chat.ToolCommand) ([]string, error) {
	lock := command.Options.StyleLock
	if command.Options.StyleLockSkipped || !lock.Enabled || len(lock.References) == 0 || !executor.styleLockImageTool(command) {
		return nil, nil
	}
	if executor.inputAsset == "" || !strings.Contains(executor.inputAsset, "{assetID}") {
		return nil, ErrStyleLockReferenceURL
	}
	urls := make([]string, 0, len(lock.References))
	for _, assetID := range lock.References {
		assetID = strings.TrimSpace(assetID)
		if assetID == "" {
			return nil, fmt.Errorf("%w: empty asset ID", ErrStyleLockReferenceURL)
		}
		urls = append(urls, strings.ReplaceAll(executor.inputAsset, "{assetID}", url.PathEscape(assetID)))
	}
	return urls, nil
}

// styleLockPromptApplies reports whether the selected Offering takes the
// lock's text and edit-scope copy. Reference capabilities that name image
// capabilities keep the historical behavior for those; retexture takes only
// the picture (#250).
func (executor *Executor) styleLockPromptApplies(offering generation.Offering) bool {
	if offering.Domain == "" || offering.Domain == generation.DomainImage {
		if len(executor.chat.StyleLock.ReferenceCapabilities) == 0 {
			return true
		}
		for _, declared := range executor.chat.StyleLock.ReferenceCapabilities {
			if declared == offering.Capability {
				return true
			}
		}
	}
	return false
}

func (executor *Executor) styleLockUnavailableMessage(command chat.ToolCommand) string {
	message := executor.chat.Presentation.Messages.StyleLockReferenceUnavailable
	if message == "" {
		message = executor.chat.Presentation.Messages.StyleLockReferenceInvalid
	}
	return executor.localized(command, message)
}

// applyStyleLockPrompt appends the Schema-owned English lock copy after all
// existing reference, previous-prompt and Skill templates have been applied.
func (executor *Executor) applyStyleLockPrompt(command chat.ToolCommand, offering generation.Offering, prompt string) string {
	lock := command.Options.StyleLock
	if command.Options.StyleLockSkipped || !lock.Enabled || !executor.styleLockPromptApplies(offering) {
		return prompt
	}
	if text := strings.TrimSpace(lock.Text); text != "" {
		template := executor.chat.StyleLock.PromptTemplate
		if template == "" {
			template = "\n\nStyle lock: {style}"
		}
		prompt += strings.NewReplacer("{style}", text, "{text}", text).Replace(template)
	}
	if offering.Capability == generation.ImageToImage {
		if template := executor.chat.StyleLock.EditScopeTemplate; template != "" {
			prompt += template
		}
	}
	return prompt
}

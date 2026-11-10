package generationtool

import (
	"errors"
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
	if executor.gateway == nil {
		return false
	}
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

// styleLockUnavailableMessage is the Schema wording for an unusable style
// reference picture.
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
	if command.Options.StyleLockSkipped || !lock.Enabled || !styleLockImageOffering(offering) {
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

package generationtool

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// referenceClass orders reference pictures for a step (#250): identity anchors
// first, then the style lock and style anchors, then the turn's own ordinary
// references. The order is also the trimming priority: a model limit first
// drops ordinary references.
type referenceClass int

const (
	referenceClassIdentity referenceClass = iota
	referenceClassStyle
	referenceClassTurn
)

// anchorReferenceURLs resolves the identity anchors a call named in its
// Schema-declared anchor argument. Names are matched against the frozen turn
// snapshot; an unknown name or a style anchor is refused with the Schema text.
// The anchors' pictures become canonical input-asset URLs, the same ones an
// uploaded attachment uses, so the owning resolver proves the asset again.
func (executor *Executor) anchorReferenceURLs(
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
) (urls []string, purposes map[string]string, refusal string, err error) {
	argument := command.Tool.AnchorArgument
	if argument == "" {
		return nil, nil, "", nil
	}
	raw, present := arguments[argument]
	if !present || string(raw) == "null" {
		return nil, nil, "", nil
	}
	var names []string
	if json.Unmarshal(raw, &names) != nil {
		return nil, nil, executor.anchorInvalid(command, "参数必须是锚点名字数组"), nil
	}
	if len(names) == 0 {
		return nil, nil, "", nil
	}
	seen := map[string]bool{}
	purposes = map[string]string{}
	localize := executor.localizeText(command)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		var anchor chat.VisualAnchor
		found := false
		for _, candidate := range command.Options.VisualAnchors {
			if candidate.Name == name {
				anchor, found = candidate, true
				break
			}
		}
		if !found {
			return nil, nil, executor.anchorInvalid(command, "找不到名为「"+name+"」的锚点"), nil
		}
		if anchor.Kind != chat.VisualAnchorKindIdentity {
			return nil, nil, executor.anchorInvalid(command, "「"+name+"」不是身份锚"), nil
		}
		purpose := ""
		for _, kind := range executor.chat.VisualAnchors.Kinds {
			if kind.ID == anchor.Kind {
				purpose = kind.ReferencePurposeTemplate
				break
			}
		}
		if localize != nil {
			purpose = localize(purpose)
		}
		purpose = strings.ReplaceAll(purpose, "{name}", anchor.Name)
		for _, reference := range anchor.References {
			reference = strings.TrimSpace(reference)
			if reference == "" {
				continue
			}
			referenceURL, ok := executor.assetReferenceURL(reference)
			if !ok {
				return nil, nil, executor.anchorInvalid(command, "锚点参考图无法转换为输入资产 URL"), ErrStyleLockReferenceURL
			}
			urls = append(urls, referenceURL)
			if purpose != "" {
				if _, exists := purposes[referenceURL]; !exists {
					purposes[referenceURL] = purpose
				}
			}
		}
	}
	return urls, purposes, "", nil
}

func (executor *Executor) anchorInvalid(command chat.ToolCommand, reason string) string {
	template := executor.chat.VisualAnchors.InvalidTemplate
	if template == "" {
		template = "视觉锚无效：{reason}"
	}
	template = strings.ReplaceAll(template, "{reason}", reason)
	return executor.localized(command, template)
}

// assetReferenceURL turns a stored input-asset ID into the canonical input
// asset URL the resolver understands. The route is Schema-owned; without it
// the picture cannot be sent.
func (executor *Executor) assetReferenceURL(assetID string) (string, bool) {
	if executor.inputAsset == "" || !strings.Contains(executor.inputAsset, "{assetID}") {
		return "", false
	}
	return strings.ReplaceAll(executor.inputAsset, "{assetID}", url.PathEscape(assetID)), true
}

// styleReferenceURLs returns the style pictures a 2D step carries: the
// person's enabled style lock first, then the session's style anchors (#250).
// A style anchor is a reference-backed look, so it follows the same capability
// gate as the lock. The purposes are server-generated and keyed by URL; the
// identity purposes already present win when a picture is shared.
func (executor *Executor) styleReferenceURLs(command chat.ToolCommand) ([]string, map[string]string, error) {
	if !executor.styleReferencesApply(command) {
		return nil, nil, nil
	}
	var urls []string
	purposes := map[string]string{}
	localize := executor.localizeText(command)
	lock := command.Options.StyleLock
	if !command.Options.StyleLockSkipped && lock.Enabled {
		purpose := executor.chat.StyleLock.ReferencePurposeTemplate
		if localize != nil {
			purpose = localize(purpose)
		}
		for _, assetID := range lock.References {
			assetID = strings.TrimSpace(assetID)
			if assetID == "" {
				return nil, nil, ErrStyleLockReferenceURL
			}
			referenceURL, ok := executor.assetReferenceURL(assetID)
			if !ok {
				return nil, nil, ErrStyleLockReferenceURL
			}
			urls = append(urls, referenceURL)
			if purpose != "" {
				if _, exists := purposes[referenceURL]; !exists {
					purposes[referenceURL] = purpose
				}
			}
		}
	}
	for _, anchor := range command.Options.VisualAnchors {
		if anchor.Kind != chat.VisualAnchorKindStyle {
			continue
		}
		purpose := ""
		for _, kind := range executor.chat.VisualAnchors.Kinds {
			if kind.ID == anchor.Kind {
				purpose = kind.ReferencePurposeTemplate
				break
			}
		}
		if localize != nil {
			purpose = localize(purpose)
		}
		purpose = strings.ReplaceAll(purpose, "{name}", anchor.Name)
		for _, assetID := range anchor.References {
			assetID = strings.TrimSpace(assetID)
			if assetID == "" {
				continue
			}
			referenceURL, ok := executor.assetReferenceURL(assetID)
			if !ok {
				return nil, nil, ErrStyleLockReferenceURL
			}
			urls = append(urls, referenceURL)
			if purpose != "" {
				if _, exists := purposes[referenceURL]; !exists {
					purposes[referenceURL] = purpose
				}
			}
		}
	}
	return uniqueReferenceURLs(urls), purposes, nil
}

// styleReferencesApply reports whether a step may carry style pictures. A
// Catalog that declares reference_capabilities gates by the tool's own
// capabilities; a historical Catalog keeps the image-domain rule. The
// processing path calls this only after its own capability and Offering
// checks, so a declared retexture can carry the lock's picture (#250).
func (executor *Executor) styleReferencesApply(command chat.ToolCommand) bool {
	declared := executor.chat.StyleLock.ReferenceCapabilities
	if len(declared) == 0 {
		return executor.styleLockImageTool(command)
	}
	for _, capability := range []generation.Capability{command.Tool.Capability, command.Tool.FallbackCapability} {
		if capability == "" {
			continue
		}
		for _, wanted := range declared {
			if wanted == capability {
				return true
			}
		}
	}
	return false
}

// referenceHandleLabels names dropped ordinary pictures by the handle the
// session already gave them. A picture whose handle is unknown keeps a plain
// position label, never a fabricated handle the Agent could call.
func (executor *Executor) referenceHandleLabels(command chat.ToolCommand, urls []string) []string {
	byURL := map[string]string{}
	for _, message := range command.History {
		for _, attachment := range message.Attachments {
			if attachment.Handle == "" || attachment.ImageSlot <= 0 || attachment.ImageSlot > len(message.ImageURLs) {
				continue
			}
			byURL[message.ImageURLs[attachment.ImageSlot-1]] = attachment.Handle
		}
	}
	for _, attachment := range command.Options.Selection {
		if attachment.Handle == "" {
			continue
		}
		if referenceURL, ok := executor.assetReferenceURL(attachment.AssetID); ok {
			byURL[referenceURL] = attachment.Handle
		}
	}
	labels := make([]string, 0, len(urls))
	for index, referenceURL := range urls {
		if handle := byURL[referenceURL]; handle != "" {
			labels = append(labels, handle)
			continue
		}
		labels = append(labels, "#"+strconv.Itoa(index+1))
	}
	return labels
}

func mergeReferencePurposes(groups ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, group := range groups {
		for url, purpose := range group {
			if _, exists := merged[url]; !exists {
				merged[url] = purpose
			}
		}
	}
	return merged
}

func uniqueReferenceURLs(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(urls))
	unique := make([]string, 0, len(urls))
	for _, item := range urls {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		unique = append(unique, item)
	}
	return unique
}

// referenceGroups is the priority-ordered reference list and how many
// pictures each class contributes to it. TurnURLs keeps the ordinary pictures
// in their final order, so a model limit can name exactly the ones it cut.
type referenceGroups struct {
	URLs     []string
	Identity int
	Style    int
	Turn     int
	TurnURLs []string
}

// assembleReferenceGroups orders identity anchors, style pictures and the
// turn's own references (#250). Without an identity anchor the historical
// list is preserved byte for byte; with one, repeats are dropped and the first
// occurrence keeps its class and position, so an identity picture shared with
// an ordinary reference stays an anchor.
func assembleReferenceGroups(identity, style, turn []string) referenceGroups {
	identity = uniqueReferenceURLs(identity)
	style = uniqueReferenceURLs(style)
	if len(identity) == 0 {
		urls := append(append([]string(nil), style...), turn...)
		return referenceGroups{
			URLs: urls, Style: len(style), Turn: len(turn),
			TurnURLs: append([]string(nil), turn...),
		}
	}
	urls := append([]string(nil), identity...)
	styleKept := 0
	for _, reference := range style {
		if slices.Contains(urls, reference) {
			continue
		}
		urls = append(urls, reference)
		styleKept++
	}
	var turnKept []string
	for _, reference := range turn {
		if slices.Contains(urls, reference) {
			continue
		}
		urls = append(urls, reference)
		turnKept = append(turnKept, reference)
	}
	return referenceGroups{
		URLs: urls, Identity: len(identity), Style: styleKept,
		Turn: len(turnKept), TurnURLs: turnKept,
	}
}

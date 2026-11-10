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

// referenceClass tags one picture in the final reference list (#250). The
// order is also the trimming priority: a model limit cuts the tail, so the
// turn's other ordinary references go first, then style pictures, then identity
// anchors. A required turn input (the artboard snapshot or a picture the person
// marked "input") keeps the front of the list whatever its class priority: an
// image-to-image model edits its first picture (#266).
type referenceClass int

const (
	referenceClassTurn referenceClass = iota
	referenceClassIdentity
	referenceClassStyle
)

// anchorReferenceURLs resolves the identity anchors a call named in its
// Schema-declared anchor argument. Names are matched against the frozen turn
// snapshot; an unknown name or a style anchor is refused with the Schema text.
// The anchors' pictures are the stored references: an input asset becomes its
// canonical input URL, a generated picture keeps the platform Task asset URL
// that recordVisualAnchorReference stored. The owning resolver proves the
// object again when the step is prepared.
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
			referenceURL, ok := executor.storedReferenceURL(reference)
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

// storedReferenceURL turns a stored anchor reference into a URL the reference
// resolver understands. An input asset becomes the Schema-owned input route; a
// platform URL (a Task asset of a generated result, or an input asset URL)
// passes through unchanged and is verified by the resolver at prepare time.
func (executor *Executor) storedReferenceURL(reference string) (string, bool) {
	if strings.HasPrefix(reference, "/") || strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "https://") {
		return reference, true
	}
	return executor.assetReferenceURL(reference)
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
			referenceURL, ok := executor.storedReferenceURL(assetID)
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

// referenceGroups is the final reference list with a class per picture. The
// classes keep the trimming and reporting correct when the list is not a
// simple prefix per class (a required turn input leads the list, and style
// pictures sit between identity anchors and ordinary references). TurnURLs
// keeps the turn's pictures in the order the turn gave them, so a model limit
// can name exactly the ordinary references it cut.
type referenceGroups struct {
	URLs     []string
	Classes  []referenceClass
	Identity int
	Style    int
	Turn     int
	TurnURLs []string
}

// countClasses reports how many kept pictures of each class the prefix of
// length kept holds.
func (groups referenceGroups) countClasses(kept int) (identity, style, turn int) {
	if kept > len(groups.Classes) {
		kept = len(groups.Classes)
	}
	for index := 0; index < kept; index++ {
		switch groups.Classes[index] {
		case referenceClassIdentity:
			identity++
		case referenceClassStyle:
			style++
		default:
			turn++
		}
	}
	return identity, style, turn
}

// droppedTurnURLs names the turn pictures the model limit cut, in the turn's
// order. A picture that survives only once, shared with an anchor, is not
// dropped: it is still sent, in its anchor class.
func (groups referenceGroups) droppedTurnURLs(kept int) []string {
	cut := map[string]bool{}
	for index := kept; index < len(groups.URLs); index++ {
		if groups.Classes[index] == referenceClassTurn {
			cut[groups.URLs[index]] = true
		}
	}
	var dropped []string
	seen := map[string]bool{}
	for _, reference := range groups.TurnURLs {
		if !cut[reference] || seen[reference] {
			continue
		}
		seen[reference] = true
		dropped = append(dropped, reference)
	}
	return dropped
}

// referenceGroupsFor assembles the final list for a command. The leading
// pictures are the turn inputs an image-to-image model must see first: the
// bound artboard snapshot (#266) and the pictures the person marked "input".
func (executor *Executor) referenceGroupsFor(command chat.ToolCommand, identity, style, turn []string) referenceGroups {
	return assembleReferenceGroups(identity, style, executor.turnLeadingReferences(command, turn), turn)
}

// turnLeadingReferences returns the turn pictures that must stay ahead of the
// anchors: the artboard snapshot of a canvas turn and every picture whose
// declared attachment purpose is "input". A picture the person marked input
// but the call did not send is not leading; the purpose check refuses it
// later.
func (executor *Executor) turnLeadingReferences(command chat.ToolCommand, turn []string) []string {
	required := map[string]bool{}
	if executor.canvasTurn(command) && command.Tool.CanvasSource == canvasSourceBind {
		for _, reference := range selectedImageURLs(command) {
			required[reference] = true
		}
	}
	if command.Tool.ReferencePurposesArgument != "" {
		for handle, purpose := range command.Options.AttachmentPurposes {
			if purpose != "input" {
				continue
			}
			resolved, err := executor.handlePolicy().ResolveHandles(
				command.ReferenceHistory(), command.Options.Selection, []string{handle}, chat.AttachmentKindImage)
			if err != nil || len(resolved) != 1 || resolved[0].URL == "" {
				continue
			}
			required[resolved[0].URL] = true
		}
	}
	leading := make([]string, 0, len(required))
	for _, reference := range turn {
		if required[reference] {
			leading = append(leading, reference)
		}
	}
	return leading
}

// assembleReferenceGroups orders the final reference list (#250). A required
// turn input leads so the image-to-image model edits the intended base picture
// (#266); identity anchors follow, then style pictures, then the turn's other
// ordinary references. Retention follows the same order (truncateReferences
// keeps the head), so ordinary references are dropped first, then style, while
// at least one input and the identity anchors remain. Without anchors and
// without a required input the historical list is preserved byte for byte
// (three-view repeats included); a required input is moved ahead of the rest
// without otherwise reordering or de-duplicating.
func assembleReferenceGroups(identity, style, leading, turn []string) referenceGroups {
	identity = uniqueReferenceURLs(identity)
	style = uniqueReferenceURLs(style)
	if len(identity) == 0 && len(style) == 0 {
		if len(leading) == 0 {
			return referenceGroups{
				URLs: append([]string(nil), turn...), Classes: turnClasses(len(turn)),
				Turn: len(turn), TurnURLs: append([]string(nil), turn...),
			}
		}
		urls := append([]string(nil), leading...)
		for _, reference := range turn {
			if slices.Contains(leading, reference) {
				continue
			}
			urls = append(urls, reference)
		}
		return referenceGroups{
			URLs: urls, Classes: turnClasses(len(urls)),
			Turn: len(urls), TurnURLs: append([]string(nil), turn...),
		}
	}
	seen := map[string]bool{}
	urls := make([]string, 0, len(leading)+len(identity)+len(style)+len(turn))
	classes := make([]referenceClass, 0, cap(urls))
	add := func(reference string, class referenceClass) {
		if reference == "" || seen[reference] {
			return
		}
		seen[reference] = true
		urls = append(urls, reference)
		classes = append(classes, class)
	}
	for _, reference := range leading {
		add(reference, referenceClassTurn)
	}
	for _, reference := range identity {
		add(reference, referenceClassIdentity)
	}
	for _, reference := range style {
		add(reference, referenceClassStyle)
	}
	for _, reference := range turn {
		add(reference, referenceClassTurn)
	}
	groups := referenceGroups{URLs: urls, Classes: classes, TurnURLs: uniqueReferenceURLs(turn)}
	for _, class := range classes {
		switch class {
		case referenceClassIdentity:
			groups.Identity++
		case referenceClassStyle:
			groups.Style++
		default:
			groups.Turn++
		}
	}
	return groups
}

func turnClasses(count int) []referenceClass {
	classes := make([]referenceClass, count)
	return classes
}

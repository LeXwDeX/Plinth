package generationtool

import (
	"context"
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
// object again when the step is prepared. handles names each resolved picture
// by the anchor label the Agent saw, so an unavailable picture can be refused
// with that handle instead of a generic failure (#250).
func (executor *Executor) anchorReferenceURLs(
	command chat.ToolCommand,
	arguments map[string]json.RawMessage,
) (urls []string, purposes map[string]string, handles map[string]string, refusal string, err error) {
	argument := command.Tool.AnchorArgument
	if argument == "" {
		return nil, nil, nil, "", nil
	}
	raw, present := arguments[argument]
	if !present || string(raw) == "null" {
		return nil, nil, nil, "", nil
	}
	var names []string
	if json.Unmarshal(raw, &names) != nil {
		return nil, nil, nil, executor.anchorInvalid(command, "参数必须是锚点名字数组"), nil
	}
	if len(names) == 0 {
		return nil, nil, nil, "", nil
	}
	seen := map[string]bool{}
	purposes = map[string]string{}
	handles = map[string]string{}
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
			return nil, nil, nil, executor.anchorInvalid(command, "找不到名为「"+name+"」的锚点"), nil
		}
		if anchor.Kind != chat.VisualAnchorKindIdentity {
			return nil, nil, nil, executor.anchorInvalid(command, "「"+name+"」不是身份锚"), nil
		}
		purpose := ""
		kindLabel := anchor.Kind
		for _, kind := range executor.chat.VisualAnchors.Kinds {
			if kind.ID == anchor.Kind {
				purpose = kind.ReferencePurposeTemplate
				kindLabel = kind.Label
				break
			}
		}
		if localize != nil {
			purpose = localize(purpose)
		}
		purpose = strings.ReplaceAll(purpose, "{name}", anchor.Name)
		for index, reference := range anchor.References {
			reference = strings.TrimSpace(reference)
			if reference == "" {
				continue
			}
			referenceURL, ok := executor.storedReferenceURL(reference)
			if !ok {
				return nil, nil, nil, executor.anchorInvalid(command, "锚点参考图无法转换为输入资产 URL"), ErrStyleLockReferenceURL
			}
			urls = append(urls, referenceURL)
			if purpose != "" {
				if _, exists := purposes[referenceURL]; !exists {
					purposes[referenceURL] = purpose
				}
			}
			if _, exists := handles[referenceURL]; !exists {
				label := executor.chat.VisualAnchors.DroppedLabelTemplate
				if localize != nil && label != "" {
					label = localize(label)
				}
				if label == "" {
					label = kindLabel + "「" + anchor.Name + "」参考图 #{index}"
				}
				handles[referenceURL] = strings.NewReplacer(
					"{kind}", kindLabel,
					"{name}", anchor.Name,
					"{index}", strconv.Itoa(index+1),
				).Replace(label)
			}
		}
	}
	return urls, purposes, handles, "", nil
}

// anchorReferenceRefusal probes the named anchors' pictures after a reference
// resolution failed and reports the first unreadable one by the anchor handle
// the Agent saw (#250). A nil result means the named anchors were readable and
// the failure belongs to an ordinary reference.
func (executor *Executor) anchorReferenceRefusal(
	ctx context.Context,
	command chat.ToolCommand,
	referenceURLs []string,
	handles map[string]string,
) error {
	if len(handles) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, referenceURL := range referenceURLs {
		handle, named := handles[referenceURL]
		if !named || seen[referenceURL] {
			continue
		}
		seen[referenceURL] = true
		if _, err := executor.references.Resolve(
			ctx,
			generation.OwnerID(command.OwnerID),
			[]string{referenceURL},
		); err != nil {
			template := executor.chat.VisualAnchors.ReferenceUnavailableTemplate
			if localize := executor.localizeText(command); localize != nil && template != "" {
				template = localize(template)
			}
			if template == "" {
				template = "视觉锚参考图不可用：{handle}。请让用户重新上传这张图，或更新/删除这个锚点后再试。"
			}
			return &referenceRefusal{message: strings.ReplaceAll(template, "{handle}", handle)}
		}
	}
	return nil
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

// styleReferenceLabels names dropped style pictures by the handle the server
// already showed the Agent: the style lock's reference index, or a style
// anchor's kind/name/index. A picture without either keeps a plain position.
func (executor *Executor) styleReferenceLabels(command chat.ToolCommand, urls []string) []string {
	labels := map[string]string{}
	for referenceURL, label := range executor.styleAnchorLabels(command) {
		labels[referenceURL] = label
	}
	localize := executor.localizeText(command)
	lock := command.Options.StyleLock
	if !command.Options.StyleLockSkipped && lock.Enabled {
		template := "风格锁参考图 #{index}"
		if vision := executor.chat.Context.Vision; vision != nil && strings.TrimSpace(vision.StyleAnchorDroppedTemplate) != "" {
			template = vision.StyleAnchorDroppedTemplate
		}
		if localize != nil {
			template = localize(template)
		}
		for index, assetID := range lock.References {
			referenceURL, ok := executor.assetReferenceURL(strings.TrimSpace(assetID))
			if !ok {
				continue
			}
			labels[referenceURL] = strings.ReplaceAll(template, "{index}", strconv.Itoa(index+1))
		}
	}
	result := make([]string, 0, len(urls))
	for index, referenceURL := range urls {
		if label := labels[referenceURL]; label != "" {
			result = append(result, label)
			continue
		}
		result = append(result, "#"+strconv.Itoa(index+1))
	}
	return result
}

// styleAnchorLabels maps each stored style-anchor picture to the label the
// Agent saw. The lock's own pictures are deliberately absent: a refused lock
// reference keeps the historical style-lock wording.
func (executor *Executor) styleAnchorLabels(command chat.ToolCommand) map[string]string {
	labels := map[string]string{}
	schema := executor.chat.VisualAnchors
	localize := executor.localizeText(command)
	template := schema.DroppedLabelTemplate
	if localize != nil && template != "" {
		template = localize(template)
	}
	if template == "" {
		template = "{kind}「{name}」参考图 #{index}"
	}
	for _, anchor := range command.Options.VisualAnchors {
		if anchor.Kind != chat.VisualAnchorKindStyle {
			continue
		}
		kindLabel := anchor.Kind
		for _, kind := range schema.Kinds {
			if kind.ID == anchor.Kind {
				kindLabel = kind.Label
				break
			}
		}
		for index, reference := range anchor.References {
			referenceURL, ok := executor.storedReferenceURL(strings.TrimSpace(reference))
			if !ok {
				continue
			}
			labels[referenceURL] = strings.NewReplacer(
				"{kind}", kindLabel,
				"{name}", anchor.Name,
				"{index}", strconv.Itoa(index+1),
			).Replace(template)
		}
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

func mergeReferenceLabels(groups ...map[string]string) map[string]string {
	return mergeReferencePurposes(groups...)
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
	// StyleURLs keeps the style pictures in their own order, so a model limit
	// can name exactly the style pictures it cut (#250).
	StyleURLs []string
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

// droppedStyleURLs names the style pictures the model limit cut, in their own
// order. A picture that survives only once, shared with an identity anchor or
// a turn input, is not dropped: it is still sent, in its earlier class.
func (groups referenceGroups) droppedStyleURLs(kept int) []string {
	cut := map[string]bool{}
	for index := kept; index < len(groups.URLs); index++ {
		if groups.Classes[index] == referenceClassStyle {
			cut[groups.URLs[index]] = true
		}
	}
	var dropped []string
	seen := map[string]bool{}
	for _, reference := range groups.StyleURLs {
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
	groups := referenceGroups{URLs: urls, Classes: classes, TurnURLs: uniqueReferenceURLs(turn), StyleURLs: append([]string(nil), style...)}
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

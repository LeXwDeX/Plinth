package catalog

// UI locales: declaration, bundle validation, request locale resolution and
// the per-locale public projection.
//
// The default locale's copy lives where it always did (content.messages and
// the structured presentation fields). Every other locale is a bundle with
// the same message keys plus `fields`: stable paths into the application
// document whose string value the locale replaces. Paths address list items
// by an identity field (`content.navigation[key=workspace].label`), never by
// position, so reordering a list cannot silently move a translation.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// LocalizationSchema declares the UI locales and the bundles of every locale
// other than the default.
type LocalizationSchema struct {
	DefaultLocale string                 `json:"default_locale"`
	Locales       []LocaleSchema         `json:"locales"`
	Preference    LocalePreferenceSchema `json:"preference"`
	// Coverage lists application paths whose Chinese string leaves every
	// non-default bundle must translate through `fields`. It grows phase by
	// phase until it covers the whole document.
	Coverage []string `json:"coverage"`
	// LocaleFields are paths whose value is the formatting locale itself
	// (Intl tags); the projection sets them to the active locale.
	LocaleFields []string `json:"locale_fields"`
	// PersistedDefaults are message keys whose text is saved into a person's
	// data as an untouched default (the first canvas layer's name). The public
	// projection lists the text of every locale for them, so the browser can
	// tell such a default from a name the person chose and show it in the
	// reader's locale without changing the saved data.
	PersistedDefaults []string                `json:"persisted_defaults,omitempty"`
	Bundles           map[string]LocaleBundle `json:"bundles"`
}

// LocaleSchema is one selectable UI locale. ID is a BCP 47 tag used for
// `html lang`, Intl formatting and the persisted preference; Label is the
// locale's own name, shown untranslated in the switcher.
type LocaleSchema struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// LocalePreferenceSchema names the cookie that persists a person's choice.
type LocalePreferenceSchema struct {
	Cookie        string `json:"cookie"`
	MaxAgeSeconds int    `json:"max_age_seconds"`
}

// LocaleBundle is the copy of one non-default locale.
type LocaleBundle struct {
	Messages map[string]string `json:"messages"`
	Fields   map[string]string `json:"fields"`
	// OfferingTexts translates Offering copy by its default-locale text.
	// Offerings repeat the same labels across dozens of models and some of
	// their lists (input rules, an option whose value is empty) have no
	// identity field, so they are translated by text, not by path.
	OfferingTexts map[string]string `json:"offering_texts,omitempty"`
}

// OfferingsCoverage is the coverage entry naming the document's offerings
// rather than an application path; Load checks it against OfferingTexts.
const OfferingsCoverage = "offerings"

// PublicLocalizationSchema is what the browser learns about locales: the
// choices and which one this projection is in, never the other bundles.
type PublicLocalizationSchema struct {
	DefaultLocale string                 `json:"default_locale"`
	ActiveLocale  string                 `json:"active_locale"`
	Locales       []LocaleSchema         `json:"locales"`
	Preference    LocalePreferenceSchema `json:"preference"`
	// PersistedDefaults maps each persisted-default message key to its text in
	// every locale (see LocalizationSchema.PersistedDefaults).
	PersistedDefaultTexts map[string][]string `json:"persisted_default_texts,omitempty"`
}

var (
	localeIDPattern      = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
	cookieNamePattern    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	messagePlaceholder   = regexp.MustCompile(`\{([A-Za-z0-9_.-]+)\}`)
	chineseTextCharacter = regexp.MustCompile(`[\x{3000}-\x{303f}\x{3400}-\x{9fff}\x{f900}-\x{faff}\x{ff00}-\x{ffef}]`)
)

// effectiveLocalization returns the declared localization, or a single
// default locale taken from runtime.frontend.display_locale.
func (source ApplicationSchema) effectiveLocalization() LocalizationSchema {
	if source.Localization != nil {
		return *source.Localization
	}
	locale := source.Runtime.Frontend.DisplayLocale
	return LocalizationSchema{
		DefaultLocale: locale,
		Locales:       []LocaleSchema{{ID: locale, Label: locale}},
	}
}

// ResolveLocale picks the UI locale for one request: the persisted choice
// when it names a declared locale, then the best Accept-Language match
// (exact tag first, then the same primary language), then the default.
func (source ApplicationSchema) ResolveLocale(preferred, acceptLanguage string) string {
	localization := source.effectiveLocalization()
	if locale, ok := matchLocaleExactly(localization.Locales, preferred); ok {
		return locale
	}
	for _, tag := range parseAcceptLanguage(acceptLanguage) {
		if locale, ok := matchLocaleExactly(localization.Locales, tag); ok {
			return locale
		}
		if locale, ok := matchLocaleLanguage(localization.Locales, tag); ok {
			return locale
		}
	}
	return localization.DefaultLocale
}

func matchLocaleExactly(locales []LocaleSchema, tag string) (string, bool) {
	tag = strings.TrimSpace(tag)
	for _, locale := range locales {
		if tag != "" && strings.EqualFold(locale.ID, tag) {
			return locale.ID, true
		}
	}
	return "", false
}

func matchLocaleLanguage(locales []LocaleSchema, tag string) (string, bool) {
	language := primaryLanguage(tag)
	for _, locale := range locales {
		if language != "" && strings.EqualFold(primaryLanguage(locale.ID), language) {
			return locale.ID, true
		}
	}
	return "", false
}

func primaryLanguage(tag string) string {
	language, _, _ := strings.Cut(strings.TrimSpace(tag), "-")
	return language
}

// parseAcceptLanguage returns the header's language tags by descending
// quality, keeping header order for ties and dropping q=0 and "*".
func parseAcceptLanguage(header string) []string {
	type weighted struct {
		tag     string
		quality float64
	}
	var tags []weighted
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" || !localeIDPattern.MatchString(strings.ToLower(tag)) {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if !found || strings.TrimSpace(name) != "q" {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				parsed = 0
			}
			quality = parsed
		}
		if quality <= 0 {
			continue
		}
		tags = append(tags, weighted{tag: tag, quality: quality})
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].quality > tags[j].quality })
	result := make([]string, 0, len(tags))
	for _, item := range tags {
		result = append(result, item.tag)
	}
	return result
}

func validateLocalization(schema ApplicationSchema) error {
	if err := validateCountForms(schema); err != nil {
		return err
	}
	localization := schema.Localization
	if localization == nil {
		return nil
	}
	if len(localization.Locales) < 2 {
		return errors.New("locales 至少声明两种语言；单语言时省略 localization")
	}
	declared := map[string]bool{}
	declaredCaseInsensitive := map[string]bool{}
	for index, locale := range localization.Locales {
		if !localeIDPattern.MatchString(locale.ID) {
			return fmt.Errorf("locales[%d].id 不是有效的 BCP 47 语言标签: %q", index, locale.ID)
		}
		if declaredCaseInsensitive[strings.ToLower(locale.ID)] {
			return fmt.Errorf("locales[%d].id 重复: %q", index, locale.ID)
		}
		if strings.TrimSpace(locale.Label) == "" {
			return fmt.Errorf("locales[%d].label 不能为空", index)
		}
		declaredCaseInsensitive[strings.ToLower(locale.ID)] = true
		declared[locale.ID] = true
	}
	if !declared[localization.DefaultLocale] {
		return fmt.Errorf("default_locale %q 不在 locales 中", localization.DefaultLocale)
	}
	if localization.DefaultLocale != schema.Runtime.Frontend.DisplayLocale {
		return fmt.Errorf(
			"default_locale %q 必须与 runtime.frontend.display_locale %q 一致",
			localization.DefaultLocale, schema.Runtime.Frontend.DisplayLocale,
		)
	}
	if !cookieNamePattern.MatchString(localization.Preference.Cookie) {
		return fmt.Errorf("preference.cookie 无效: %q", localization.Preference.Cookie)
	}
	if localization.Preference.MaxAgeSeconds <= 0 {
		return errors.New("preference.max_age_seconds 必须为正数")
	}
	if _, exists := localization.Bundles[localization.DefaultLocale]; exists {
		return fmt.Errorf("默认语言 %q 的文案就是 Schema 本身，不能再声明 bundle", localization.DefaultLocale)
	}
	for locale := range localization.Bundles {
		if !declared[locale] {
			return fmt.Errorf("bundles.%s 没有对应的 locales 声明", locale)
		}
	}

	if !strings.Contains(schema.Chat.Agent.ReplyLanguageTemplate, "{language}") {
		return errors.New("chat.agent.reply_language_template 必须声明并包含 {language}：非默认语言的对话轮次靠它让 Agent 用界面语言回复")
	}
	for _, key := range localization.PersistedDefaults {
		if strings.TrimSpace(schema.Content.Messages[key]) == "" {
			return fmt.Errorf("persisted_defaults %q 不是有文案的 content.messages key", key)
		}
	}
	for locale, maximum := range schema.Chat.Agent.TitleMaxCharsByLocale {
		if !declared[locale] || maximum <= 0 {
			return fmt.Errorf("chat.agent.title_max_characters_by_locale.%s 必须是已声明语言的正数", locale)
		}
	}

	document, err := localizationDocument(schema)
	if err != nil {
		return err
	}
	for _, path := range localization.LocaleFields {
		value, err := resolveLocalizationPath(document, path)
		if err != nil {
			return fmt.Errorf("locale_fields %q: %w", path, err)
		}
		if value != localization.DefaultLocale {
			return fmt.Errorf("locale_fields %q 的值 %q 不是默认语言 %q", path, value, localization.DefaultLocale)
		}
	}
	type coverageRoot struct {
		path string
		node any
		// leaf is the root's own address when the root is a string field.
		leaf leafKey
	}
	coverage := make([]coverageRoot, 0, len(localization.Coverage))
	for _, path := range localization.Coverage {
		if path == OfferingsCoverage {
			continue
		}
		node, err := resolveLocalizationNode(document, path)
		if err != nil {
			return fmt.Errorf("coverage %q: %w", path, err)
		}
		root := coverageRoot{path: path, node: node}
		if _, isString := node.(string); isString {
			parent, key, err := resolveLocalizationLeaf(document, path)
			if err != nil {
				return fmt.Errorf("coverage %q: %w", path, err)
			}
			root.leaf = leafKey{object: reflect.ValueOf(parent).Pointer(), key: key}
		}
		coverage = append(coverage, root)
	}

	for _, locale := range localization.Locales {
		if locale.ID == localization.DefaultLocale {
			continue
		}
		bundle, exists := localization.Bundles[locale.ID]
		if !exists {
			return fmt.Errorf("缺少 bundles.%s", locale.ID)
		}
		if err := validateBundleMessages(schema.Content.Messages, bundle.Messages); err != nil {
			return fmt.Errorf("bundles.%s.messages: %w", locale.ID, err)
		}
		covered, err := validateBundleFields(document, bundle.Fields)
		if err != nil {
			return fmt.Errorf("bundles.%s.fields: %w", locale.ID, err)
		}
		if err := validateOfferingTexts(bundle.OfferingTexts); err != nil {
			return fmt.Errorf("bundles.%s.offering_texts: %w", locale.ID, err)
		}
		var missing []string
		for _, root := range coverage {
			if text, isString := root.node.(string); isString {
				if chineseTextCharacter.MatchString(text) && !covered[root.leaf] {
					missing = append(missing, root.path)
				}
				continue
			}
			collectUncoveredChinese(root.node, root.path, covered, &missing)
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("bundles.%s.fields 缺少 %d 个中文字段的翻译: %s", locale.ID, len(missing), summarizeKeys(missing))
		}
	}
	return nil
}

func validateBundleMessages(base, translated map[string]string) error {
	var missing, extra, drift []string
	for key, value := range base {
		text, exists := translated[key]
		if !exists {
			missing = append(missing, key)
			continue
		}
		if strings.TrimSpace(text) == "" && strings.TrimSpace(value) != "" && !emptyTranslationAllowed(key) {
			drift = append(drift, key+"（空译文）")
			continue
		}
		if !samePlaceholders(value, text) {
			drift = append(drift, key)
		}
	}
	for key := range translated {
		if _, exists := base[key]; !exists {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(drift)
	switch {
	case len(missing) > 0:
		return fmt.Errorf("缺少 %d 个 key: %s", len(missing), summarizeKeys(missing))
	case len(extra) > 0:
		return fmt.Errorf("多出 %d 个默认语言没有的 key: %s", len(extra), summarizeKeys(extra))
	case len(drift) > 0:
		return fmt.Errorf("%d 个 key 的占位符与默认语言不一致: %s", len(drift), summarizeKeys(drift))
	}
	return nil
}

func validateOfferingTexts(texts map[string]string) error {
	sources := make([]string, 0, len(texts))
	for source := range texts {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	for _, source := range sources {
		switch {
		case !chineseTextCharacter.MatchString(source):
			return fmt.Errorf("%q 不是默认语言文案", source)
		case strings.TrimSpace(texts[source]) == "":
			return fmt.Errorf("%q 的译文为空", source)
		case !samePlaceholders(source, texts[source]):
			return fmt.Errorf("%q 的占位符与默认语言不一致", source)
		}
	}
	return nil
}

// ValidateOfferingLocalization checks every bundle's offering_texts against
// the document's offerings (their generic JSON): a translation whose text no
// offering uses any more is stale, and when coverage names "offerings" every
// Chinese offering text needs a translation.
func ValidateOfferingLocalization(localization *LocalizationSchema, offerings []any) error {
	if localization == nil {
		return nil
	}
	used := map[string]bool{}
	collectChineseTexts(offerings, used)
	identifiers := offeringCopyIdentifiers(offerings)
	covered := false
	for _, path := range localization.Coverage {
		covered = covered || path == OfferingsCoverage
	}
	locales := make([]string, 0, len(localization.Bundles))
	for locale := range localization.Bundles {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	for _, locale := range locales {
		texts := localization.Bundles[locale].OfferingTexts
		var stale, missing []string
		for source := range texts {
			if !used[source] {
				stale = append(stale, source)
			}
		}
		if covered {
			for source := range used {
				if _, exists := texts[source]; !exists {
					missing = append(missing, source)
				}
			}
		}
		sort.Strings(stale)
		sort.Strings(missing)
		if len(stale) > 0 {
			return fmt.Errorf("bundles.%s.offering_texts 有 %d 条没有 Offering 使用的过期译文（删掉它们；测试夹具删改 Offering 文案后用 catalogtest.DropUnusedOfferingTexts）: %s", locale, len(stale), summarizeKeys(stale))
		}
		if len(missing) > 0 {
			return fmt.Errorf("bundles.%s.offering_texts 缺少 %d 条 Offering 中文文案的翻译: %s", locale, len(missing), summarizeKeys(missing))
		}
		if err := validateOfferingTextCopy(locale, texts, identifiers); err != nil {
			return err
		}
	}
	return nil
}

// validateOfferingTextCopy checks only the translated user-visible Offering
// copy. Messages, fields and user content are outside this scope.
func validateOfferingTextCopy(locale string, texts map[string]string, identifiers []string) error {
	for source, translated := range texts {
		for _, copy := range []string{source, translated} {
			for _, field := range internalOfferingCopyFields {
				if containsOfferingCopyWord(copy, field) {
					return fmt.Errorf("bundles.%s.offering_texts 的 %q 含内部字段 %q；用户文案应写可读含义", locale, source, field)
				}
			}
			for _, identifier := range identifiers {
				if containsOfferingCopyWord(copy, identifier) {
					return fmt.Errorf("bundles.%s.offering_texts 的 %q 含内部参数名 %q；请使用参数标签", locale, source, identifier)
				}
			}
		}
	}
	return nil
}

// offeringCopyIdentifiers derives machine-only parameter identifiers from the
// Offering document. It does not keep a Provider or parameter allowlist.
func offeringCopyIdentifiers(offerings []any) []string {
	identifiers := []string{}
	for _, raw := range offerings {
		offering, ok := raw.(map[string]any)
		if !ok || offering["id"] == nil {
			continue
		}
		parameters, ok := offering["parameters"].([]any)
		if !ok {
			continue
		}
		for _, rawParameter := range parameters {
			parameter, ok := rawParameter.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"name", "wire_name"} {
				identifier, _ := parameter[key].(string)
				if isMachineOnlyOfferingCopyIdentifier(identifier) {
					identifiers = append(identifiers, identifier)
				}
			}
		}
	}
	return machineOnlyOfferingCopyIdentifiers(identifiers)
}

func collectChineseTexts(node any, texts map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		for _, child := range value {
			collectChineseTexts(child, texts)
		}
	case []any:
		for _, child := range value {
			collectChineseTexts(child, texts)
		}
	case string:
		if chineseTextCharacter.MatchString(value) {
			texts[value] = true
		}
	}
}

// emptyTranslationAllowed covers affixes some languages simply do not have
// (Chinese "页" after a page number has no English counterpart).
func emptyTranslationAllowed(key string) bool {
	return strings.HasSuffix(key, "_suffix") || strings.HasSuffix(key, "_prefix")
}

func samePlaceholders(left, right string) bool {
	return reflect.DeepEqual(placeholderSet(left), placeholderSet(right))
}

func placeholderSet(text string) map[string]bool {
	names := map[string]bool{}
	for _, match := range messagePlaceholder.FindAllStringSubmatch(text, -1) {
		names[match[1]] = true
	}
	return names
}

// leafKey identifies one string leaf of the generic document: the map that
// holds it and its key there.
type leafKey struct {
	object uintptr
	key    string
}

// machineLocalizationLeafFields are machine identifiers. Translating one
// changes behavior: a route, a state code, an icon name, a wire field or a
// message key. "name" is context dependent (a provider model name is copy, a
// tool name is an identifier), so it is checked separately.
var machineLocalizationLeafFields = map[string]bool{
	"internal_state":       true,
	"public_state":         true,
	"href":                 true,
	"key":                  true,
	"icon":                 true,
	"color":                true,
	"error_color":          true,
	"retry_badge_color":    true,
	"track_color":          true,
	"countdown_color":      true,
	"method":               true,
	"id":                   true,
	"domain":               true,
	"outcome":              true,
	"placement":            true,
	"match":                true,
	"wire_name":            true,
	"implementation_key":   true,
	"label_key":            true,
	"hint_key":             true,
	"target_key":           true,
	"message_key":          true,
	"template_message_key": true,
	"offering_argument":    true,
	"offering_from_source": true,
}

func validateBundleFieldLeaf(steps []pathStep) error {
	last := steps[len(steps)-1]
	if machineLocalizationLeafFields[last.key] {
		return fmt.Errorf("字段 %q 是机器标识，不能翻译", last.key)
	}
	if last.key == "name" {
		// A tool's name selects the implementation; inside a tool list it is
		// never user-facing copy. Elsewhere (a provider model name, an MCP
		// preset name) "name" remains translatable.
		for index := 0; index+1 < len(steps); index++ {
			if steps[index].key == "tools" && steps[index+1].selector {
				return errors.New("工具列表中的 name 是工具标识，不能翻译")
			}
		}
	}
	return nil
}

func validateBundleFields(document map[string]any, fields map[string]string) (map[leafKey]bool, error) {
	covered := make(map[leafKey]bool, len(fields))
	paths := make([]string, 0, len(fields))
	for path := range fields {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if strings.HasPrefix(path, "content.messages") {
			return nil, fmt.Errorf("%q: content.messages 的翻译写在 bundle.messages", path)
		}
		steps, err := parseLocalizationPath(path)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		if err := validateBundleFieldLeaf(steps); err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		parent, key, err := resolveLocalizationLeaf(document, path)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		base, isString := parent[key].(string)
		if !isString {
			return nil, fmt.Errorf("%q 不是字符串字段", path)
		}
		if strings.TrimSpace(fields[path]) == "" {
			return nil, fmt.Errorf("%q 的译文为空", path)
		}
		if !samePlaceholders(base, fields[path]) {
			return nil, fmt.Errorf("%q 的占位符与默认语言不一致", path)
		}
		identity := leafKey{object: reflect.ValueOf(parent).Pointer(), key: key}
		if covered[identity] {
			return nil, fmt.Errorf("%q 与另一条路径指向同一字段", path)
		}
		covered[identity] = true
	}
	return covered, nil
}

func collectUncoveredChinese(node any, path string, covered map[leafKey]bool, missing *[]string) {
	switch value := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := path + "." + key
			if strings.ContainsAny(key, ".[]") {
				child = path + "[" + key + "]"
			}
			if child == "content.messages" {
				continue
			}
			if text, isString := value[key].(string); isString {
				if chineseTextCharacter.MatchString(text) &&
					!covered[leafKey{object: reflect.ValueOf(value).Pointer(), key: key}] {
					*missing = append(*missing, child)
				}
				continue
			}
			collectUncoveredChinese(value[key], child, covered, missing)
		}
	case []any:
		for index, item := range value {
			child := path + listItemSelector(value, index)
			if text, isString := item.(string); isString {
				if chineseTextCharacter.MatchString(text) {
					// Bare strings in a list have no stable address.
					*missing = append(*missing, child+"（列表中的字符串无法稳定寻址）")
				}
				continue
			}
			collectUncoveredChinese(item, child, covered, missing)
		}
	}
}

// selectorFields are the identity fields a coverage report suggests, in
// order of preference, for addressing a list item.
var selectorFields = []string{"id", "key", "name", "value", "variant", "internal_state", "type", "action", "href", "model_id", "days"}

// identityValue is a list item's identity as selector text: a string, or a
// number as written in the Schema (`[days=7]`).
func identityValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	}
	return "", false
}

// listItemSelector names a list item by its first unique identity field, so
// a coverage report prints a path that can be pasted into a bundle.
func listItemSelector(items []any, index int) string {
	element, isObject := items[index].(map[string]any)
	if isObject {
		for _, field := range selectorFields {
			value, isIdentity := identityValue(element[field])
			if !isIdentity || value == "" || strings.Contains(value, "]") {
				continue
			}
			matches := 0
			for _, other := range items {
				if object, ok := other.(map[string]any); ok {
					if candidate, ok := identityValue(object[field]); ok && candidate == value {
						matches++
					}
				}
			}
			if matches == 1 {
				return "[" + field + "=" + value + "]"
			}
		}
	}
	return fmt.Sprintf("[%d]", index)
}

func summarizeKeys(keys []string) string {
	const shown = 8
	if len(keys) <= shown {
		return strings.Join(keys, ", ")
	}
	return strings.Join(keys[:shown], ", ") + fmt.Sprintf(" …（另 %d 个）", len(keys)-shown)
}

// localizationDocument is the application as a generic JSON tree, the shape
// bundle paths address. The localization block itself is not addressable.
func localizationDocument(schema ApplicationSchema) (map[string]any, error) {
	schema.Localization = nil
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("编码 application: %w", err)
	}
	return decodeGenericJSON(encoded)
}

func decodeGenericJSON(encoded []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	return document, nil
}

// pathStep is one step of a bundle path: an object field (`name`), a map key
// written in brackets because it contains dots (`[board.empty]`), or a list
// item chosen by an identity field (`[key=workspace]`).
type pathStep struct {
	key      string
	selector bool
	field    string
	value    string
}

// parseLocalizationPath splits `a.b[field=value].c[dotted.key]`. Bracket
// contents may contain dots but not `]`.
func parseLocalizationPath(path string) ([]pathStep, error) {
	var steps []pathStep
	rest := path
	expectName := true
	for rest != "" {
		if rest[0] == '[' {
			closing := strings.IndexByte(rest, ']')
			if closing < 0 {
				return nil, errors.New("选择器缺少 ]")
			}
			inner := rest[1:closing]
			if field, value, found := strings.Cut(inner, "="); found {
				if field == "" || value == "" || len(steps) == 0 {
					return nil, errors.New("选择器必须是 [字段=值]")
				}
				steps = append(steps, pathStep{selector: true, field: field, value: value})
			} else {
				if inner == "" || len(steps) == 0 {
					return nil, errors.New("[键] 不能为空，也不能出现在路径开头")
				}
				steps = append(steps, pathStep{key: inner})
			}
			rest = rest[closing+1:]
			expectName = false
			continue
		}
		if !expectName {
			if rest[0] != '.' {
				return nil, errors.New("] 后必须是 . 、[ 或路径结束")
			}
			rest = rest[1:]
		}
		end := strings.IndexAny(rest, ".[")
		name := rest
		if end >= 0 {
			name = rest[:end]
		}
		if name == "" {
			return nil, errors.New("路径段为空")
		}
		steps = append(steps, pathStep{key: name})
		if end < 0 {
			return steps, nil
		}
		rest = rest[end:]
		if rest[0] == '.' {
			rest = rest[1:]
			if rest == "" {
				return nil, errors.New("路径段为空")
			}
		}
		expectName = rest == "" || rest[0] != '['
	}
	if len(steps) == 0 {
		return nil, errors.New("路径为空")
	}
	return steps, nil
}

func resolveLocalizationNode(document map[string]any, path string) (any, error) {
	steps, err := parseLocalizationPath(path)
	if err != nil {
		return nil, err
	}
	var current any = document
	for _, step := range steps {
		current, err = stepLocalizationPath(current, step)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

// resolveLocalizationLeaf returns the object holding the path's last key.
func resolveLocalizationLeaf(document map[string]any, path string) (map[string]any, string, error) {
	steps, err := parseLocalizationPath(path)
	if err != nil {
		return nil, "", err
	}
	last := steps[len(steps)-1]
	if last.selector {
		return nil, "", errors.New("路径必须以字段名或 [键] 结束")
	}
	var current any = document
	for _, step := range steps[:len(steps)-1] {
		current, err = stepLocalizationPath(current, step)
		if err != nil {
			return nil, "", err
		}
	}
	object, isObject := current.(map[string]any)
	if !isObject {
		return nil, "", errors.New("父节点不是对象")
	}
	if _, exists := object[last.key]; !exists {
		return nil, "", fmt.Errorf("字段 %q 不存在", last.key)
	}
	return object, last.key, nil
}

func resolveLocalizationPath(document map[string]any, path string) (string, error) {
	parent, key, err := resolveLocalizationLeaf(document, path)
	if err != nil {
		return "", err
	}
	value, isString := parent[key].(string)
	if !isString {
		return "", errors.New("不是字符串字段")
	}
	return value, nil
}

func stepLocalizationPath(current any, step pathStep) (any, error) {
	if !step.selector {
		object, isObject := current.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("%q 的父节点不是对象", step.key)
		}
		child, exists := object[step.key]
		if !exists {
			return nil, fmt.Errorf("字段 %q 不存在", step.key)
		}
		return child, nil
	}
	items, isList := current.([]any)
	if !isList {
		return nil, fmt.Errorf("[%s=%s] 用在了非列表上", step.field, step.value)
	}
	var match any
	count := 0
	for _, item := range items {
		element, isObject := item.(map[string]any)
		if !isObject {
			continue
		}
		if value, isIdentity := identityValue(element[step.field]); isIdentity && value == step.value {
			match = element
			count++
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("[%s=%s] 匹配 %d 项，必须恰好 1 项", step.field, step.value, count)
	}
	return match, nil
}

// EncodedPublicSchema is the public projection in one locale, encoded once.
type EncodedPublicSchema struct {
	Locale string
	JSON   []byte
	ETag   string
	Schema PublicApplicationSchema
}

// LocalizedPublicSchemas lazily encodes the public projection per locale.
// The application is immutable for the life of the process, so each locale
// is built at most once.
type LocalizedPublicSchemas struct {
	application   ApplicationSchema
	defaultLocale string
	slots         map[string]*localizedSlot
}

type localizedSlot struct {
	once       sync.Once
	projection *EncodedPublicSchema
	err        error
}

func NewLocalizedPublicSchemas(application ApplicationSchema) (*LocalizedPublicSchemas, error) {
	localization := application.effectiveLocalization()
	result := &LocalizedPublicSchemas{
		application:   cloneApplication(application),
		defaultLocale: localization.DefaultLocale,
		slots:         map[string]*localizedSlot{},
	}
	for _, locale := range localization.Locales {
		result.slots[locale.ID] = &localizedSlot{}
	}
	if _, declared := result.slots[result.defaultLocale]; !declared {
		return nil, fmt.Errorf("默认语言 %q 不在 locales 中", result.defaultLocale)
	}
	// The default projection is the one every fallback serves; building it
	// eagerly surfaces an encoding failure at construction.
	if _, err := result.build(result.defaultLocale); err != nil {
		return nil, err
	}
	return result, nil
}

// For returns the projection for a declared locale, or the default one.
func (s *LocalizedPublicSchemas) For(locale string) EncodedPublicSchema {
	if _, declared := s.slots[locale]; !declared {
		locale = s.defaultLocale
	}
	projection, err := s.build(locale)
	if err != nil {
		projection, _ = s.build(s.defaultLocale)
	}
	return *projection
}

func (s *LocalizedPublicSchemas) build(locale string) (*EncodedPublicSchema, error) {
	slot, declared := s.slots[locale]
	if !declared {
		return nil, fmt.Errorf("语言 %q 没有声明", locale)
	}
	slot.once.Do(func() {
		slot.projection, slot.err = s.application.localizedPublic(locale)
	})
	return slot.projection, slot.err
}

func (source ApplicationSchema) localizedPublic(locale string) (*EncodedPublicSchema, error) {
	localization := source.effectiveLocalization()
	public := source.Public()
	public.Localization.ActiveLocale = locale
	bundle, translated := localization.Bundles[locale]
	if translated {
		messages := make(map[string]string, len(public.Content.Messages))
		for key, value := range public.Content.Messages {
			messages[key] = value
		}
		for key, value := range bundle.Messages {
			messages[key] = value
		}
		public.Content.Messages = messages
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		return nil, fmt.Errorf("编码 %s 公开 Schema: %w", locale, err)
	}
	if translated || len(localization.LocaleFields) > 0 {
		document, err := decodeGenericJSON(encoded)
		if err != nil {
			return nil, err
		}
		if translated {
			for path, value := range bundle.Fields {
				// Server-only fields are absent from the public shape.
				if parent, key, err := resolveLocalizationLeaf(document, path); err == nil {
					parent[key] = value
				}
			}
		}
		for _, path := range localization.LocaleFields {
			if parent, key, err := resolveLocalizationLeaf(document, path); err == nil {
				parent[key] = locale
			}
		}
		if encoded, err = json.Marshal(document); err != nil {
			return nil, err
		}
		public = PublicApplicationSchema{}
		if err := json.Unmarshal(encoded, &public); err != nil {
			return nil, fmt.Errorf("解码 %s 公开 Schema: %w", locale, err)
		}
	}
	digest := sha256.Sum256(encoded)
	return &EncodedPublicSchema{
		Locale: locale,
		JSON:   encoded,
		ETag:   `"sha256-` + hex.EncodeToString(digest[:]) + `"`,
		Schema: public,
	}, nil
}

func (source ApplicationSchema) publicLocalization() PublicLocalizationSchema {
	localization := source.effectiveLocalization()
	result := PublicLocalizationSchema{
		DefaultLocale: localization.DefaultLocale,
		ActiveLocale:  localization.DefaultLocale,
		Locales:       append([]LocaleSchema(nil), localization.Locales...),
		Preference:    localization.Preference,
	}
	for _, key := range localization.PersistedDefaults {
		if result.PersistedDefaultTexts == nil {
			result.PersistedDefaultTexts = map[string][]string{}
		}
		texts := []string{source.Content.Messages[key]}
		for _, locale := range localization.Locales {
			if translated := localization.Bundles[locale.ID].Messages[key]; translated != "" && !slices.Contains(texts, translated) {
				texts = append(texts, translated)
			}
		}
		result.PersistedDefaultTexts[key] = texts
	}
	return result
}

func cloneLocalizationSchema(source *LocalizationSchema) *LocalizationSchema {
	if source == nil {
		return nil
	}
	result := *source
	result.Locales = append([]LocaleSchema(nil), source.Locales...)
	result.Coverage = append([]string(nil), source.Coverage...)
	result.LocaleFields = append([]string(nil), source.LocaleFields...)
	result.PersistedDefaults = append([]string(nil), source.PersistedDefaults...)
	result.Bundles = make(map[string]LocaleBundle, len(source.Bundles))
	for locale, bundle := range source.Bundles {
		copied := LocaleBundle{
			Messages: make(map[string]string, len(bundle.Messages)),
			Fields:   make(map[string]string, len(bundle.Fields)),
		}
		if bundle.OfferingTexts != nil {
			copied.OfferingTexts = make(map[string]string, len(bundle.OfferingTexts))
			for key, value := range bundle.OfferingTexts {
				copied.OfferingTexts[key] = value
			}
		}
		for key, value := range bundle.Messages {
			copied.Messages[key] = value
		}
		for key, value := range bundle.Fields {
			copied.Fields[key] = value
		}
		result.Bundles[locale] = copied
	}
	return &result
}

// TextLocalizer gives server code the copy of one locale: by message key, or
// by the default-locale text it already holds (a field value or message the
// handler read from the Schema). Text that is not Schema copy is returned as is.
type TextLocalizer struct {
	defaultLocale string
	labels        map[string]string
	base          map[string]string
	messages      map[string]map[string]string
	texts         map[string]map[string]string
}

func NewTextLocalizer(application ApplicationSchema) (*TextLocalizer, error) {
	localization := application.effectiveLocalization()
	result := &TextLocalizer{
		defaultLocale: localization.DefaultLocale,
		labels:        map[string]string{},
		base:          application.Content.Messages,
		messages:      map[string]map[string]string{},
		texts:         map[string]map[string]string{},
	}
	for _, locale := range localization.Locales {
		result.labels[locale.ID] = locale.Label
	}
	if len(localization.Bundles) == 0 {
		return result, nil
	}
	document, err := localizationDocument(application)
	if err != nil {
		return nil, err
	}
	for locale, bundle := range localization.Bundles {
		texts := map[string]string{}
		keys := make([]string, 0, len(bundle.Messages))
		for key := range bundle.Messages {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if source := application.Content.Messages[key]; source != "" {
				if _, taken := texts[source]; !taken {
					texts[source] = bundle.Messages[key]
				}
			}
		}
		paths := make([]string, 0, len(bundle.Fields))
		for path := range bundle.Fields {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			source, err := resolveLocalizationPath(document, path)
			if err != nil || source == "" {
				continue
			}
			if _, taken := texts[source]; !taken {
				texts[source] = bundle.Fields[path]
			}
		}
		for source, translated := range bundle.OfferingTexts {
			if _, taken := texts[source]; !taken {
				texts[source] = translated
			}
		}
		result.messages[locale] = bundle.Messages
		result.texts[locale] = texts
	}
	return result, nil
}

// Message returns a message in the locale, or the default text.
func (l *TextLocalizer) Message(locale, key string) string {
	if l == nil {
		return ""
	}
	if translated, ok := l.messages[locale][key]; ok {
		return translated
	}
	return l.base[key]
}

// Text returns the locale's copy of a default-locale Schema text. A nil
// localizer returns the text unchanged.
func (l *TextLocalizer) Text(locale, text string) string {
	if l == nil {
		return text
	}
	if translated, ok := l.texts[locale][text]; ok {
		return translated
	}
	return text
}

// TextIn is Text in the UI locale carried by ctx (see WithLocale).
func (l *TextLocalizer) TextIn(ctx context.Context, text string) string {
	return l.Text(LocaleFrom(ctx), text)
}

// LocalizeJSON replaces every Schema text among the string leaves of a
// generic JSON value (maps, lists, strings) with the locale's copy. Only
// pass values made entirely of Schema data, never user content: a person's
// own text that happens to equal a label would be translated too.
func (l *TextLocalizer) LocalizeJSON(locale string, value any) any {
	if l == nil || l.texts[locale] == nil {
		return value
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = l.LocalizeJSON(locale, child)
		}
	case []any:
		for index, child := range typed {
			typed[index] = l.LocalizeJSON(locale, child)
		}
	case string:
		// Every Schema text a bundle translates is Chinese; codes, IDs and
		// numbers written as strings are never looked up.
		if chineseTextCharacter.MatchString(typed) {
			return l.Text(locale, typed)
		}
	}
	return value
}

// LocaleLabel is the locale's own name ("English").
func (l *TextLocalizer) LocaleLabel(locale string) string {
	if l == nil || l.labels[locale] == "" {
		return locale
	}
	return l.labels[locale]
}

// Translates reports whether the locale has any copy other than the
// Schema's own default text.
func (l *TextLocalizer) Translates(locale string) bool {
	return l != nil && l.texts[locale] != nil
}

type localeContextKey struct{}

// WithLocale records the UI locale a piece of work answers in: a request,
// or an Agent turn frozen with the locale of the request that started it.
func WithLocale(ctx context.Context, locale string) context.Context {
	if locale == "" {
		return ctx
	}
	return context.WithValue(ctx, localeContextKey{}, locale)
}

// LocaleFrom returns the UI locale recorded by WithLocale, or "" (the
// default locale).
func LocaleFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	locale, _ := ctx.Value(localeContextKey{}).(string)
	return locale
}

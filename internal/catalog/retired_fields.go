package catalog

import (
	"bytes"
	"encoding/json"
	"slices"
)

// retiredCatalogFields are Catalog keys a release took out of the Product
// Schema whose old values mean nothing any more. A custom CATALOG_FILE written
// for an earlier release may still hold them; Load drops them before the
// strict decode, so such a document still loads and nothing reads the values.
// A key that changed meaning, or whose removal needs the operator to act, does
// not belong here: the strict decode refuses it instead (#521).
//
// Each entry is the path of object keys from the document root; "*" stands for
// every key of that object and "[]" for every element of an array.
var retiredCatalogFields = [][]string{
	// #553: a reply has no tool call limit; the limit, its runtime override,
	// the texts of the summary that ended a reply at the limit, the end reason
	// recorded for such a turn and the composer's old counter texts went.
	{"application", "chat", "agent", "max_tool_calls_per_reply"},
	{"application", "chat", "agent", "max_tool_calls_per_reply_setting"},
	{"application", "chat", "agent", "max_tool_calls_per_reply_maximum"},
	{"application", "runtime", "configuration", "settings", "agent_max_tool_calls_per_reply"},
	{"application", "chat", "presentation", "messages", "tool_round_limit"},
	{"application", "chat", "presentation", "messages", "tool_round_limit_pending"},
	{"application", "chat", "presentation", "messages", "tool_round_limit_pending_item"},
	{"application", "chat", "presentation", "messages", "tool_round_limit_pending_tool"},
	{"application", "localization", "bundles", "*", "fields", "chat.presentation.messages.tool_round_limit"},
	{"application", "localization", "bundles", "*", "fields", "chat.presentation.messages.tool_round_limit_pending"},
	{"application", "localization", "bundles", "*", "fields", "chat.presentation.messages.tool_round_limit_pending_item"},
	{"application", "localization", "bundles", "*", "fields", "chat.presentation.messages.tool_round_limit_pending_tool"},
	{"application", "chat", "state", "turn_end_reasons", "tool_limit"},
	// #490, #511: a tool's execution no longer declares a consent mode (the
	// approval policy decides), and the runtime routes no longer name agent_bind
	// (the call binds its Task through the operation record).
	{"application", "chat", "tools", "[]", "execution", "consent"},
	{"application", "chat", "agent_tools", "[]", "execution", "consent"},
	{"application", "agent_configuration", "mcp_presets", "[]", "tools", "[]", "execution", "consent"},
	{"application", "runtime", "http", "routes", "agent_bind"},
	{"application", "content", "messages", "ui.studio.tool_calls.label"},
	{"application", "content", "messages", "ui.studio.tool_calls.hint"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.tool_calls.label"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.tool_calls.hint"},
	// #586: the 2D image count is the Agent's call from what the person says;
	// the panel's session count control, its prompt block and its words went.
	{"application", "chat", "image_count"},
	{"application", "localization", "bundles", "*", "fields", "chat.image_count.context_template"},
	{"application", "localization", "bundles", "*", "fields", "chat.image_count.limit_template"},
	{"application", "localization", "bundles", "*", "fields", "chat.image_count.uncounted_template"},
	{"application", "localization", "bundles", "*", "fields", "chat.image_count.model_separator"},
	{"application", "localization", "bundles", "*", "fields", "chat.image_count.omitted_template"},
	{"application", "localization", "bundles", "*", "fields", "chat.image_count.reason_template"},
	{"application", "content", "messages", "ui.studio.params.count_less"},
	{"application", "content", "messages", "ui.studio.params.count_more"},
	{"application", "content", "messages", "ui.studio.params.count_minimum"},
	{"application", "content", "messages", "ui.studio.params.count_minimum_one"},
	{"application", "content", "messages", "ui.studio.params.count_maximum"},
	{"application", "content", "messages", "ui.studio.params.count_maximum_one"},
	{"application", "content", "messages", "ui.studio.params.summary_count"},
	{"application", "content", "messages", "ui.studio.params.summary_count_one"},
	{"application", "content", "messages", "ui.studio.params.default_count"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.count_less"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.count_more"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.count_minimum"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.count_minimum_one"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.count_maximum"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.count_maximum_one"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.summary_count"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.summary_count_one"},
	{"application", "localization", "bundles", "*", "messages", "ui.studio.params.default_count"},
}

// dropRetiredFields returns the document without the retired keys. A document
// that names none of them is returned as it is, unparsed; one that is not the
// expected shape is also left alone, for the strict decode to report.
func dropRetiredFields(document []byte) []byte {
	if !slices.ContainsFunc(retiredCatalogFields, func(path []string) bool {
		return bytes.Contains(document, []byte(`"`+path[len(path)-1]+`"`))
	}) {
		return document
	}
	for _, path := range retiredCatalogFields {
		if next, changed := dropRetiredField(document, path); changed {
			document = next
		}
	}
	return document
}

// dropRetiredField removes one path from an object. Only the objects along a
// path that changed are encoded again; every other value keeps its raw bytes.
func dropRetiredField(raw []byte, path []string) ([]byte, bool) {
	if path[0] == "[]" {
		var elements []json.RawMessage
		if json.Unmarshal(raw, &elements) != nil || elements == nil {
			return raw, false
		}
		changed := false
		for index, element := range elements {
			if next, elementChanged := dropRetiredField(element, path[1:]); elementChanged {
				elements[index] = next
				changed = true
			}
		}
		if !changed {
			return raw, false
		}
		encoded, err := json.Marshal(elements)
		if err != nil {
			return raw, false
		}
		return encoded, true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return raw, false
	}
	keys := []string{path[0]}
	if path[0] == "*" {
		keys = keys[:0]
		for key := range object {
			keys = append(keys, key)
		}
	}
	changed := false
	for _, key := range keys {
		child, exists := object[key]
		if !exists {
			continue
		}
		if len(path) == 1 {
			delete(object, key)
			changed = true
			continue
		}
		if next, childChanged := dropRetiredField(child, path[1:]); childChanged {
			object[key] = next
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return raw, false
	}
	return encoded, true
}

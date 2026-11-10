package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// ProductionBrief is a session's structured production brief (#103). The
// Schema declares which fields exist, their labels and their bounds; the
// service stores the normalized values and renders them into the system
// context on every turn. A missing brief is the empty value.
type ProductionBrief map[string]json.RawMessage

// Empty reports whether the brief holds no value at all.
func (brief ProductionBrief) Empty() bool {
	for _, value := range brief {
		if len(bytes.TrimSpace(value)) > 0 && string(bytes.TrimSpace(value)) != "null" {
			return false
		}
	}
	return true
}

// Text returns a text value, or the empty string when it is absent.
func (brief ProductionBrief) Text(name string) string {
	raw, exists := brief[name]
	if !exists {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// Flag returns a boolean value; absent reads as false.
func (brief ProductionBrief) Flag(name string) bool {
	raw, exists := brief[name]
	if !exists {
		return false
	}
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

// Integer returns an integer value and whether it is present.
func (brief ProductionBrief) Integer(name string) (int, bool) {
	raw, exists := brief[name]
	if !exists {
		return 0, false
	}
	var value json.Number
	if json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	number, err := value.Int64()
	if err != nil {
		return 0, false
	}
	return int(number), true
}

// BriefError is a refused brief: Reason names the field and the rule without
// exposing the rejected text. The Agent reads it through invalid_template.
type BriefError struct {
	Reason string
}

func (err *BriefError) Error() string { return err.Reason }

// ValidateProductionBrief checks a whole replacement against the Schema's
// declared fields and returns the normalized object. Unknown members are
// refused instead of dropped, so a typo cannot silently lose a locked choice.
// Empty text and null clear a field. A profile field must name a declared
// profile. The refusal wording comes from the Schema's messages, with the
// built-in sentence as the fallback for a historical Catalog (#103 review C3).
func ValidateProductionBrief(
	schema catalog.ChatProductionBriefSchema,
	profiles []catalog.ChatModel3DUsageProfile,
	imageProfiles []catalog.ChatImage2DUsageProfile,
	raw map[string]json.RawMessage,
) (ProductionBrief, error) {
	declared := make(map[string]catalog.ChatProductionBriefField, len(schema.Fields))
	for _, field := range schema.Fields {
		declared[field.Name] = field
	}
	profileIDs := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		profileIDs[profile.ID] = true
	}
	imageProfileIDs := make(map[string]bool, len(imageProfiles))
	for _, profile := range imageProfiles {
		imageProfileIDs[profile.ID] = true
	}
	messages := schema.Messages
	normalized := ProductionBrief{}
	for name, value := range raw {
		field, exists := declared[name]
		if !exists {
			return nil, &BriefError{Reason: briefMessage(messages.UnknownField, `未知字段 "{field}"`, "{field}", name)}
		}
		if len(bytes.TrimSpace(value)) == 0 || string(bytes.TrimSpace(value)) == "null" {
			continue
		}
		switch field.Type {
		case "profile":
			var purpose string
			if json.Unmarshal(value, &purpose) != nil {
				return nil, &BriefError{Reason: briefMessage(messages.NotString, `字段 "{field}" 必须是字符串`, "{field}", name)}
			}
			purpose = strings.TrimSpace(purpose)
			if purpose == "" {
				continue
			}
			if !profileIDs[purpose] {
				return nil, &BriefError{Reason: briefMessage(messages.ProfileUndeclared, `用途 "{value}" 未声明`, "{value}", purpose)}
			}
			normalized[name] = mustJSON(purpose)
		case "image_profile":
			var purpose string
			if json.Unmarshal(value, &purpose) != nil {
				return nil, &BriefError{Reason: briefMessage(messages.NotString, `字段 "{field}" 必须是字符串`, "{field}", name)}
			}
			purpose = strings.TrimSpace(purpose)
			if purpose == "" {
				continue
			}
			if !imageProfileIDs[purpose] {
				return nil, &BriefError{Reason: briefMessage(messages.ProfileUndeclared, `用途 "{value}" 未声明`, "{value}", purpose)}
			}
			normalized[name] = mustJSON(purpose)
		case "text":
			var text string
			if json.Unmarshal(value, &text) != nil {
				return nil, &BriefError{Reason: briefMessage(messages.NotString, `字段 "{field}" 必须是字符串`, "{field}", name)}
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			if field.MaximumCharacters > 0 && len([]rune(text)) > field.MaximumCharacters {
				return nil, &BriefError{Reason: briefMessage(
					messages.TextTooLong, `字段 "{field}" 不能超过 {maximum} 个字符`,
					"{field}", name, "{maximum}", fmt.Sprint(field.MaximumCharacters),
				)}
			}
			normalized[name] = mustJSON(text)
		case "boolean":
			var flag bool
			if json.Unmarshal(value, &flag) != nil {
				return nil, &BriefError{Reason: briefMessage(messages.NotBoolean, `字段 "{field}" 必须是布尔值`, "{field}", name)}
			}
			normalized[name] = mustJSON(flag)
		case "integer":
			var number json.Number
			if json.Unmarshal(value, &number) != nil {
				return nil, &BriefError{Reason: briefMessage(messages.NotInteger, `字段 "{field}" 必须是整数`, "{field}", name)}
			}
			integer, err := number.Int64()
			if err != nil {
				return nil, &BriefError{Reason: briefMessage(messages.NotInteger, `字段 "{field}" 必须是整数`, "{field}", name)}
			}
			if (field.Minimum > 0 && integer < int64(field.Minimum)) ||
				(field.Maximum > 0 && integer > int64(field.Maximum)) {
				return nil, &BriefError{Reason: briefMessage(
					messages.OutOfRange, `字段 "{field}" 必须在 {minimum} 与 {maximum} 之间`,
					"{field}", name, "{minimum}", fmt.Sprint(field.Minimum), "{maximum}", fmt.Sprint(field.Maximum),
				)}
			}
			normalized[name] = mustJSON(integer)
		default:
			return nil, &BriefError{Reason: briefMessage(
				messages.UnsupportedType, `字段 "{field}" 的类型 "{type}" 未实现`,
				"{field}", name, "{type}", field.Type,
			)}
		}
	}
	return normalized, nil
}

// briefMessage fills a Schema message, or the built-in fallback for a
// historical Catalog that declares no messages. Replacements is a flat
// placeholder/value list.
func briefMessage(template, fallback string, replacements ...string) string {
	if strings.TrimSpace(template) == "" {
		template = fallback
	}
	return strings.NewReplacer(replacements...).Replace(template)
}

// DecodeProductionBrief reads a stored brief. Malformed storage reads as the
// empty brief: an old row cannot block a turn.
func DecodeProductionBrief(stored string) ProductionBrief {
	if strings.TrimSpace(stored) == "" {
		return ProductionBrief{}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(stored), &fields) != nil {
		return ProductionBrief{}
	}
	return ProductionBrief(fields)
}

// EncodeProductionBrief returns the stored form; an empty brief stores "".
func EncodeProductionBrief(brief ProductionBrief) (string, error) {
	if brief.Empty() {
		return "", nil
	}
	encoded, err := json.Marshal(map[string]json.RawMessage(brief))
	if err != nil {
		return "", fmt.Errorf("encode production brief: %w", err)
	}
	return string(encoded), nil
}

func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

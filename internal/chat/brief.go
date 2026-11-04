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
// Empty text and null clear a field. purpose must name a declared profile.
func ValidateProductionBrief(
	schema catalog.ChatProductionBriefSchema,
	profiles []catalog.ChatModel3DUsageProfile,
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
	normalized := ProductionBrief{}
	for name, value := range raw {
		field, exists := declared[name]
		if !exists {
			return nil, &BriefError{Reason: fmt.Sprintf("未知字段 %q", name)}
		}
		if len(bytes.TrimSpace(value)) == 0 || string(bytes.TrimSpace(value)) == "null" {
			continue
		}
		switch field.Type {
		case "profile":
			var purpose string
			if json.Unmarshal(value, &purpose) != nil {
				return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 必须是字符串", name)}
			}
			purpose = strings.TrimSpace(purpose)
			if purpose == "" {
				continue
			}
			if !profileIDs[purpose] {
				return nil, &BriefError{Reason: fmt.Sprintf("用途 %q 未声明", purpose)}
			}
			normalized[name] = mustJSON(purpose)
		case "text":
			var text string
			if json.Unmarshal(value, &text) != nil {
				return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 必须是字符串", name)}
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			if field.MaximumCharacters > 0 && len([]rune(text)) > field.MaximumCharacters {
				return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 不能超过 %d 个字符", name, field.MaximumCharacters)}
			}
			normalized[name] = mustJSON(text)
		case "boolean":
			var flag bool
			if json.Unmarshal(value, &flag) != nil {
				return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 必须是布尔值", name)}
			}
			normalized[name] = mustJSON(flag)
		case "integer":
			var number json.Number
			if json.Unmarshal(value, &number) != nil {
				return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 必须是整数", name)}
			}
			integer, err := number.Int64()
			if err != nil {
				return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 必须是整数", name)}
			}
			if (field.Minimum > 0 && integer < int64(field.Minimum)) ||
				(field.Maximum > 0 && integer > int64(field.Maximum)) {
				return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 必须在 %d 与 %d 之间", name, field.Minimum, field.Maximum)}
			}
			normalized[name] = mustJSON(integer)
		default:
			return nil, &BriefError{Reason: fmt.Sprintf("字段 %q 的类型 %q 未实现", name, field.Type)}
		}
	}
	return normalized, nil
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

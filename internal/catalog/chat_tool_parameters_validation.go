package catalog

import (
	"errors"
	"fmt"
	"slices"

	"github.com/LeXwDeX/Plinth/internal/schema/toolschema"
)

// AgentToolArgumentOptions is how a chat Agent tool's parameters are parsed
// (#665): the root is an object, every object says additionalProperties, and
// only a deliberate map may stay open. The server checks the model's arguments
// against the same parse, so the Schema cannot declare a constraint that is
// not enforced.
var AgentToolArgumentOptions = toolschema.Options{RootObject: true, ClosedObjects: true, FreeFormObjects: true}

// generationToolArgumentOptions parses the generation tools' parameters. Their
// arguments are mapped onto Offering parameters and decoded leniently, so only
// the shape of the declaration is checked, not that every object is closed.
var generationToolArgumentOptions = toolschema.Options{RootObject: true}

// toolParametersError words a parse failure of a chat tool's declaration. A
// keyword outside the toolschema subset names the tool, the parameter and the
// keyword (#665).
func toolParametersError(section string, index int, tool string, err error) error {
	var unsupported *toolschema.UnsupportedKeywordError
	if errors.As(err, &unsupported) {
		parameter := unsupported.Parameter()
		if parameter == "" {
			parameter = "（根对象）"
		}
		return fmt.Errorf("%s[%d] %q 的参数 %s 使用了工具参数子集之外的关键字 %q（%s）",
			section, index, tool, parameter, unsupported.Keyword, unsupported.Path)
	}
	return fmt.Errorf("%s[%d] %q parameters: %w", section, index, tool, err)
}

// validateChatToolParameters parses every chat tool's parameter declaration
// (#665). Before, a declaration only had to be valid JSON: a required name
// that is not a property, a malformed enum or bound, or an open object a
// strict decoder refuses all reached the model unnoticed.
func validateChatToolParameters(schema ChatSchema) error {
	// Compatibility (#665): a generation tool's arguments are never checked
	// against its declaration (they are mapped onto the Offering's parameters,
	// which check them), so a keyword outside the subset that a Catalog written
	// before #665 declares there enforces nothing either way and does not stop
	// the Catalog from loading; any other defect still does. An Agent tool's
	// declaration is enforced on every call and its implementation_key binds it
	// to this binary, so a keyword the server would not enforce is refused.
	for index, tool := range schema.Tools {
		_, err := toolschema.Parse(tool.Parameters, generationToolArgumentOptions)
		var unsupported *toolschema.UnsupportedKeywordError
		if err != nil && !errors.As(err, &unsupported) {
			return toolParametersError("tools", index, tool.Name, err)
		}
	}
	for index, tool := range schema.AgentTools {
		parsed, err := toolschema.Parse(tool.Parameters, AgentToolArgumentOptions)
		if err != nil {
			return toolParametersError("chat.agent_tools", index, tool.Name, err)
		}
		// The notes tool's length is the Schema's bound, so the model sees the
		// limit the service enforces instead of a refusal after the fact.
		if tool.Execution.ImplementationKey == "session.notes" && schema.Context.MaximumSessionNotesCharacters > 0 {
			notes, declared := parsed.Properties["notes"]
			if !declared || notes.MaxLength == nil || *notes.MaxLength != schema.Context.MaximumSessionNotesCharacters {
				return fmt.Errorf("%s.notes 的 maxLength 必须等于 context.maximum_session_notes_characters（%d）",
					tool.Name, schema.Context.MaximumSessionNotesCharacters)
			}
		}
	}
	return validateThreeViewToolParameters(schema)
}

// validateThreeViewToolParameters ties create_three_view's arguments to the
// three-view declaration: its purpose enum is exactly the declared purposes,
// and the four-view switch the Skills name is a boolean (#665).
func validateThreeViewToolParameters(schema ChatSchema) error {
	var tool *ChatAgentToolSchema
	for index := range schema.AgentTools {
		if schema.AgentTools[index].Execution.ImplementationKey == "skill.prepare" {
			tool = &schema.AgentTools[index]
		}
	}
	if tool == nil {
		return nil
	}
	parsed, err := toolschema.Parse(tool.Parameters, AgentToolArgumentOptions)
	if err != nil {
		return err
	}
	// A document without declared purposes (the pre-#409 Catalog) may still
	// carry the property; the service then ignores it.
	purpose, declared := parsed.Properties["purpose"]
	if len(schema.ThreeView.Purposes) > 0 {
		if !declared {
			return fmt.Errorf("%s 必须声明 purpose 参数", tool.Name)
		}
		var want []string
		for _, entry := range schema.ThreeView.Purposes {
			want = append(want, entry.ID)
		}
		var got []string
		for _, value := range purpose.Enum {
			text, isString := value.(string)
			if !isString {
				return fmt.Errorf("%s.purpose 的取值必须是字符串", tool.Name)
			}
			got = append(got, text)
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			return fmt.Errorf("%s.purpose 的 enum %v 必须与 three_view.purposes %v 一致", tool.Name, got, want)
		}
	}
	// include_right (the four-view switch) is optional, so a Catalog written
	// before the four-view Skill still loads; declared, it is a boolean.
	if right, declared := parsed.Properties["include_right"]; declared && !slices.Equal(right.Types, []string{"boolean"}) {
		return fmt.Errorf("%s.include_right（四视图）必须是布尔参数", tool.Name)
	}
	for _, name := range []string{"reference_image_indices", "reference_handles"} {
		if reference, declared := parsed.Properties[name]; declared &&
			(reference.MaxItems == nil || *reference.MaxItems != 1) {
			return fmt.Errorf("%s.%s 只接受一张图，必须声明 maxItems: 1", tool.Name, name)
		}
	}
	return nil
}

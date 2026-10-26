package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestChatDomainModelsKeepsOnlyBoundedStringChoices(t *testing.T) {
	t.Parallel()
	body := map[string]json.RawMessage{
		"configuredModels": json.RawMessage(`{"image":"a","model-3d":"b","bad":7,"empty":"","nested":{"x":1}}`),
	}
	got := chatDomainModels(body, "configuredModels")
	if len(got) != 2 || got["image"] != "a" || got["model-3d"] != "b" {
		t.Fatalf("domain models = %#v", got)
	}
	if chatDomainModels(body, "") != nil || chatDomainModels(body, "missing") != nil {
		t.Fatal("an undeclared or absent field must yield no choices")
	}
	if chatDomainModels(map[string]json.RawMessage{"f": json.RawMessage(`["x"]`)}, "f") != nil {
		t.Fatal("a non-object value must yield no choices")
	}
	var many []string
	for index := 0; index < 30; index++ {
		many = append(many, fmt.Sprintf(`"d%d":"m"`, index))
	}
	bounded := chatDomainModels(map[string]json.RawMessage{"f": json.RawMessage("{" + strings.Join(many, ",") + "}")}, "f")
	if len(bounded) != maximumDomainModels {
		t.Fatalf("choices must be bounded, got %d", len(bounded))
	}
}

package agenttools

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

// #665: the three-view tool's recovery texts are the Schema's, not Go
// literals: a stopped turn and arguments of the wrong shape both answer with
// the declared wording before anything is prepared.
func TestThreeViewRecoveryTextsComeFromTheSchema(t *testing.T) {
	runs, err := skillrun.NewService(noExistingThreeViewRun{}, unusedThreeViewRecipes{})
	if err != nil {
		t.Fatal(err)
	}
	messages := catalog.ChatThreeViewMessages{
		ReferenceRequired: "configured one image",
		TurnStopped:       "configured stopped",
		ArgumentsInvalid:  "configured shape hint",
	}
	for name, test := range map[string]struct {
		arguments string
		stopped   bool
		want      string
	}{
		"stopped turn":     {`{}`, true, messages.TurnStopped},
		"wrongly shaped":   {`{"purpose":5}`, false, messages.ArgumentsInvalid},
		"not an object":    {`[1]`, false, messages.ArgumentsInvalid},
		"reference needed": {`{}`, false, messages.ReferenceRequired},
	} {
		t.Run(name, func(t *testing.T) {
			stopped := test.stopped
			executor := &Executor{
				skillRuntime: &skillrun.Runtime{Runs: runs}, skillReferences: unusedThreeViewReferences{},
				skillOfferings: referenceSelectionOfferings{},
				skillTurnStopped: func(context.Context, string, string, string) (bool, error) {
					return stopped, nil
				},
			}
			executor.SetThreeView(catalog.ChatThreeViewSchema{Messages: messages})
			command := chat.ToolCommand{
				OwnerID: "owner", SessionID: "session", AssistantMessageID: "turn",
				Call:    agents.ToolCall{ID: "call", Name: ToolCreateThreeView, Arguments: []byte(test.arguments)},
				Options: chat.TurnOptions{CreativeDomain: "image", AIModel: "selected-image-model"},
			}
			result, err := executor.createThreeView(context.Background(), command, nil)
			if err != nil || !result.NoSubmission || result.Error != "" || result.AgentContent != test.want {
				t.Fatalf("result=%+v error=%v, want %q", result, err, test.want)
			}
		})
	}
}

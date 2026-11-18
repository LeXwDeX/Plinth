package chat

// The executable remedy of a failed purpose check (#330 step 4). The mapping
// comes from chat.model_review.inspect.production.fixes: each failed check has
// one declared fix, and one of its option's capabilities fixes it. Only the
// capabilities application.step_policy.counted_capabilities counts are 3D
// steps; a fix that needs only local processing (UV unwrap, precise resize,
// format conversion, print repair) or a regenerate-only path is not offered
// with a step count, because it would read as "0 步".

import (
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// SelfCheckRemedy is one failed check with an executable fix that costs at
// least one counted 3D step. Capabilities are the fix option's capability ids
// in declaration order (an option may mix counted and local ones); Steps counts
// only the capabilities the step policy counts.
type SelfCheckRemedy struct {
	Check        string
	Capabilities []string
	Steps        int
}

// SelfCheckRemedies maps failed purpose checks to the declared fixes. A check
// without a fix, a fix without options, or an option whose capabilities are
// all outside the counted set yields no remedy. The first option with a counted
// capability is the offered route; checks are returned in the order they were
// failed, without duplicates.
func SelfCheckRemedies(
	fixes []catalog.ChatModelInspectFixSchema,
	failed []string,
	counted []schemacontract.Capability,
) []SelfCheckRemedy {
	if len(fixes) == 0 || len(failed) == 0 {
		return nil
	}
	byCheck := make(map[string]catalog.ChatModelInspectFixSchema, len(fixes))
	for _, fix := range fixes {
		byCheck[fix.Check] = fix
	}
	countedSet := make(map[string]bool, len(counted))
	for _, capability := range counted {
		countedSet[string(capability)] = true
	}
	seen := make(map[string]bool, len(failed))
	remedies := make([]SelfCheckRemedy, 0, len(failed))
	for _, check := range failed {
		check = strings.TrimSpace(check)
		if check == "" || seen[check] {
			continue
		}
		seen[check] = true
		fix, declared := byCheck[check]
		if !declared {
			continue
		}
		for _, option := range fix.Options {
			capabilities := make([]string, 0, len(option.Capabilities))
			steps := 0
			for _, candidate := range option.Capabilities {
				capabilities = append(capabilities, candidate.Capability)
				if countedSet[candidate.Capability] {
					steps++
				}
			}
			if steps == 0 {
				continue
			}
			remedies = append(remedies, SelfCheckRemedy{
				Check:        check,
				Capabilities: capabilities,
				Steps:        steps,
			})
			break
		}
	}
	return remedies
}

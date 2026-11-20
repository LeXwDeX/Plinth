package chat

import (
	"slices"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// RetiredModelSwitch records that a turn named a retired generation model
// for Domain and the service moved it to the successor (#329 follow-up). The
// generation step of that domain tells the Agent so (result model_note).
type RetiredModelSwitch struct {
	Domain   string `json:"Domain"`
	From     string `json:"From"`
	FromName string `json:"FromName"`
	To       string `json:"To"`
	ToName   string `json:"ToName"`
}

func retiredModelIndex(entries []catalog.RetiredModelSuccessor) map[string]catalog.RetiredModelSuccessor {
	if len(entries) == 0 {
		return nil
	}
	index := make(map[string]catalog.RetiredModelSuccessor, len(entries))
	for _, entry := range entries {
		index[entry.ModelID] = entry
	}
	return index
}

// currentModel is the model a pick stands for today: the successor of a
// retired model, otherwise the pick itself.
func (service *Service) currentModel(model string) string {
	if entry, retired := service.retiredModels[model]; retired {
		return entry.SuccessorModelID
	}
	return model
}

// moveRetiredModels moves the turn's picks of retired models to their
// successors, the domain in view (AIModel) and every other domain
// (DomainModels), and records each switch once per domain. The request's
// own map is not changed.
//
// The session's explicit locks (ExplicitModel3D, ExplicitModelImage), frozen
// into the options from the stored session before this runs, move the same
// way and stay locks (#380); a lock left on a retired model would make every
// tool of its domain refuse. Only the turn's copy changes: the session row is
// the person's own click and is not rewritten (a restored or replaced pick is
// not either), so each turn moves it again, and a lock switch is not recorded
// in RetiredModels, which would repeat its note on every turn. The index holds
// only models that have a successor, so a model without one, in a lock as in
// a pick, is left as it is.
func (service *Service) moveRetiredModels(options *TurnOptions) {
	options.RetiredModels = nil
	if len(service.retiredModels) == 0 {
		return
	}
	recorded := map[string]bool{}
	record := func(domain, model string) {
		entry, retired := service.retiredModels[model]
		if !retired || domain == "" || recorded[domain] {
			return
		}
		recorded[domain] = true
		options.RetiredModels = append(options.RetiredModels, RetiredModelSwitch{
			Domain: domain, From: entry.ModelID, FromName: entry.ModelName,
			To: entry.SuccessorModelID, ToName: entry.SuccessorModelName,
		})
	}
	record(options.CreativeDomain, options.AIModel)
	options.AIModel = service.currentModel(options.AIModel)
	options.ExplicitModel3D = service.currentModel(options.ExplicitModel3D)
	options.ExplicitModelImage = service.currentModel(options.ExplicitModelImage)
	if len(options.DomainModels) == 0 {
		return
	}
	domains := make([]string, 0, len(options.DomainModels))
	for domain := range options.DomainModels {
		domains = append(domains, domain)
	}
	slices.Sort(domains)
	moved := make(map[string]string, len(options.DomainModels))
	for _, domain := range domains {
		model := options.DomainModels[domain]
		record(domain, model)
		moved[domain] = service.currentModel(model)
	}
	options.DomainModels = moved
}

// RetiredModelFor is the switch the turn recorded for a domain, if any.
func (options TurnOptions) RetiredModelFor(domain string) (RetiredModelSwitch, bool) {
	if domain == "" {
		domain = options.CreativeDomain
	}
	for _, switched := range options.RetiredModels {
		if switched.Domain == domain {
			return switched, true
		}
	}
	return RetiredModelSwitch{}, false
}

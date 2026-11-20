package openairesponses

import "encoding/json"

// The reasoning items of a reply travel back to the model through the Port's
// ReasoningContent, which the orchestrator copies from a Completion onto the
// assistant message of the same tool loop (a string it never reads, shows or
// stores). With store false the encrypted items are the only way the model's
// own reasoning reaches its next round; the request asks for them with
// include reasoning.encrypted_content.
//
// Observed on 2026-11-20 at effort xhigh (medium and high returned none): the
// reasoning item comes first, at output_index 0, with content and summary as
// empty arrays, an id, encrypted_content and no status; the item of
// response.output_item.added already carries a shorter encrypted_content, the
// one of response.output_item.done the whole one, and the reasoning item of
// the output of response.completed a third value of the done one's length; no
// summary event comes when no summary was requested. The done item's token,
// sent back without the item's id (type, summary and encrypted_content only),
// ahead of the function_call it led to, was accepted; so was a round with no
// reasoning item at all (the model then reasons again). Hence what is kept
// and replayed: the done item (the output of response.completed only fills a
// gap), in that three-member form (replayReasoning).
//
// ReasoningContent holds a JSON list of those items, each with the number of
// function calls of the same reply that came before it, so that a reasoning
// item that came between two calls goes back between them (assistantItems).
// Only an item with encrypted_content is kept: without it nothing a later
// request could send would stand for it. The content is opaque to every
// caller, and a value that is not this list (the field of another Adapter's
// reply, an empty string) is ignored.
type carriedReasoning struct {
	Before int             `json:"before"`
	Item   json.RawMessage `json:"item"`
}

// encodeReasoning is the ReasoningContent for the items; "" for none.
func encodeReasoning(items []carriedReasoning) string {
	if len(items) == 0 {
		return ""
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// decodeReasoning reads a ReasoningContent back, in order, keeping only the
// entries that are reasoning items.
func decodeReasoning(content string) []carriedReasoning {
	if content == "" {
		return nil
	}
	var carried []carriedReasoning
	if json.Unmarshal([]byte(content), &carried) != nil {
		return nil
	}
	kept := carried[:0]
	for _, entry := range carried {
		var header struct {
			Type string `json:"type"`
		}
		if entry.Before >= 0 && json.Unmarshal(entry.Item, &header) == nil && header.Type == "reasoning" {
			kept = append(kept, entry)
		}
	}
	return kept
}

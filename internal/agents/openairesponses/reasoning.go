package openairesponses

import "encoding/json"

// The reasoning items of a reply travel back to the model through the Port's
// ReasoningContent, which the orchestrator copies from a Completion onto the
// assistant message of the same tool loop (a string it never reads, shows or
// stores). With store false the encrypted items are the only way the model's
// own reasoning reaches its next round; the request asks for them with
// include reasoning.encrypted_content. Per the documents, not yet seen in a
// real sample: the probe's requests at effort medium and high returned no
// reasoning item.
//
// ReasoningContent holds a JSON list of the items exactly as the upstream
// returned them, each with the number of function calls of the same reply that
// came before it, so that a reasoning item that came between two calls goes
// back between them (assistantItems). Only an item with encrypted_content is
// kept: without it nothing a later request could send would stand for it. The
// content is opaque to every caller, and a value that is not this list (the
// field of another Adapter's reply, an empty string) is ignored.
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

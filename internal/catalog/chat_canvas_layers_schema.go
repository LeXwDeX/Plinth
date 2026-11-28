package catalog

import (
	"fmt"
	"strings"
)

// ChatCanvasLayersSchema declares the read-only board reading of #709: the
// view_canvas tool, the @canvasN handles of the board snapshot frozen
// with a turn that is not a canvas turn, the one context line that says the
// board holds layers, and every text the Agent reads about the board. Server
// side only; the browser never reads it.
//
// The board stays out of a turn by default (#644). The snapshot only lets the
// Agent look when the conversation carries no material at all; the tool
// refuses on a canvas turn, on a turn that already carries pictures and on a
// second call of the same reply.
type ChatCanvasLayersSchema struct {
	// ToolName is the declared control tool (view_canvas). Empty turns
	// the board reading off: no snapshot is frozen and no line is added.
	ToolName string `json:"tool_name,omitempty"`
	// HandlePrefix is the board layer handle prefix ("@canvas"). Each readable
	// picture layer of the frozen snapshot gets the prefix and its 1-based
	// position among the readable pictures, top layer first.
	HandlePrefix string `json:"handle_prefix,omitempty"`
	// LayerLimit bounds the layers frozen with a turn and listed by the tool;
	// TextCharacters bounds the text and name kept per layer.
	LayerLimit     int `json:"layer_limit,omitempty"`
	TextCharacters int `json:"text_characters,omitempty"`
	// ContextTemplate ({count} {tool}) is the one line of a turn that is not a
	// canvas turn while the board holds layers.
	ContextTemplate string `json:"context_template,omitempty"`
	// EmptyMessage answers the tool when the board holds nothing.
	EmptyMessage string `json:"empty_message,omitempty"`
	// CanvasTurnMessage answers the tool on a canvas turn (the snapshot is
	// already part of the turn); TurnMaterialMessage on a turn that carries
	// pictures or a selection; RepeatedCallMessage on a second call of the
	// same reply.
	CanvasTurnMessage   string `json:"canvas_turn_message,omitempty"`
	TurnMaterialMessage string `json:"turn_material_message,omitempty"`
	RepeatedCallMessage string `json:"repeated_call_message,omitempty"`
	// TruncatedTemplate ({shown} {total}) is the statistics line of a board
	// with more layers than LayerLimit.
	TruncatedTemplate string `json:"truncated_template,omitempty"`
	// UnreadableNote marks a picture layer whose picture did not verify for
	// the owner and session: it is listed without a handle.
	UnreadableNote string `json:"unreadable_note,omitempty"`
	// ResultNote reminds the Agent, in every listing, to say it looked at the
	// board and to use the handles as ordinary references.
	ResultNote string `json:"result_note,omitempty"`
	// UnknownHandleTemplate ({handle} {tool}) refuses an @canvasN handle the
	// turn's snapshot does not hold.
	UnknownHandleTemplate string `json:"unknown_handle_template,omitempty"`
	// SourceNoteTemplate ({handles}) is the approval-card and result note of a
	// generation whose references include board layers.
	SourceNoteTemplate string `json:"source_note_template,omitempty"`
	// SummaryTemplate ({layers} {pictures}) is the one-line summary kept with
	// the tool call.
	SummaryTemplate string `json:"summary_template,omitempty"`
	// PreviewReason is the code the listing returns with "preview": null: the
	// flattened board preview is step 2 of #709 and is not available yet.
	PreviewReason string `json:"preview_reason,omitempty"`
	// PreviewNote tells the Agent there is no flattened preview in this
	// listing and how to look at a single layer instead.
	PreviewNote string `json:"preview_note,omitempty"`
	// ContinueOnCanvasLabel is the label of the choice-card option that turns
	// the next turn into a canvas turn ("continue on the board"); the tool
	// description asks for it whenever the board holds several layers or any
	// annotation.
	ContinueOnCanvasLabel string `json:"continue_on_canvas_label,omitempty"`
}

// Enabled reports whether the Schema declares the board reading.
func (schema ChatCanvasLayersSchema) Enabled() bool {
	return strings.TrimSpace(schema.ToolName) != "" && strings.TrimSpace(schema.HandlePrefix) != "" &&
		schema.LayerLimit > 0
}

// validateCanvasLayers checks the board reading declaration (#709): the tool
// it names is declared, its handle prefix cannot be read as another handle,
// the bounds are sane and every text the Agent reads is present with its
// placeholders.
func validateCanvasLayers(schema ChatSchema) error {
	board := schema.CanvasLayers
	if strings.TrimSpace(board.ToolName) == "" {
		return nil
	}
	if !board.Enabled() {
		return fmt.Errorf("chat.canvas_layers 需要 handle_prefix 与正的 layer_limit")
	}
	declared := false
	for _, tool := range schema.Tools {
		declared = declared || tool.Name == board.ToolName
	}
	for _, tool := range schema.AgentTools {
		declared = declared || tool.Name == board.ToolName
	}
	if !declared {
		return fmt.Errorf("chat.canvas_layers.tool_name %q 未在 chat.tools 或 chat.agent_tools 中声明", board.ToolName)
	}
	if !strings.HasPrefix(board.HandlePrefix, "@") {
		return fmt.Errorf("chat.canvas_layers.handle_prefix 必须以 @ 开头")
	}
	for _, prefix := range schema.Context.HandlePrefixes {
		if strings.HasPrefix(board.HandlePrefix, prefix) || strings.HasPrefix(prefix, board.HandlePrefix) {
			return fmt.Errorf("chat.canvas_layers.handle_prefix 与附件句柄前缀 %q 冲突", prefix)
		}
	}
	if board.LayerLimit > 200 || board.TextCharacters < 0 || board.TextCharacters > 2000 {
		return fmt.Errorf("chat.canvas_layers.layer_limit 须在 1..200、text_characters 须在 0..2000")
	}
	required := map[string][]string{
		"context_template":         {"{count}", "{tool}"},
		"empty_message":            nil,
		"canvas_turn_message":      nil,
		"turn_material_message":    nil,
		"repeated_call_message":    nil,
		"truncated_template":       {"{shown}", "{total}"},
		"unreadable_note":          nil,
		"result_note":              nil,
		"unknown_handle_template":  {"{handle}", "{tool}"},
		"source_note_template":     {"{handles}"},
		"summary_template":         {"{layers}", "{pictures}"},
		"preview_reason":           nil,
		"preview_note":             nil,
		"continue_on_canvas_label": nil,
	}
	texts := map[string]string{
		"context_template":         board.ContextTemplate,
		"empty_message":            board.EmptyMessage,
		"canvas_turn_message":      board.CanvasTurnMessage,
		"turn_material_message":    board.TurnMaterialMessage,
		"repeated_call_message":    board.RepeatedCallMessage,
		"truncated_template":       board.TruncatedTemplate,
		"unreadable_note":          board.UnreadableNote,
		"result_note":              board.ResultNote,
		"unknown_handle_template":  board.UnknownHandleTemplate,
		"source_note_template":     board.SourceNoteTemplate,
		"summary_template":         board.SummaryTemplate,
		"preview_reason":           board.PreviewReason,
		"preview_note":             board.PreviewNote,
		"continue_on_canvas_label": board.ContinueOnCanvasLabel,
	}
	for name, placeholders := range required {
		text := texts[name]
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("chat.canvas_layers.%s 不能为空", name)
		}
		for _, placeholder := range placeholders {
			if !strings.Contains(text, placeholder) {
				return fmt.Errorf("chat.canvas_layers.%s 必须包含 %s", name, placeholder)
			}
		}
	}
	return nil
}

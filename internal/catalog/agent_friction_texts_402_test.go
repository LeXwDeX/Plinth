package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #402: two places where the Agent's own words cost a round.
//
// The canvas and the brief's frame. The canvas size is the person's decision and
// a canvas turn already follows it in code (generationtool/canvas_size.go). The
// Agent still asked the person to change the canvas when the purpose's frame
// (the brief's "画幅 16:9") differed, because the canvas note taught it to send
// the person back to the canvas and the brief wording read as a requirement.
// The rule is Schema wording: the frame is a suggestion, a different canvas is
// followed and said in one sentence, and only the person's explicit request for
// another size points at the canvas.
//
// The reply language. Nothing told the Agent which language to answer in, so a
// Chinese conversation got English replies.

func TestCanvasNoteLeavesTheSizeToTheCanvas_402(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	note := product.Application().Chat.Context.SelectionCanvasNote
	for _, want := range []string{
		"输出尺寸跟随画板",
		"画板尺寸是用户的决定",
		"画幅只是建议",
		"按画板出图",
		"一句话说明",
		"不要要求用户改画板",
		"只有用户明确要求换尺寸时",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the canvas note lacks %q:\n%s", want, note)
		}
	}
	// The old rule sent the person back to the canvas whenever a size was wanted.
	if strings.Contains(note, "用户想换尺寸时，请其先调整画板尺寸") {
		t.Errorf("the canvas note still asks the person to adjust the canvas first:\n%s", note)
	}
	// Pointing at the canvas is allowed only behind the person's own request.
	if at, bound := strings.Index(note, "调整画板"), strings.Index(note, "只有用户明确要求换尺寸时"); at >= 0 && (bound < 0 || at < bound) {
		t.Errorf("the canvas is pointed at before the person asked for another size:\n%s", note)
	}
}

func TestBriefFrameIsWordedAsASuggestion_402(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := product.Application().Chat
	template := chat.Image2DUsage.RequirementsNote.AspectRatioTemplate
	if !strings.Contains(template, "建议") || !strings.Contains(template, "{value}") {
		t.Errorf("the purpose's frame reads as a requirement: %q", template)
	}
	// The 2D craft text says what to do when the person's size differs.
	var craft string
	for _, prompt := range chat.Agent.CraftPrompts {
		if prompt.ID == "image2d_production" {
			craft = prompt.Text
		}
	}
	if craft == "" {
		t.Fatal("the 2D production craft text is not declared")
	}
	for _, want := range []string{
		"画幅只是用途的建议",
		"用户的画板尺寸或明确给出的尺寸是用户的决定",
		"不要要求用户改画板",
		// The earlier rule about the person's other choices stays.
		"用途推荐与用户所选冲突时先确认",
	} {
		if !strings.Contains(craft, want) {
			t.Errorf("the 2D craft text lacks %q", want)
		}
	}
}

func TestSystemPromptSetsTheReplyLanguage_402(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	prompt := product.Application().Chat.Agent.SystemPrompt
	for _, want := range []string{
		"用用户最近一条消息所用的语言",
		"工具名、参数名、模型名保持原文",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	// The rule is about the words the person reads: the prompt written into a
	// tool call keeps its own rule.
	if !strings.Contains(prompt, "工具参数中的 prompt 用英文") {
		t.Error("the system prompt lost the rule that tool prompts are written in English")
	}
	// It is stated once, so a later edit cannot leave two versions apart.
	if count := strings.Count(prompt, "用用户最近一条消息所用的语言"); count != 1 {
		t.Errorf("the reply language rule appears %d times, want once", count)
	}
}

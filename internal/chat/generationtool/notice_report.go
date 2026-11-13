package generationtool

// Notice transparency (#284): a Task can succeed and still not deliver
// everything it was asked for (a batch of pictures of which some were not
// matted). The tool result carries the Task's public notices, each with the
// Schema's sentence in the turn's locale, so the Agent says what happened
// instead of reporting a finished batch.
import (
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// reportedNotice is one entry of the notices list in a tool result. Message is
// the Schema's sentence in the turn's locale; it names the pictures the notice
// is about, which Inputs also lists as 1-based positions in the order the
// step submitted them. Message is empty only when the Schema has no text for
// the code (or the executor has no localizer), and the Agent then words the
// code and positions itself.
type reportedNotice struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Inputs  []int  `json:"inputs,omitempty"`
}

// noticeReport lists the notices of a succeeded Task for the tools that
// declare the result key; nil when the tool declares none or the Task has no
// notice, so a result without the key says the Task delivered everything.
func (executor *Executor) noticeReport(command chat.ToolCommand, task generation.Task) []reportedNotice {
	if command.Tool.ResultFields.Notices == "" || len(task.Notices) == 0 {
		return nil
	}
	report := make([]reportedNotice, 0, len(task.Notices))
	for _, notice := range task.Notices {
		report = append(report, reportedNotice{
			Code:    notice.Code,
			Message: executor.texts.TaskNoticeText(command.Options.Locale, notice.Code, notice.Inputs),
			Inputs:  notice.Inputs,
		})
	}
	return report
}

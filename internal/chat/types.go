package chat

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/harness/resolve"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type TurnOptions struct {
	// Harness is read from the authenticated session when a new turn starts.
	// It stays frozen across restarts. Nil denotes a historical turn or a
	// service without Harness assembly; recovery must never replace it.
	Harness               *resolve.Snapshot `json:"Harness,omitempty"`
	CreativeDomain        string
	AspectRatio           string
	PoseMode              string
	AIModel               string
	Size                  string
	ImageSize             string
	Parameters            map[string]json.RawMessage
	SkillID               string
	SkillName             string
	ReferenceImageIndices []int
	// DomainModels is the model the user picked in each workspace domain. The
	// workspace domain in view (CreativeDomain) is only a hint; a tool of the
	// other domain uses the model chosen for its own domain.
	DomainModels map[string]string `json:"DomainModels,omitempty"`
	// RetiredModels records the picks of retired generation models the
	// service moved to their successors when the turn started (#329
	// follow-up); AIModel and DomainModels already name the successors.
	RetiredModels []RetiredModelSwitch `json:"RetiredModels,omitempty"`
	// Selection is the objects the user had selected on the workspace when the
	// turn was sent. The server verified each one for the owner and session
	// and gave it a handle; the Agent reads it as the "current selection".
	Selection []Attachment `json:"Selection,omitempty"`
	// SelectionPicked marks a Selection that is the session's picked candidate
	// ("选用") rather than something the client selected on the workspace.
	SelectionPicked bool `json:"SelectionPicked,omitempty"`
	// Locale is the UI locale of the request that started the turn, frozen
	// so the reply, the title and the server's own messages in the turn use
	// it across restarts. Empty is the default locale.
	Locale string `json:"Locale,omitempty"`
	// InputSource is the source the person chose for the turn, verified and
	// frozen by the service (#266). The canvas value means Selection holds the
	// artboard snapshot taken at send time; generation tools that read images
	// then use it whatever the Agent named. Empty: no declared source.
	InputSource string `json:"InputSource,omitempty"`
	// AgentScope is the session's Agent scope when the turn started. The
	// service reads it from the stored session, never from the request, and the
	// turn keeps it across restarts: the tool list, the system context and the
	// server-side tool check of the whole turn use this one value. Empty (a
	// turn stored before scopes existed) means the Schema default.
	AgentScope string `json:"AgentScope,omitempty"`
	// Model and ReasoningEffort are the chat model and the thinking level of
	// the turn, frozen by the service from the stored session when the turn
	// starts and kept across restarts (#322). Every model request of the turn
	// uses them. Empty Model (a turn stored before #322) is the deployment's
	// model; empty ReasoningEffort sends the model profile's default.
	Model           string `json:"Model,omitempty"`
	ReasoningEffort string `json:"ReasoningEffort,omitempty"`
	// ModelConnection is the connection that serves Model (#322 Scope 5);
	// empty (a turn stored before it, or a deployment with one model) is the
	// deployment's own connection.
	ModelConnection string `json:"ModelConnection,omitempty"`
}

type Turn struct {
	OwnerID            string
	SessionID          string
	AssistantMessageID string
	Options            TurnOptions
	State              string
	StopRequested      bool
	Checkpoint         TurnCheckpoint
	CheckpointVersion  int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// TurnCheckpoint is the durable continuation of one assistant tool round.
// Request already contains completed earlier rounds; Completed contains only
// this round's tool replies, in call order. A waiting call is never completed.
type TurnCheckpoint struct {
	Configuration *AgentConfigurationSnapshot `json:"configuration,omitempty"`
	Round         int                         `json:"round"`
	Assistant     Message                     `json:"assistant"`
	Request       agents.Request              `json:"request"`
	Completion    agents.Completion           `json:"completion"`
	Completed     []agents.Message            `json:"completed,omitempty"`
	ImageURLs     []string                    `json:"image_urls,omitempty"`
	WaitingTaskID string                      `json:"waiting_task_id,omitempty"`
	WaitingCallID string                      `json:"waiting_call_id,omitempty"`
	WaitDeadline  time.Time                   `json:"wait_deadline,omitempty"`
	HadToolError  bool                        `json:"had_tool_error,omitempty"`
}

type TurnPageCursor struct {
	CreatedAt          time.Time
	AssistantMessageID string
}

type Session struct {
	ID        string
	OwnerID   string
	OwnerName string
	Title     string
	// TitleSource is TitleSourcePending, TitleSourceAuto or TitleSourceUser.
	// It is storage state for the automatic summary and is not projected.
	TitleSource string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Messages    int
	// ApprovalMode is the session's paid-step approval mode ("auto"/"manual").
	ApprovalMode string
	// AgentScope is the session's Agent scope ("2d", "3d" or "auto"): which
	// tools the Agent may use. It is stored with the session.
	AgentScope string
	// EstimatedCredits is the sum of the estimated credits of the tasks this
	// session has accepted. It is an estimate, never a settled charge.
	EstimatedCredits int
	PriceTotals      []schema.NativeAmount
}

// Where a session title came from. Only a pending title may be replaced by
// the automatic summary; a title a person set is never overwritten.
const (
	TitleSourcePending = "pending"
	TitleSourceAuto    = "auto"
	TitleSourceUser    = "user"
)

type SessionListQuery struct {
	Search string
	Cursor string
	Limit  int
}

type SessionPage struct {
	Sessions   []Session
	NextCursor string
	// Total is the number of the owner's sessions matching Search, independent
	// of Cursor and Limit. It answers how many sessions the list has, not how
	// many were loaded.
	Total int
}

// MessageListQuery asks for the newest Limit messages of a session that come
// before Before (an opaque cursor from an earlier page; empty means the newest).
type MessageListQuery struct {
	Before string
	Limit  int
}

// MessagePage holds one page of messages in conversation order (oldest first).
// NextCursor reads the messages before this page; it is empty when there are
// none.
type MessagePage struct {
	Messages   []Message
	NextCursor string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments []byte
	TaskID    string
	// IdempotencyKey is frozen when the call is planned. Stop recovery uses it
	// even if the tool or its Offering later disappears from Product Schema.
	IdempotencyKey string
	// EstimatedCredits is the estimated credits of the accepted step (0 when
	// unknown). ApprovalSource says who approved it: "policy" or "human".
	EstimatedCredits int
	NativeQuote      *schema.NativeQuote
	ApprovalSource   string
}

type Message struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	ImageURLs []string
	ToolCalls []ToolCall
	// Attachments describe the images and models this message shows or sends
	// (platform IDs, lineage and the stable handle the Agent uses).
	Attachments []Attachment
	CreatedAt   time.Time
}

type Image struct {
	URL    string
	TaskID string
}

// FrontendMessage 是稳定语义投影；旧 HTTP 字段名由 Product Schema 在
// transport 层映射，避免把历史 camelCase 字段重新固化为 Go 类型协议。
type FrontendMessage struct {
	ID                    string
	Role                  string
	Content               string
	Images                []Image
	IsPending             bool
	PendingTaskTypes      []string
	CreatedAtMilliseconds int64
}

type ProjectionPolicy struct {
	PendingContent   string
	ToolCapabilities map[string]string
}

type Event struct {
	ID        int64
	SessionID string
	Data      map[string]any
	CreatedAt time.Time
}

type Stream struct {
	AssistantMessageID string
	SessionID          string
	Status             string
	CleanupAt          *time.Time
}

type Subscription struct {
	Replay []Event
	Events <-chan Event
	Status string
	close  func()
	once   sync.Once
}

func (subscription *Subscription) Close() {
	if subscription != nil {
		subscription.once.Do(func() {
			if subscription.close != nil {
				subscription.close()
			}
		})
	}
}

// ModelFor returns the model the user chose for a workspace domain. The
// domain in view uses AIModel (the long-standing field); another domain uses
// its entry of DomainModels. An empty result means the user chose none there.
func (options TurnOptions) ModelFor(domain string) string {
	if domain == "" || options.CreativeDomain == "" || domain == options.CreativeDomain {
		return options.AIModel
	}
	return options.DomainModels[domain]
}

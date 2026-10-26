// Package pendingapproval keeps an Agent's approval request alive after the
// turn that made it has ended.
//
// A turn that waited for a human decision long enough (the Schema's consent
// wait) ends with its ExecutionGrant still pending and its operation marked
// detached. From then on this service owns the request: it projects the
// session's pending approvals, applies the human decision, submits the frozen
// PreparedInput exactly once through the public task kernel, and posts the
// outcome into the same chat session as an assistant message. A grant that
// waited past the Schema's expiry can no longer be approved; it leaves the
// pending list at once and the sweep posts a message that it expired.
//
// Stopping the turn that created an approval does not touch it once its card
// was detached from the turn. If the stop wins the race against the detach, the
// sweep denies the grant as the system and posts a cancellation message, so a
// card never ends as a silent dead one.
package pendingapproval

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

var (
	// ErrUnavailable means the approval can no longer be used: its turn was
	// deleted, or its Offering changed or was disabled.
	ErrUnavailable = errors.New("pending approval: unavailable")
	// ErrSessionNotFound means the session is not the owner's.
	ErrSessionNotFound = errors.New("pending approval: session not found")
)

// Status of a listed approval.
const (
	StatusPending = "pending"
	StatusExpired = "expired"
)

// ApprovalSourceHuman names a human decision on a posted tool call.
const approvalSourceHuman = "human"

// Gateway is the public task kernel the service submits through.
type Gateway interface {
	Offerings(generation.Capability) []generation.Offering
	OfferingAvailable(generation.OfferingID) bool
	GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error)
	SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error)
	Get(context.Context, generation.OwnerID, generation.TaskID) (generation.Task, error)
}

// Chat is the part of the chat service the approvals use.
type Chat interface {
	OwnsSession(ctx context.Context, owner, session string) (bool, error)
	ListMessages(ctx context.Context, owner, session string) ([]chat.Message, error)
	TurnStopRequested(ctx context.Context, owner, session, assistantMessageID string) (bool, error)
	PostAssistantMessage(ctx context.Context, owner string, message chat.Message) (chat.Message, bool, error)
	UpdateMessage(ctx context.Context, owner string, message chat.Message) error
	AttachResults(ctx context.Context, owner string, message *chat.Message, modelTasks map[string]bool)
}

// Operations is the durable operation store, including the detached part.
type Operations interface {
	generationtool.DetachedOperationStore
}

// Config wires the service.
type Config struct {
	// VerifyPreparedSource rechecks an original frozen Harness scope before
	// approval and before detached submission. Nonzero Harness bindings require it.
	VerifyPreparedSource func(context.Context, generationtool.Operation, generation.PreparedInput) error
	Operations           Operations
	Grants               *executiongrant.Service
	Gateway              Gateway
	Chat                 Chat
	Schema               catalog.ChatSchema
	Clock                func() time.Time
	// Texts gives the copy of the UI locale frozen with the turn that asked
	// for the approval. Nil keeps the Schema's default-locale copy.
	Texts *catalog.TextLocalizer
	// DefaultLocale is the Schema's default language. A turn frozen before
	// locales existed has an empty locale; it must not inherit the request
	// language of whatever sweep happens to pick it up.
	DefaultLocale string
}

// Service is safe for concurrent use.
type Service struct {
	verifyPreparedSource func(context.Context, generationtool.Operation, generation.PreparedInput) error
	operations           Operations
	grants               *executiongrant.Service
	gateway              Gateway
	chat                 Chat
	schema               catalog.ChatSchema
	clock                func() time.Time
	tools                map[string]catalog.ChatToolSchema
	texts                *catalog.TextLocalizer
	defaultLocale        string

	locks [64]sync.Mutex
	// The cursors page the background sweep through decided-but-unsettled
	// operations, through waiting ones (to mark the expired) and through
	// stopped ones that were never detached (to cancel them).
	cursor, waitingCursor, stoppedCursor int64
}

// New validates the dependencies.
func New(config Config) (*Service, error) {
	if config.Operations == nil || config.Grants == nil || config.Grants.Grants == nil ||
		config.Gateway == nil || config.Chat == nil {
		return nil, errors.New("pending approval: incomplete dependencies")
	}
	if len(config.Schema.Harness.TaskSourceModes) > 0 && config.VerifyPreparedSource == nil {
		return nil, errors.New("pending approval: Harness source verifier unavailable")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	tools := make(map[string]catalog.ChatToolSchema, len(config.Schema.Tools))
	for _, tool := range config.Schema.Tools {
		tools[tool.Name] = tool
	}
	return &Service{
		verifyPreparedSource: config.VerifyPreparedSource, operations: config.Operations, grants: config.Grants, gateway: config.Gateway,
		chat: config.Chat, schema: config.Schema, clock: config.Clock, tools: tools, texts: config.Texts,
		defaultLocale: config.DefaultLocale,
	}, nil
}

// turnReader is the part of the chat service that knows a turn's frozen
// options; fakes without it keep the default locale.
type turnReader interface {
	GetTurn(ctx context.Context, ownerID, sessionID, assistantID string) (chat.Turn, error)
}

type defaultLocaleContextKey struct{}

func withDefaultLocale(ctx context.Context) context.Context {
	return context.WithValue(ctx, defaultLocaleContextKey{}, true)
}

func defaultLocaleRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(defaultLocaleContextKey{}).(bool)
	return requested
}

// inTurnLocale carries the UI locale of the turn that asked for the approval,
// so what the service posts into the conversation, even from the background
// sweep, reads in the language the person used for that turn.
func (s *Service) inTurnLocale(ctx context.Context, op generationtool.Operation) context.Context {
	reader, ok := s.chat.(turnReader)
	if !ok {
		return ctx
	}
	turn, err := reader.GetTurn(ctx, op.OwnerID, op.SessionID, op.AssistantMessageID)
	if err != nil {
		// A failed read cannot freeze the turn's locale. Never inherit the
		// request/background language.
		return s.schemaDefaultLocale(ctx)
	}
	locale := turn.Options.Locale
	if locale == "" {
		// An empty frozen locale is the Schema default, not the request's
		// language (a background sweep may carry any Accept-Language).
		locale = s.defaultLocale
	}
	if locale == "" {
		return s.schemaDefaultLocale(ctx)
	}
	return catalog.WithLocale(ctx, locale)
}

func (s *Service) schemaDefaultLocale(ctx context.Context) context.Context {
	// No default locale was wired (a legacy test or caller). Keep the
	// Schema's own copy rather than the request's language.
	if s.defaultLocale == "" {
		return withDefaultLocale(ctx)
	}
	return catalog.WithLocale(ctx, s.defaultLocale)
}

func (s *Service) text(ctx context.Context, text string) string {
	if defaultLocaleRequested(ctx) {
		return s.texts.Text("", text)
	}
	return s.texts.TextIn(ctx, text)
}

// Input is one input the approval would send to the model: a reference image
// or the source model. Handle is the session handle the user and the Agent
// already know it by, when the session shows it.
type Input struct {
	Parameter string `json:"parameter"`
	// Kind is InputAsset for a picture or file the step sends and InputTask for
	// the earlier task (a 3D model) the step works on.
	Kind    string `json:"kind,omitempty"`
	Role    string `json:"role,omitempty"`
	AssetID string `json:"asset_id,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Handle  string `json:"handle,omitempty"`
}

// Kinds of approval input.
const (
	InputAsset = "asset"
	InputTask  = "task"
)

// Item is the public projection of one pending approval.
type Item struct {
	NativeQuote          *generation.NativeQuote          `json:"native_quote,omitempty"`
	ParameterAdjustments []generation.ParameterAdjustment `json:"parameter_adjustments,omitempty"`
	RequestID            string                           `json:"request_id"`
	Status               string                           `json:"status"`
	Tool                 string                           `json:"tool"`
	Capability           string                           `json:"capability"`
	Model                string                           `json:"model,omitempty"`
	Provider             string                           `json:"provider,omitempty"`
	// ProviderID is the provider's account key, which the client matches to the
	// provider's balance.
	ProviderID string `json:"provider_id,omitempty"`
	// EstimatedCredits is the total the step is estimated to cost: the base
	// price plus ExtraCredits. BaseCredits and ExtraCredits split it when the
	// step spends extra credits (extra export formats, billed options);
	// ExtraLabel names what they pay for and ExtraItems lists the chosen
	// options by display name.
	EstimatedCredits int      `json:"estimated_credits"`
	BaseCredits      int      `json:"base_credits,omitempty"`
	ExtraCredits     int      `json:"extra_credits,omitempty"`
	ExtraLabel       string   `json:"extra_label,omitempty"`
	ExtraItems       []string `json:"extra_items,omitempty"`
	Prompt           string   `json:"prompt,omitempty"`
	// RechargeTaskID is set when the step repeats an earlier attempt of the
	// session that was charged upstream but whose result was not stored
	// (#294): the card then says approving charges again.
	RechargeTaskID string `json:"recharge_task_id,omitempty"`
	// ReferenceNote says, in the reader's language, that the step uses only
	// the first of the reference pictures the turn gave because the model
	// takes fewer (#329 E); the inputs list only the pictures it uses.
	ReferenceNote string    `json:"reference_note,omitempty"`
	Inputs        []Input   `json:"inputs"`
	MessageID     string    `json:"message_id"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Outcome is what a decision produced. TaskID and MessageID are empty while
// the work was not submitted or posted yet; the background sweep completes it.
type Outcome struct {
	RequestID string
	Status    executiongrant.Status
	TaskID    string
	MessageID string
}

func (s *Service) lock(requestID string) *sync.Mutex {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(requestID))
	return &s.locks[hash.Sum32()%uint32(len(s.locks))]
}

// MessageID is the deterministic ID of the message that reports a request, so
// posting it twice cannot create two.
func MessageID(requestID string) string { return "pa_" + requestID }

// DetachedSession names the session of the owner's detached Agent operation
// that this grant belongs to; ok is false when it is not one. It has no side
// effect.
func (s *Service) DetachedSession(ctx context.Context, owner, requestID string) (session string, ok bool, err error) {
	op, err := s.operations.LoadAgentOperationByGrant(ctx, owner, requestID)
	if errors.Is(err, generationtool.ErrOperationNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return op.SessionID, op.Detached, nil
}

// List projects the session's pending approvals, newest first. Each belongs to
// the owner's session. An approval past its expiry is not listed: it neither
// shows nor counts, and does not use up one of MaximumListed. (A card the page
// already shows turns "expired" by its own expires_at; the sweep posts the
// message that it expired.)
func (s *Service) List(ctx context.Context, owner, session string) ([]Item, error) {
	owned, err := s.chat.OwnsSession(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, ErrSessionNotFound
	}
	ops, err := s.operations.ListPendingDetached(ctx, owner, session,
		s.schema.PendingApprovals.MaximumListed, s.grants.ExpiryCutoff())
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(ops))
	if len(ops) == 0 {
		return items, nil
	}
	messages, err := s.chat.ListMessages(ctx, owner, session)
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		grant, err := s.grants.Grants.GetExecutionGrant(ctx, owner, op.GrantRequestID)
		if err != nil {
			return nil, err
		}
		if !generationtool.AgentGrantMatchesOperation(grant, op) || grant.Status != executiongrant.Pending ||
			s.grants.Expired(grant) {
			continue
		}
		prepared, err := s.gateway.GetPreparedInput(ctx, generation.OwnerID(owner), op.PreparedInputID)
		if err != nil {
			return nil, err
		}
		if prepared.InputHash != op.PreparedInputHash || prepared.SessionID != session {
			continue
		}
		items = append(items, s.item(ctx, owner, op, grant, prepared, messages))
	}
	return items, nil
}

func (s *Service) item(ctx context.Context, owner string, op generationtool.Operation, grant executiongrant.Grant,
	prepared generation.PreparedInput, messages []chat.Message) Item {
	status := StatusPending
	if s.grants.Expired(grant) {
		status = StatusExpired
	}
	item := Item{
		NativeQuote: prepared.NativeQuote, ParameterAdjustments: prepared.ParameterAdjustments,
		RequestID: grant.RequestID, Status: status, Tool: op.ToolName,
		Capability: string(prepared.Capability), MessageID: op.AssistantMessageID,
		CreatedAt: grant.PendingSince(), ExpiresAt: s.grants.ExpiresAt(grant),
		RechargeTaskID: op.RechargeTaskID, Inputs: []Input{},
	}
	if offering, ok := s.offering(prepared.Capability, op.OfferingID, op.OfferingRevision); ok {
		item.Model = offering.ModelName
		if item.Model == "" {
			item.Model = offering.ModelID
		}
		item.Provider = offering.ProviderName
		item.ProviderID = offering.ProviderID
		item.ReferenceNote = s.referenceNote(ctx, op, offering)
		if prepared.NativeQuote == nil {
			item.EstimatedCredits = offering.EstimateTotalCredits(prepared.NormalizedInput)
		}
		if extra := offering.ExtraCreditDetail(prepared.NormalizedInput); extra.Credits > 0 {
			item.ExtraCredits, item.ExtraLabel, item.ExtraItems = extra.Credits, extra.Label, extra.Items
			item.BaseCredits = item.EstimatedCredits - extra.Credits
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(prepared.NormalizedInput, &values) == nil {
			for _, parameter := range offering.Parameters {
				if parameter.Semantic == generation.SemanticPrompt {
					_ = json.Unmarshal(values[parameter.Name], &item.Prompt)
				}
			}
		}
	}
	for _, asset := range prepared.Assets {
		input := Input{Parameter: asset.Parameter, Kind: InputAsset, Role: asset.Role, AssetID: string(asset.ID)}
		if attachment, preview, ok := s.modelWithContent(ctx, owner, messages, asset.SHA256); ok {
			// A step on a local model file freezes a copy of the session's 3D model (#324):
			// the card names it as the model the step works on, by its handle and preview,
			// not as a picture (the copy has no picture to show).
			input.Kind, input.Handle, input.TaskID, input.AssetID = InputTask, attachment.Handle, attachment.TaskID, preview
		} else if attachment, ok := sessionAttachment(messages, func(a chat.Attachment) bool {
			return a.AssetID == string(asset.ID)
		}); ok {
			input.Handle, input.TaskID = attachment.Handle, attachment.TaskID
		} else if attachment, ok := s.attachmentWithContent(ctx, owner, messages, asset.SHA256); ok {
			// A picture picked on the stage is copied into an input asset when the step is
			// prepared, so only its bytes tie it back to the object the person chose.
			input.Handle, input.TaskID = attachment.Handle, attachment.TaskID
		}
		item.Inputs = append(item.Inputs, input)
	}
	for _, dependency := range prepared.Dependencies {
		input := Input{Parameter: dependency.Parameter, Kind: InputTask, TaskID: string(dependency.TaskID)}
		for _, output := range dependency.OutputAssets {
			if input.AssetID == "" || output.Role == "thumbnail" {
				input.AssetID, input.Role = string(output.ID), output.Role
			}
		}
		if attachment, ok := sessionAttachment(messages, func(a chat.Attachment) bool {
			return a.TaskID == string(dependency.TaskID)
		}); ok {
			input.Handle = attachment.Handle
		}
		item.Inputs = append(item.Inputs, input)
	}
	return item
}

// referenceNote is the Offering's reference-limit note in the locale ctx
// carries, for a step that uses fewer of the reference pictures than the turn
// gave (#329 E); "" otherwise and for operations created before the counts
// were kept.
func (s *Service) referenceNote(ctx context.Context, op generationtool.Operation, offering generation.Offering) string {
	return generationtool.ReferenceLimitNote(s.tools[op.ToolName], offering, op.ReferenceCount, op.ReferenceTotal,
		func(text string) string { return s.text(ctx, text) })
}

// maximumContentMatches bounds how many recent task pictures one approval looks through.
const maximumContentMatches = 24

// attachmentWithContent finds the latest session attachment whose task picture has the same
// bytes (SHA-256) as an input asset.
func (s *Service) attachmentWithContent(ctx context.Context, owner string, messages []chat.Message, sha256 string) (chat.Attachment, bool) {
	if sha256 == "" {
		return chat.Attachment{}, false
	}
	looked := 0
	for index := len(messages) - 1; index >= 0; index-- {
		for _, attachment := range messages[index].Attachments {
			if attachment.Handle == "" || attachment.TaskID == "" || attachment.AssetID == "" {
				continue
			}
			if looked++; looked > maximumContentMatches {
				return chat.Attachment{}, false
			}
			task, err := s.gateway.Get(ctx, generation.OwnerID(owner), generation.TaskID(attachment.TaskID))
			if err != nil {
				continue
			}
			for _, asset := range task.Assets {
				if string(asset.ID) == attachment.AssetID && asset.SHA256 == sha256 {
					return attachment, true
				}
			}
		}
	}
	return chat.Attachment{}, false
}

// modelWithContent finds the latest 3D model of the session whose model file has the same
// bytes (SHA-256) as an input asset, with the asset ID of its rendered preview ("" when it has
// none).
func (s *Service) modelWithContent(ctx context.Context, owner string, messages []chat.Message, sha256 string) (chat.Attachment, string, bool) {
	if sha256 == "" {
		return chat.Attachment{}, "", false
	}
	looked := 0
	for index := len(messages) - 1; index >= 0; index-- {
		for _, attachment := range messages[index].Attachments {
			if attachment.Handle == "" || attachment.TaskID == "" || attachment.Kind != chat.AttachmentKindModel {
				continue
			}
			if looked++; looked > maximumContentMatches {
				return chat.Attachment{}, "", false
			}
			task, err := s.gateway.Get(ctx, generation.OwnerID(owner), generation.TaskID(attachment.TaskID))
			if err != nil {
				continue
			}
			matched, preview := false, ""
			for _, asset := range task.Assets {
				if asset.Role == "thumbnail" {
					if preview == "" {
						preview = string(asset.ID)
					}
				} else if asset.SHA256 == sha256 {
					matched = true
				}
			}
			if matched {
				return attachment, preview, true
			}
		}
	}
	return chat.Attachment{}, "", false
}

// sessionAttachment finds the latest attachment of the session that matches.
func sessionAttachment(messages []chat.Message, match func(chat.Attachment) bool) (chat.Attachment, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		for _, attachment := range messages[index].Attachments {
			if attachment.Handle != "" && match(attachment) {
				return attachment, true
			}
		}
	}
	return chat.Attachment{}, false
}

func (s *Service) offering(capability generation.Capability, id generation.OfferingID, revision string) (generation.Offering, bool) {
	for _, offering := range s.gateway.Offerings(capability) {
		if offering.ID == id && offering.Revision == revision {
			return offering, true
		}
	}
	return generation.Offering{}, false
}

// Decide applies a human decision to a detached approval of the owner. It
// reports handled=false, with no effect, when the request is not one: the
// caller then decides it the ordinary way. An approval is submitted exactly
// once however often or concurrently it is approved, and an expired one is
// refused with executiongrant.ErrExpired.
func (s *Service) Decide(ctx context.Context, principal executiongrant.Principal, requestID string,
	decision executiongrant.Decision) (Outcome, bool, error) {
	op, err := s.operations.LoadAgentOperationByGrant(ctx, principal.OwnerID, requestID)
	if errors.Is(err, generationtool.ErrOperationNotFound) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	if !op.Detached {
		return Outcome{}, false, nil
	}
	mutex := s.lock(requestID)
	mutex.Lock()
	defer mutex.Unlock()
	grant, err := s.grants.Grants.GetExecutionGrant(ctx, principal.OwnerID, requestID)
	if err != nil {
		return Outcome{}, true, err
	}
	if !generationtool.AgentGrantMatchesOperation(grant, op) {
		return Outcome{}, true, executiongrant.ErrConflict
	}
	if grant.Status == executiongrant.Pending {
		if err := s.checkUsable(ctx, op, decision); err != nil {
			return Outcome{}, true, err
		}
	}
	grant, err = s.grants.Decide(ctx, principal, requestID, decision)
	if err != nil {
		return Outcome{}, true, err
	}
	// The decision is durable from here on. A transient failure below is
	// finished by the background sweep, so it is not the caller's failure.
	outcome, _ := s.continueLocked(ctx, op)
	outcome.RequestID, outcome.Status = grant.RequestID, grant.Status
	if outcome.TaskID == "" {
		outcome.TaskID = grant.TaskID
	}
	return outcome, true, nil
}

// checkUsable refuses a decision on an approval that can never run: its turn
// is gone, or (to approve) its Offering changed. A turn that was stopped after
// the card was detached does not matter: the card left the turn.
func (s *Service) checkUsable(ctx context.Context, op generationtool.Operation, decision executiongrant.Decision) error {
	if _, err := s.chat.TurnStopRequested(ctx, op.OwnerID, op.SessionID, op.AssistantMessageID); errors.Is(err, chat.ErrMessageNotFound) {
		return ErrUnavailable
	} else if err != nil {
		return err
	}
	if decision != executiongrant.Approve {
		return nil
	}
	prepared, err := s.gateway.GetPreparedInput(ctx, generation.OwnerID(op.OwnerID), op.PreparedInputID)
	if err != nil {
		return err
	}
	if _, ok := s.offering(prepared.Capability, op.OfferingID, op.OfferingRevision); !ok ||
		!s.gateway.OfferingAvailable(op.OfferingID) {
		return ErrUnavailable
	}
	return s.verifyHarnessPreparedSource(ctx, op, prepared)
}

func (s *Service) verifyHarnessPreparedSource(ctx context.Context, op generationtool.Operation, prepared generation.PreparedInput) error {
	if s.verifyPreparedSource == nil {
		if !op.Harness.IsZero() {
			return ErrUnavailable
		}
		return nil
	}
	return s.verifyPreparedSource(ctx, op, prepared)
}

// Continue drives one detached operation as far as its grant allows. It is
// idempotent and is what the background sweep calls.
func (s *Service) Continue(ctx context.Context, op generationtool.Operation) error {
	mutex := s.lock(op.GrantRequestID)
	mutex.Lock()
	defer mutex.Unlock()
	_, err := s.continueLocked(ctx, op)
	return err
}

func (s *Service) continueLocked(ctx context.Context, op generationtool.Operation) (Outcome, error) {
	var outcome Outcome
	stored, err := s.operations.LoadAgentOperationByGrant(ctx, op.OwnerID, op.GrantRequestID)
	if err != nil {
		return outcome, err
	}
	op = stored
	outcome.MessageID = ""
	if !op.Detached || op.Settled {
		return outcome, nil
	}
	grant, err := s.grants.Grants.GetExecutionGrant(ctx, op.OwnerID, op.GrantRequestID)
	if err != nil {
		return outcome, err
	}
	if !generationtool.AgentGrantMatchesOperation(grant, op) {
		return outcome, generationtool.ErrOperationConflict
	}
	switch grant.Status {
	case executiongrant.Denied:
		return s.settleDenied(ctx, op, grant)
	case executiongrant.Approved, executiongrant.Bound:
		return s.runApproved(ctx, op, grant)
	default:
		// Pending waits for a person; Claimed is being submitted right now.
		return outcome, nil
	}
}

// settleDenied posts the denial, or the cancellation when the system denied the
// grant because its turn was stopped before the card was handed over.
func (s *Service) settleDenied(ctx context.Context, op generationtool.Operation, grant executiongrant.Grant) (Outcome, error) {
	ctx = s.inTurnLocale(ctx, op)
	content := s.text(ctx, s.schema.PendingApprovals.Messages.Denied)
	switch {
	case grant.CancelledByStop():
		content = s.text(ctx, s.schema.PendingApprovals.Messages.Cancelled)
	default:
		if extra, ok := s.deniedExtraCredits(ctx, op); ok {
			content = extra
		}
	}
	return s.postAndSettle(ctx, op, content)
}

// deniedExtraCredits words the refusal of a step that also asked to spend extra
// credits: the step was not done, what the extra credits were for, and the
// alternative (the same step without them). ok is false for a step with no
// extra credits, whose refusal keeps the plain message.
func (s *Service) deniedExtraCredits(ctx context.Context, op generationtool.Operation) (string, bool) {
	prepared, err := s.gateway.GetPreparedInput(ctx, generation.OwnerID(op.OwnerID), op.PreparedInputID)
	if err != nil {
		return "", false
	}
	if prepared.NativeQuote != nil {
		q := prepared.NativeQuote
		extra, e := q.HasExtra()
		if e != nil || !extra {
			return "", false
		}
		o, found := s.offering(prepared.Capability, op.OfferingID, op.OfferingRevision)
		if !found {
			return "", false
		}
		template := s.schema.PendingApprovals.Messages.DeniedNativeExtra
		if template == "" {
			return "", false
		}
		items := []string{}
		for _, c := range q.Components {
			items = append(items, s.text(ctx, c.Label))
		}
		model := o.ModelName
		if model == "" {
			model = o.ModelID
		}
		separator := s.text(ctx, s.schema.PendingApprovals.Messages.ExtraItemsSeparator)
		// UnitLabel is Provider data and is never translated, even when the
		// dictionary contains an identical source text.
		return strings.NewReplacer("{model}", s.text(ctx, model), "{unit}", q.UnitLabel, "{items}", strings.Join(items, separator), "{extra}", q.ExtraAmount, "{base}", q.BaseAmount, "{total}", q.TotalAmount).Replace(s.text(ctx, template)), true
	}
	if prepared.ExtraCredits <= 0 {
		return "", false
	}
	offering, found := s.offering(prepared.Capability, op.OfferingID, op.OfferingRevision)
	if !found {
		return "", false
	}
	extra := offering.ExtraCreditDetail(prepared.NormalizedInput)
	if extra.Credits <= 0 {
		return "", false
	}
	total := offering.EstimateTotalCredits(prepared.NormalizedInput)
	model := offering.ModelName
	if model == "" {
		model = offering.ModelID
	}
	messages := s.schema.PendingApprovals.Messages
	items := make([]string, 0, len(extra.Items))
	for _, item := range extra.Items {
		items = append(items, s.text(ctx, item))
	}
	return strings.NewReplacer(
		"{model}", s.text(ctx, model),
		"{label}", s.text(ctx, extra.Label),
		"{items}", strings.Join(items, s.text(ctx, messages.ExtraItemsSeparator)),
		"{extra}", strconv.Itoa(extra.Credits),
		"{base}", strconv.Itoa(total-extra.Credits),
		"{total}", strconv.Itoa(total),
	).Replace(s.text(ctx, messages.DeniedExtraCredits)), true
}

// settleExpired posts that the approval expired without a decision.
func (s *Service) settleExpired(ctx context.Context, op generationtool.Operation) (Outcome, error) {
	ctx = s.inTurnLocale(ctx, op)
	return s.postAndSettle(ctx, op, s.text(ctx, s.schema.PendingApprovals.Messages.Expired))
}

// postAndSettle posts the one message that reports a request (a fixed ID makes
// a second post a no-op) and marks the operation settled.
func (s *Service) postAndSettle(ctx context.Context, op generationtool.Operation, content string) (Outcome, error) {
	messageID := MessageID(op.GrantRequestID)
	if _, _, err := s.chat.PostAssistantMessage(ctx, op.OwnerID, chat.Message{
		ID: messageID, SessionID: op.SessionID, Content: content,
	}); err != nil && !errors.Is(err, chat.ErrSessionNotFound) {
		return Outcome{}, err
	}
	if err := s.operations.SettleAgentOperation(ctx, op); err != nil {
		return Outcome{}, err
	}
	return Outcome{MessageID: messageID}, nil
}

func (s *Service) runApproved(ctx context.Context, op generationtool.Operation, grant executiongrant.Grant) (Outcome, error) {
	taskID := generation.TaskID(grant.TaskID)
	if grant.Status == executiongrant.Approved {
		prepared, err := s.gateway.GetPreparedInput(ctx, generation.OwnerID(op.OwnerID), op.PreparedInputID)
		if err != nil {
			if permanentSubmitFailure(err) {
				return s.settleSubmitFailed(ctx, op)
			}
			return Outcome{}, err
		}
		if err = s.verifyHarnessPreparedSource(ctx, op, prepared); err != nil {
			if errors.Is(err, harness.ErrStale) || errors.Is(err, generationtool.ErrOperationConflict) || errors.Is(err, ErrUnavailable) {
				return s.postAndSettle(ctx, op, s.text(s.inTurnLocale(ctx, op), s.schema.Harness.ChangedMessage))
			}
			return Outcome{}, err
		}
		accepted, err := s.gateway.SubmitPrepared(context.WithoutCancel(ctx), generation.SubmitPreparedCommand{
			OwnerID: generation.OwnerID(op.OwnerID), SessionID: op.SessionID,
			PreparedInputID: op.PreparedInputID, IdempotencyKey: op.IdempotencyKey,
			OperationID: op.OperationID, AuthorizationRef: op.GrantRequestID, Harness: op.Harness,
		})
		if err != nil {
			if errors.Is(err, harness.ErrStale) {
				return s.postAndSettle(ctx, op, s.text(s.inTurnLocale(ctx, op), s.schema.Harness.ChangedMessage))
			}
			if permanentSubmitFailure(err) {
				return s.settleSubmitFailed(ctx, op)
			}
			return Outcome{}, err
		}
		taskID = accepted.TaskID
	}
	task, err := s.gateway.Get(ctx, generation.OwnerID(op.OwnerID), taskID)
	if err != nil {
		return Outcome{TaskID: string(taskID)}, err
	}
	if task.OwnerID != generation.OwnerID(op.OwnerID) || task.SessionID != op.SessionID || task.ID != taskID {
		return Outcome{TaskID: string(taskID)}, generationtool.ErrOperationConflict
	}
	return s.report(ctx, op, task)
}

// permanentSubmitFailure tells a submit error that no later attempt can
// fix from one that a later sweep may: the frozen input is gone, invalid or
// no longer matches the catalog, the Offering is gone, or the grant can no
// longer be claimed (its turn was stopped). A kernel conflict is permanent
// too, since it means the frozen input, the Schema or the input assets
// changed under the approval; only the repository's own concurrent-update
// conflict and an overlapping active conversion (#330) are retries. The
// conversion refusal wrote no Task and claimed no grant, so the approval can
// stay pending until the other run ends.
func permanentSubmitFailure(err error) bool {
	if errors.Is(err, generation.ErrConversionInProgress) {
		return false
	}
	for _, code := range []generation.ErrorCode{
		generation.ErrInvalidCapability, generation.ErrInvalidInput,
		generation.ErrUnknownOffering, generation.ErrNoOffering,
		generation.ErrNotFound, generation.ErrForbidden,
	} {
		if generation.IsCode(err, code) {
			return true
		}
	}
	if generation.IsCode(err, generation.ErrConflict) && !errors.Is(err, generation.RepositoryConflict) {
		return true
	}
	return errors.Is(err, executiongrant.ErrState) || errors.Is(err, executiongrant.ErrConflict)
}

func (s *Service) settleSubmitFailed(ctx context.Context, op generationtool.Operation) (Outcome, error) {
	ctx = s.inTurnLocale(ctx, op)
	return s.postAndSettle(ctx, op, s.text(ctx, s.schema.PendingApprovals.Messages.SubmitFailed))
}

// report posts (once) and finalizes (once the task is terminal) the message
// that carries the task's handle and result.
func (s *Service) report(ctx context.Context, op generationtool.Operation, task generation.Task) (Outcome, error) {
	messageID := MessageID(op.GrantRequestID)
	outcome := Outcome{TaskID: string(task.ID), MessageID: messageID}
	messages, err := s.chat.ListMessages(ctx, op.OwnerID, op.SessionID)
	if errors.Is(err, chat.ErrSessionNotFound) {
		// The session is gone; nothing is left to report into.
		return outcome, s.operations.SettleAgentOperation(ctx, op)
	}
	if err != nil {
		return outcome, err
	}
	tool := s.tools[op.ToolName]
	ctx = s.inTurnLocale(ctx, op)
	model := s.text(ctx, task.Offering.ModelName)
	if model == "" {
		model = task.Offering.ModelID
	}
	posted, exists := chat.Message{}, false
	for _, message := range messages {
		if message.ID == messageID {
			posted, exists = message, true
		}
	}
	if !exists {
		posted, _, err = s.chat.PostAssistantMessage(ctx, op.OwnerID, chat.Message{
			ID: messageID, SessionID: op.SessionID,
			Content: strings.NewReplacer("{model}", model).Replace(s.text(ctx, s.schema.PendingApprovals.Messages.Started)),
			ToolCalls: []chat.ToolCall{{
				ID: op.CallID, Name: op.ToolName, Arguments: originalArguments(messages, op),
				TaskID: string(task.ID), NativeQuote: task.NativeQuote, EstimatedCredits: legacyTaskCredits(task),
				ApprovalSource: approvalSourceHuman,
			}},
		})
		if err != nil {
			if errors.Is(err, chat.ErrSessionNotFound) {
				return outcome, s.operations.SettleAgentOperation(ctx, op)
			}
			return outcome, err
		}
	}
	switch task.State {
	case generation.TaskSucceeded:
		posted.Content = strings.NewReplacer("{model}", model).Replace(s.text(ctx, s.schema.PendingApprovals.Messages.Succeeded))
		// The conversation keeps which pictures the model really used (#329 E),
		// in the current Schema text when the Offering is still listed: a task
		// frozen before the text changed says it as the card did (#329 note 99647).
		noteOffering := task.Offering
		if current, ok := s.offering(task.Offering.Capability, op.OfferingID, op.OfferingRevision); ok {
			noteOffering.ExecutionMessages = current.ExecutionMessages
		}
		if note, template := s.referenceNote(ctx, op, noteOffering), s.schema.PendingApprovals.Messages.SucceededWithNote; note != "" && template != "" {
			posted.Content = strings.NewReplacer("{model}", model, "{note}", note).Replace(s.text(ctx, template))
		}
		posted.ImageURLs = generationtool.ResultAssetURLs(s.schema, tool, task)
		modelTasks := map[string]bool{}
		if tool.ResultAttachmentKind == chat.AttachmentKindModel {
			modelTasks[string(task.ID)] = true
		}
		s.chat.AttachResults(ctx, op.OwnerID, &posted, modelTasks)
	case generation.TaskFailed, generation.TaskCanceled:
		posted.Content = s.text(ctx, generationtool.FailureMessage(s.schema, task.Offering.CreditsMetered, task.Failure))
	default:
		return outcome, nil
	}
	if err := s.chat.UpdateMessage(ctx, op.OwnerID, posted); err != nil && !errors.Is(err, chat.ErrMessageNotFound) {
		return outcome, err
	}
	return outcome, s.operations.SettleAgentOperation(ctx, op)
}

// originalArguments recovers the arguments of the tool call that asked for the
// approval, from the turn's own message. A message that is gone yields "{}".
func originalArguments(messages []chat.Message, op generationtool.Operation) []byte {
	for _, message := range messages {
		if message.ID != op.AssistantMessageID {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID == op.CallID && len(call.Arguments) > 0 {
				return call.Arguments
			}
		}
	}
	return []byte("{}")
}

// Sweep finishes one batch of work per kind and returns how many operations it
// settled:
//   - decided but unsettled operations: it submits an approval whose submit
//     never ran, posts denials, and finalizes the message of a finished task;
//   - waiting operations past their expiry: it posts that they expired;
//   - never-detached operations whose turn was stopped: it denies their grant
//     as the system and posts the cancellation.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	batch := s.schema.PendingApprovals.MonitorBatchSize
	var failures []error
	settled := 0
	run := func(list func(int64, int) ([]generationtool.Operation, error), cursor *int64,
		handle func(generationtool.Operation) error) {
		ops, err := list(*cursor, batch)
		if err != nil {
			failures = append(failures, err)
			return
		}
		if len(ops) < batch {
			*cursor = 0
		} else {
			*cursor = ops[len(ops)-1].Seq
		}
		for _, op := range ops {
			if ctx.Err() != nil {
				return
			}
			if err := handle(op); err != nil {
				failures = append(failures, err)
				continue
			}
			if after, err := s.operations.LoadAgentOperationByGrant(ctx, op.OwnerID, op.GrantRequestID); err == nil && after.Settled {
				settled++
			}
		}
	}
	run(func(after int64, limit int) ([]generationtool.Operation, error) {
		return s.operations.ListUnsettledDetached(ctx, after, limit)
	}, &s.cursor, func(op generationtool.Operation) error { return s.Continue(ctx, op) })
	run(func(after int64, limit int) ([]generationtool.Operation, error) {
		return s.operations.ListWaitingDetached(ctx, after, limit)
	}, &s.waitingCursor, func(op generationtool.Operation) error { return s.expire(ctx, op) })
	run(func(after int64, limit int) ([]generationtool.Operation, error) {
		return s.operations.ListStoppedUndetached(ctx, after, limit)
	}, &s.stoppedCursor, func(op generationtool.Operation) error { return s.cancelStopped(ctx, op) })
	return settled, errors.Join(failures...)
}

// expire posts the expiry of a waiting approval that nobody decided in time. A
// decision that committed first wins: the approval is then finished by the
// ordinary path.
func (s *Service) expire(ctx context.Context, op generationtool.Operation) error {
	mutex := s.lock(op.GrantRequestID)
	mutex.Lock()
	defer mutex.Unlock()
	stored, err := s.operations.LoadAgentOperationByGrant(ctx, op.OwnerID, op.GrantRequestID)
	if err != nil || !stored.Detached || stored.Settled {
		return err
	}
	grant, err := s.grants.Grants.GetExecutionGrant(ctx, stored.OwnerID, stored.GrantRequestID)
	if err != nil {
		return err
	}
	if !generationtool.AgentGrantMatchesOperation(grant, stored) {
		return generationtool.ErrOperationConflict
	}
	if !s.grants.Expired(grant) {
		return nil
	}
	_, err = s.settleExpired(ctx, stored)
	return err
}

// cancelStopped ends an approval whose turn was stopped before it was detached:
// nothing can approve it any more. The grant is denied as the system and the
// cancellation is posted.
func (s *Service) cancelStopped(ctx context.Context, op generationtool.Operation) error {
	mutex := s.lock(op.GrantRequestID)
	mutex.Lock()
	defer mutex.Unlock()
	cancelled, err := s.operations.CancelStoppedAgentOperation(ctx, op)
	if err != nil || !cancelled {
		return err
	}
	_, err = s.continueLocked(ctx, op)
	return err
}

// Run sweeps until the context ends. A failed sweep is retried at the next
// tick; the durable state makes every step repeatable.
func (s *Service) Run(ctx context.Context, onError func(error)) error {
	interval := time.Duration(s.schema.PendingApprovals.MonitorIntervalMilliseconds) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
		}
	}
}

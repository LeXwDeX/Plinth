// Package taskreroll starts a new task from the frozen input of a succeeded
// one (#242): the same step with fresh seeds, so the person can ask for "one
// more" without re-filling parameters.
//
// A reroll is not a back door around approval or idempotency. It replays the
// original frozen input, changes only the Schema-declared seed parameters, and
// runs the ordinary PrepareInput -> ExecutionGrant -> SubmitPrepared path. The
// session approval mode (or budget) still decides; the client must supply an
// Idempotency-Key, and repeating the same key returns the same Task.
package taskreroll

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var (
	// ErrNotFound: the task is not the caller's, or does not exist.
	ErrNotFound = errors.New("taskreroll: task not found")
	// ErrNotSucceeded: only a succeeded Task that is not bound to a plan or
	// skill-run step can be rerolled.
	ErrNotSucceeded = errors.New("taskreroll: task is not rerollable")
	// ErrNoFrozenInput: the Task has no bound PreparedInput to replay.
	ErrNoFrozenInput = errors.New("taskreroll: task has no frozen input")
	// ErrInProgress: this key's reroll is already being started or waits for a
	// decision prepared earlier.
	ErrInProgress = errors.New("taskreroll: reroll already in progress")
	// ErrDeclined: the person declined the reroll of this key.
	ErrDeclined = errors.New("taskreroll: reroll was declined")
	// ErrUnavailable: the Schema does not declare the reroll flow.
	ErrUnavailable = errors.New("taskreroll: reroll is not declared")
	// ErrInvalid: the request or the wiring is incomplete.
	ErrInvalid = errors.New("taskreroll: invalid request")
)

// Kernel is the task kernel as the reroll needs it.
type Kernel interface {
	GetScoped(context.Context, generation.TaskListScope, generation.TaskID) (generation.Task, error)
	LookupByIdempotency(context.Context, generation.OwnerID, string) (generation.Task, error)
	PrepareInput(context.Context, generation.PrepareInputCommand) (generation.PreparedInput, error)
	GetPreparedInput(context.Context, generation.OwnerID, generation.PreparedInputID) (generation.PreparedInput, error)
	SubmitPrepared(context.Context, generation.SubmitPreparedCommand) (generation.Accepted, error)
}

// Grants prepares an ExecutionGrant and applies the session approval mode.
type Grants interface {
	Prepare(context.Context, executiongrant.PrepareCommand) (executiongrant.Grant, error)
	ApplyPolicy(context.Context, executiongrant.Grant) (executiongrant.Grant, error)
}

// Origin is what the bound grant of a task records about its admission.
type Origin struct {
	// PreparedInputID is the frozen input the task was admitted with, "" for a
	// task without a bound grant.
	PreparedInputID string
	// PlanStepID is set when the task was one step of a processing plan or a
	// skill run. Such a task belongs to its plan or run, which owns the step's
	// state and result; a standalone reroll would be a paid task whose output
	// reaches neither.
	PlanStepID string
}

// Origins finds the admission of a task.
type Origins interface {
	PreparedInputOf(ctx context.Context, owner string, task string) (Origin, error)
	// GrantByKey returns the grant that carries an idempotency key, if any.
	GrantByKey(ctx context.Context, owner string, key string) (executiongrant.Grant, bool, error)
}

// Config wires the service. OperationID must be the identity the standalone
// task endpoint uses for a prepared input, so a decision made through it matches.
type Config struct {
	Kernel      Kernel
	Grants      Grants
	Origins     Origins
	OperationID func(generation.PreparedInputID) string
	// Schema is application.chat.reroll: the seed parameters and their bounds.
	Schema catalog.ChatRerollSchema
	// Seed chooses a new seed in [min, max]. Nil uses crypto/rand.
	Seed func(min, max int) int
}

type Service struct {
	config Config
	// guard serializes rerolls of one task and client key inside this process.
	guard sync.Map
}

func New(config Config) (*Service, error) {
	if config.Kernel == nil || config.Grants == nil || config.OperationID == nil ||
		!config.Schema.Enabled || config.Schema.MinSeed > config.Schema.MaxSeed {
		return nil, ErrInvalid
	}
	if config.Seed == nil {
		config.Seed = randomSeed
	}
	return &Service{config: config}, nil
}

// Result says what the reroll did.
type Result struct {
	// TaskID is set once the new task exists (created now or found again).
	TaskID   string
	Replayed bool
	// Waiting is set when a person must decide before the task is created.
	Waiting *Waiting
}

type Waiting struct {
	PreparedInputID string
	RequestID       string
	IdempotencyKey  string
}

// IdempotencyKey derives the key of one reroll request. The task and the
// client's key both participate, so two clients cannot collide and repeating
// the same request returns the same Task.
func IdempotencyKey(task generation.TaskID, clientKey string) string {
	return "reroll:" + string(task) + ":" + strings.TrimSpace(clientKey)
}

// Reroll starts the reroll of one succeeded task of the principal's owner.
func (s *Service) Reroll(ctx context.Context, principal executiongrant.Principal, id, clientKey string) (Result, error) {
	if !s.config.Schema.Enabled {
		return Result{}, ErrUnavailable
	}
	clientKey = strings.TrimSpace(clientKey)
	if principal.OwnerID == "" || principal.ActorID == "" || principal.Source != executiongrant.HumanBrowser ||
		strings.TrimSpace(id) == "" || clientKey == "" || len(clientKey) > 200 {
		return Result{}, ErrInvalid
	}
	owner := generation.OwnerID(principal.OwnerID)
	guard := guardKey(owner, id+"\x00"+clientKey)
	if _, busy := s.guard.LoadOrStore(guard, struct{}{}); busy {
		return Result{}, ErrInProgress
	}
	defer s.guard.Delete(guard)

	original, err := s.config.Kernel.GetScoped(ctx, generation.OwnTasks(owner), generation.TaskID(id))
	if err != nil {
		if isCode(err, generation.ErrNotFound) {
			return Result{}, ErrNotFound
		}
		return Result{}, err
	}
	if original.OwnerID != owner {
		return Result{}, ErrNotFound
	}
	if original.State != generation.TaskSucceeded || original.SessionID == "" {
		return Result{}, ErrNotSucceeded
	}
	origin, err := s.config.Origins.PreparedInputOf(ctx, principal.OwnerID, string(original.ID))
	if err != nil {
		return Result{}, err
	}
	if origin.PlanStepID != "" {
		return Result{}, ErrNotSucceeded
	}
	if origin.PreparedInputID == "" {
		return Result{}, ErrNoFrozenInput
	}
	key := IdempotencyKey(original.ID, clientKey)
	// A reroll that already created its task is the answer, whatever the
	// catalog looks like today.
	if existing, err := s.config.Kernel.LookupByIdempotency(ctx, owner, key); err == nil {
		return Result{TaskID: string(existing.ID), Replayed: true}, nil
	} else if !isCode(err, generation.ErrNotFound) {
		return Result{}, err
	}

	prepared, grant, err := s.admission(ctx, principal, original, origin, key)
	if err != nil {
		return Result{}, err
	}
	operation := s.config.OperationID(prepared.ID)
	grant, err = s.config.Grants.ApplyPolicy(ctx, grant)
	if err != nil {
		return Result{}, err
	}
	if grant.Status != executiongrant.Approved {
		return Result{Waiting: &Waiting{
			PreparedInputID: string(prepared.ID), RequestID: grant.RequestID, IdempotencyKey: key,
		}}, nil
	}
	accepted, err := s.config.Kernel.SubmitPrepared(ctx, generation.SubmitPreparedCommand{
		OwnerID: owner, SessionID: prepared.SessionID, PreparedInputID: prepared.ID,
		IdempotencyKey: key, OperationID: operation, AuthorizationRef: grant.RequestID,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{TaskID: string(accepted.TaskID), Replayed: accepted.Replayed}, nil
}

// admission returns the frozen input and grant of this reroll: the ones an
// earlier request already made, or fresh ones with fresh seeds.
func (s *Service) admission(ctx context.Context, principal executiongrant.Principal, original generation.Task,
	origin Origin, key string) (generation.PreparedInput, executiongrant.Grant, error) {
	owner := generation.OwnerID(principal.OwnerID)
	if earlier, found, err := s.config.Origins.GrantByKey(ctx, principal.OwnerID, key); err != nil {
		return generation.PreparedInput{}, executiongrant.Grant{}, err
	} else if found {
		switch earlier.Status {
		case executiongrant.Denied:
			return generation.PreparedInput{}, executiongrant.Grant{}, ErrDeclined
		case executiongrant.Pending, executiongrant.Approved:
			prepared, err := s.config.Kernel.GetPreparedInput(ctx, owner, generation.PreparedInputID(earlier.PreparedInputID))
			return prepared, earlier, err
		default:
			// Claimed or bound without a task is an admission in flight.
			return generation.PreparedInput{}, executiongrant.Grant{}, ErrInProgress
		}
	}
	frozen, err := s.config.Kernel.GetPreparedInput(ctx, owner, generation.PreparedInputID(origin.PreparedInputID))
	if err != nil {
		if isCode(err, generation.ErrNotFound) {
			return generation.PreparedInput{}, executiongrant.Grant{}, ErrNoFrozenInput
		}
		return generation.PreparedInput{}, executiongrant.Grant{}, err
	}
	input, err := RerollInput(frozen.NormalizedInput, s.config.Schema, s.config.Seed)
	if err != nil {
		return generation.PreparedInput{}, executiongrant.Grant{}, err
	}
	command := generation.PrepareInputCommand{
		OwnerID: owner, SessionID: frozen.SessionID, Capability: frozen.Capability,
		OfferingID: frozen.OfferingID, Input: input,
		SceneID: frozen.SceneID, ManifestID: frozen.ManifestID, ManifestHash: frozen.ManifestHash,
		SourceSceneObjectID: frozen.SourceSceneObjectID, SourceSceneRevision: frozen.SourceSceneRevision,
	}
	command.ParameterReasons = make(map[string]string, len(frozen.ParameterAdjustments))
	command.ParameterSources = make(map[string]string, len(frozen.ParameterAdjustments))
	for _, adjustment := range frozen.ParameterAdjustments {
		command.ParameterReasons[adjustment.Name] = adjustment.Reason
		if strings.HasPrefix(adjustment.Source, "profile:") {
			command.ParameterSources[adjustment.Name] = adjustment.Source
		}
	}
	prepared, err := s.config.Kernel.PrepareInput(ctx, command)
	if err != nil {
		return generation.PreparedInput{}, executiongrant.Grant{}, err
	}
	grant, err := s.config.Grants.Prepare(ctx, executiongrant.PrepareCommand{
		Principal: principal, SessionID: prepared.SessionID,
		OfferingID: string(prepared.OfferingID), OfferingRevision: prepared.OfferingRevision,
		PreparedInputID: string(prepared.ID), PreparedInputHash: prepared.InputHash,
		IdempotencyKey: key, OperationID: s.config.OperationID(prepared.ID),
	})
	if err != nil {
		if errors.Is(err, executiongrant.ErrConflict) {
			return generation.PreparedInput{}, executiongrant.Grant{}, ErrInProgress
		}
		return generation.PreparedInput{}, executiongrant.Grant{}, err
	}
	return prepared, grant, nil
}

// RerollInput replays one frozen input with every declared seed parameter
// replaced by a fresh value in the Schema's range. A parameter the frozen
// input does not carry (the Offering declares no such seed) is left alone, so
// the provider simply resamples.
func RerollInput(input json.RawMessage, schema catalog.ChatRerollSchema, seed func(min, max int) int) (json.RawMessage, error) {
	if !schema.Enabled {
		return nil, ErrUnavailable
	}
	var values map[string]json.RawMessage
	if len(input) == 0 || json.Unmarshal(input, &values) != nil || values == nil {
		return nil, ErrNoFrozenInput
	}
	for _, name := range schema.SeedParameters {
		raw, present := values[name]
		if !present {
			continue
		}
		var value int
		if json.Unmarshal(raw, &value) != nil {
			continue
		}
		values[name] = json.RawMessage(fmt.Sprintf("%d", seed(schema.MinSeed, schema.MaxSeed)))
	}
	return json.Marshal(values)
}

func randomSeed(min, max int) int {
	if max <= min {
		return min
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max-min)+1))
	if err != nil {
		return min
	}
	return min + int(value.Int64())
}

func guardKey(owner generation.OwnerID, id string) string { return string(owner) + "\x00" + id }

func isCode(err error, code generation.ErrorCode) bool {
	var failure *generation.Error
	return errors.As(err, &failure) && failure.Code == code
}

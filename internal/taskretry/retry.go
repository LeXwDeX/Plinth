// Package taskretry starts a new task from the frozen input of a failed one.
//
// A retry is not a back door around approval or idempotency. It runs the same
// kernel path as any other paid step: PrepareInput freezes the original task's
// input again, an ExecutionGrant is prepared and approved by the session's
// approval mode (or waits for a person), and SubmitPrepared creates the Task.
// The idempotency key is derived from the original task, so repeating the
// request can never create a second task for the same failure.
package taskretry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/executiongrant"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var (
	// ErrNotFound: the task is not the caller's, or does not exist.
	ErrNotFound = errors.New("taskretry: task not found")
	// ErrNotRetryable: the task did not fail, the failure is not retryable, or
	// the failure is ambiguous (the upstream may already have taken the job).
	ErrNotRetryable = errors.New("taskretry: task is not retryable")
	// ErrInProgress: this failure's retry is already being started or waits for
	// a decision that was prepared earlier.
	ErrInProgress = errors.New("taskretry: retry already in progress")
	// ErrDeclined: the person declined the retry of this failure. The key of a
	// declined retry cannot be reused, so this failure is not retried again.
	ErrDeclined = errors.New("taskretry: retry was declined")
	// ErrInvalid: the request or the wiring is incomplete.
	ErrInvalid = errors.New("taskretry: invalid request")
)

// Kernel is the task kernel as the retry needs it.
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
	// task without a bound grant (an older task).
	PreparedInputID string
	// PlanStepID is set when the task was one step of a processing plan or a
	// skill run. Such a task is retried through its plan or run, which owns
	// the step's state and delivers its result; a standalone retry would be a
	// paid task whose output reaches neither.
	PlanStepID string
}

// Origins finds the admission of a task, so the retry keeps its scene or
// render-manifest context and refuses a task that belongs to a plan step.
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
}

type Service struct {
	config Config
	// guard serializes retries of one failed task inside this process.
	guard sync.Map
}

func New(config Config) (*Service, error) {
	if config.Kernel == nil || config.Grants == nil || config.OperationID == nil {
		return nil, ErrInvalid
	}
	return &Service{config: config}, nil
}

// Result says what the retry did.
type Result struct {
	// TaskID is set once the new task exists (created now or found again).
	TaskID   string
	Replayed bool
	// Waiting is set when a person must decide before the task is created. The
	// caller finishes with the ordinary grant decision and task submit using
	// these values.
	Waiting *Waiting
}

type Waiting struct {
	PreparedInputID string
	RequestID       string
	IdempotencyKey  string
}

// IdempotencyKey derives the key of the retry of one task. It depends only on
// the failed task, so every request for the same failure shares it.
func IdempotencyKey(task generation.TaskID) string {
	sum := sha256.Sum256([]byte(task))
	return "retry-" + hex.EncodeToString(sum[:16])
}

// Retry starts the retry of one failed task of the principal's owner.
func (s *Service) Retry(ctx context.Context, principal executiongrant.Principal, id string) (Result, error) {
	if principal.OwnerID == "" || principal.ActorID == "" || principal.Source != executiongrant.HumanBrowser || strings.TrimSpace(id) == "" {
		return Result{}, ErrInvalid
	}
	owner := generation.OwnerID(principal.OwnerID)
	if _, busy := s.guard.LoadOrStore(guardKey(owner, id), struct{}{}); busy {
		return Result{}, ErrInProgress
	}
	defer s.guard.Delete(guardKey(owner, id))

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
	if original.State != generation.TaskFailed || original.Failure == nil ||
		!original.Failure.Retryable || original.Failure.Ambiguous || original.SessionID == "" {
		return Result{}, ErrNotRetryable
	}
	var origin Origin
	if s.config.Origins != nil {
		origin, err = s.config.Origins.PreparedInputOf(ctx, principal.OwnerID, string(original.ID))
		if err != nil {
			return Result{}, err
		}
		if origin.PlanStepID != "" {
			// A plan or skill-run step is retried by its plan or run only.
			return Result{}, ErrNotRetryable
		}
	}
	key := IdempotencyKey(original.ID)
	// A retry that already created its task is the answer, whatever the
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

// admission returns the frozen input and grant of this failure's retry: the
// ones an earlier request already made, or fresh ones.
func (s *Service) admission(ctx context.Context, principal executiongrant.Principal, original generation.Task, origin Origin, key string) (generation.PreparedInput, executiongrant.Grant, error) {
	owner := generation.OwnerID(principal.OwnerID)
	if s.config.Origins != nil {
		earlier, found, err := s.config.Origins.GrantByKey(ctx, principal.OwnerID, key)
		if err != nil {
			return generation.PreparedInput{}, executiongrant.Grant{}, err
		}
		if found {
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
	}
	command := generation.PrepareInputCommand{
		OwnerID: owner, SessionID: original.SessionID, Capability: original.Capability,
		OfferingID: original.Offering.ID, Input: original.Input,
	}
	if origin.PreparedInputID != "" {
		frozen, err := s.config.Kernel.GetPreparedInput(ctx, owner, generation.PreparedInputID(origin.PreparedInputID))
		if err != nil {
			return generation.PreparedInput{}, executiongrant.Grant{}, err
		}
		command.Capability, command.OfferingID, command.Input = frozen.Capability, frozen.OfferingID, frozen.NormalizedInput
		command.SceneID, command.ManifestID, command.ManifestHash = frozen.SceneID, frozen.ManifestID, frozen.ManifestHash
		command.SourceSceneObjectID, command.SourceSceneRevision = frozen.SourceSceneObjectID, frozen.SourceSceneRevision
		command.ParameterReasons = make(map[string]string, len(frozen.ParameterAdjustments))
		for _, adjustment := range frozen.ParameterAdjustments {
			command.ParameterReasons[adjustment.Name] = adjustment.Reason
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

func guardKey(owner generation.OwnerID, task string) string { return string(owner) + "\x00" + task }

func isCode(err error, code generation.ErrorCode) bool {
	var failure *generation.Error
	return errors.As(err, &failure) && failure.Code == code
}

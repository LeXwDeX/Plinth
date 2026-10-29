package app

// Everything in this file is linked only into `go test -c ./internal/app`.
// Ordinary binaries expose neither fault configuration nor a fault-control API.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/processing"
	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
	"github.com/LeXwDeX/Plinth/internal/providers/schemaimages"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Set with -ldflags '-X github.com/LeXwDeX/Plinth/internal/app.acceptanceSourceSHA=<full SHA>'.
var acceptanceSourceSHA string

const acceptanceAcknowledgement = "isolated-test-only"

type acceptanceManifest struct {
	OwnerID       generation.OwnerID   `json:"owner_id"`
	SourceSHA     string               `json:"source_sha"`
	Address       string               `json:"address"`
	WebAssetsRoot string               `json:"web_assets_root"`
	CatalogFile   string               `json:"catalog_file"`
	Scenarios     []acceptanceScenario `json:"scenarios"`
}

type acceptanceScenario struct {
	MaxProviderStarts int                        `json:"max_provider_starts"`
	ID                string                     `json:"id"`
	OwnerID           generation.OwnerID         `json:"owner_id"`
	SessionID         string                     `json:"session_id"`
	PlanID            string                     `json:"plan_id,omitempty"`
	StepIndex         int                        `json:"step_index"`
	Capability        generation.Capability      `json:"capability"`
	OfferingID        generation.OfferingID      `json:"offering_id"`
	IdempotencyKey    string                     `json:"idempotency_key,omitempty"`
	OfferingRevision  string                     `json:"offering_revision,omitempty"`
	PreparedInputID   generation.PreparedInputID `json:"prepared_input_id,omitempty"`
	PreparedInputHash string                     `json:"prepared_input_hash,omitempty"`
	Fault             string                     `json:"fault"`
}

func (s acceptanceScenario) key(retry int) string {
	if s.PlanID == "" {
		return s.IdempotencyKey
	}
	return fmt.Sprintf("processing:%s:%d:%d", s.PlanID, s.StepIndex, retry)
}

func (m acceptanceManifest) validate() error {
	sha, err := hex.DecodeString(m.SourceSHA)
	if err != nil || len(sha) != 20 {
		return errors.New("acceptance manifest requires a full source SHA")
	}
	host, port, err := net.SplitHostPort(m.Address)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || host != "127.0.0.1" || n < 1024 || n > 65535 || n == 13333 {
		return errors.New("acceptance server requires a dedicated loopback port")
	}
	if m.CatalogFile == "" || m.WebAssetsRoot == "" {
		return errors.New("acceptance manifest requires explicit catalog and frontend paths")
	}
	if m.OwnerID == "" {
		return errors.New("acceptance manifest requires its default owner")
	}
	seen := map[string]bool{}
	for _, s := range m.Scenarios {
		if s.MaxProviderStarts < 1 || s.MaxProviderStarts > 10 || s.ID == "" || seen[s.ID] || s.OwnerID != m.OwnerID || s.SessionID == "" || s.OfferingID == "" || !s.Capability.Valid() || s.StepIndex < 0 || s.StepIndex >= processing.MaxPlanSteps {
			return errors.New("acceptance scenario has an incomplete or duplicate identity")
		}
		seen[s.ID] = true
		switch s.Fault {
		case "rigging_start_failed", "submit_response_lost", "prepare_unavailable", "submit_before_acceptance", "none":
			planBytes, planErr := hex.DecodeString(s.PlanID)
			if planErr != nil || len(planBytes) != 32 {
				return errors.New("processing acceptance scenario requires its exact plan ID")
			}
		case "image_insufficient_quota":
			if s.Capability != generation.TextToImage && s.Capability != generation.ImageToImage {
				return errors.New("image wire fault requires an image capability")
			}
			if s.PlanID != "" || len(s.IdempotencyKey) < 16 || s.MaxProviderStarts != 1 {
				return errors.New("image acceptance scenario requires its exact task idempotency key")
			}
		case "standalone":
			inputHash, hashErr := hex.DecodeString(s.PreparedInputHash)
			if s.PlanID != "" || s.StepIndex != 0 || s.MaxProviderStarts != 1 || len(s.IdempotencyKey) < 16 || len(s.IdempotencyKey) > 200 || s.OfferingRevision == "" || s.PreparedInputID == "" || hashErr != nil || len(inputHash) != sha256.Size {
				return errors.New("standalone acceptance requires one start bound to its frozen input, revision and idempotency key")
			}
		default:
			return errors.New("unknown acceptance fault")
		}
	}
	return nil
}

type acceptanceController struct {
	root, manifestPath string
	manifest           acceptanceManifest
	mu                 sync.Mutex
	module             *generation.Module
	admissions         generation.TaskAdmissionStore
	plans              *processing.Service
	imageAdapters      map[string]generation.ProviderPort
}

func (c *acceptanceController) current() (acceptanceManifest, error) {
	data, err := os.ReadFile(c.manifestPath)
	if err != nil {
		return acceptanceManifest{}, err
	}
	var m acceptanceManifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, errors.New("invalid acceptance manifest")
	}
	if err := m.validate(); err != nil {
		return m, err
	}
	if m.OwnerID != c.manifest.OwnerID || m.SourceSHA != c.manifest.SourceSHA || m.Address != c.manifest.Address || m.CatalogFile != c.manifest.CatalogFile || m.WebAssetsRoot != c.manifest.WebAssetsRoot {
		return m, errors.New("running acceptance process identity cannot change")
	}
	return m, nil
}

func (c *acceptanceController) marker(s acceptanceScenario, kind string) string {
	identity := s.ID + ":" + string(s.OwnerID) + ":" + s.SessionID + ":" + s.PlanID + ":" + s.IdempotencyKey + ":" + string(s.OfferingID) + ":" + kind
	if s.PlanID == "" {
		// Renaming a scenario or editing its declared scope must never replenish
		// the one outbound Start budget for an already consumed owner/intent.
		encoded, _ := json.Marshal([]string{"standalone", string(s.OwnerID), s.IdempotencyKey, kind})
		identity = string(encoded)
	}
	hash := sha256.Sum256([]byte(identity))
	return filepath.Join(c.root, "markers", hex.EncodeToString(hash[:]))
}

// O_EXCL is the durable, cross-goroutine and cross-process consumption boundary.
// A crash after reservation fails closed; it never authorizes another injection.
func (c *acceptanceController) claim(s acceptanceScenario, kind string) (bool, error) {
	file, err := os.OpenFile(c.marker(s, kind), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	if _, err := file.WriteString(kind + "\n"); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	dir, err := os.Open(filepath.Join(c.root, "markers"))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	return true, dir.Sync()
}

func (c *acceptanceController) consumed(s acceptanceScenario, kind string) bool {
	_, err := os.Stat(c.marker(s, kind))
	return err == nil
}

type acceptanceEvent struct {
	At        time.Time            `json:"at"`
	Kind      string               `json:"kind"`
	Scenario  string               `json:"scenario"`
	TaskID    generation.TaskID    `json:"task_id,omitempty"`
	AttemptID generation.AttemptID `json:"attempt_id,omitempty"`
	Key       string               `json:"idempotency_key,omitempty"`
}

func (c *acceptanceController) record(kind string, s acceptanceScenario, task generation.TaskID, attempt generation.AttemptID, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	file, err := os.OpenFile(filepath.Join(c.root, "events.jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(acceptanceEvent{time.Now().UTC(), kind, s.ID, task, attempt, key}); err != nil {
		return err
	}
	return file.Sync()
}

func (c *acceptanceController) plan(ctx context.Context, s acceptanceScenario) (processing.Plan, processing.Step, error) {
	if c.plans == nil {
		return processing.Plan{}, processing.Step{}, errors.New("acceptance processing service is not assembled")
	}
	p, err := c.plans.Get(ctx, s.OwnerID, s.PlanID)
	if err != nil {
		return p, processing.Step{}, err
	}
	if p.OwnerID != s.OwnerID || p.SessionID != s.SessionID || p.ID != s.PlanID || s.StepIndex >= len(p.Steps) {
		return p, processing.Step{}, errors.New("acceptance plan identity mismatch")
	}
	step := p.Steps[s.StepIndex]
	if step.Capability != s.Capability || step.OfferingID != s.OfferingID {
		return p, step, errors.New("acceptance step identity mismatch")
	}
	return p, step, nil
}

type acceptanceGateway struct {
	processing.Gateway
	control *acceptanceController
}

func (g *acceptanceGateway) PrepareInput(ctx context.Context, cmd generation.PrepareInputCommand) (generation.PreparedInput, error) {
	m, err := g.control.current()
	if err != nil {
		return generation.PreparedInput{}, err
	}
	for _, s := range m.Scenarios {
		if s.Fault != "prepare_unavailable" || cmd.OwnerID != s.OwnerID || cmd.SessionID != s.SessionID || cmd.Capability != s.Capability || cmd.OfferingID != s.OfferingID {
			continue
		}
		p, step, err := g.control.plan(ctx, s)
		if err != nil || step.State != processing.Pending || step.PreparedInputID != "" {
			continue
		}
		// Preparation has no operation ID yet. Bind it to this persisted pending
		// step and its actual source Task, rather than session/capability alone.
		source := p.SourceTaskID
		if s.StepIndex > 0 {
			source = p.Steps[s.StepIndex-1].TaskID
		}
		var input map[string]any
		if json.Unmarshal(cmd.Input, &input) != nil || input["source_task"] != string(source) {
			continue
		}
		candidates, err := g.control.plans.ListBySource(ctx, s.OwnerID, s.SessionID, p.SourceTaskID)
		if err != nil {
			return generation.PreparedInput{}, err
		}
		matching := 0
		for _, candidate := range candidates {
			if candidate.State != processing.Pending && candidate.State != processing.Running {
				continue
			}
			for index, candidateStep := range candidate.Steps {
				if candidateStep.State == processing.Succeeded {
					continue
				}
				candidateSource := candidate.SourceTaskID
				if index > 0 {
					candidateSource = candidate.Steps[index-1].TaskID
				}
				if candidateStep.State == processing.Pending && candidateStep.Capability == cmd.Capability && candidateStep.OfferingID == cmd.OfferingID && candidateSource == source {
					matching++
				}
				break
			}
		}
		if matching != 1 {
			return generation.PreparedInput{}, errors.New("acceptance preparation scope is ambiguous")
		}
		claimed, err := g.control.claim(s, "prepare")
		if err != nil {
			return generation.PreparedInput{}, err
		}
		if claimed {
			if err := g.control.record("injected_prepare", s, "", "", s.key(step.Retry)); err != nil {
				return generation.PreparedInput{}, err
			}
			return generation.PreparedInput{}, errors.New("acceptance transient preparation failure")
		}
	}
	return g.Gateway.PrepareInput(ctx, cmd)
}

func (g *acceptanceGateway) SubmitPrepared(ctx context.Context, cmd generation.SubmitPreparedCommand) (generation.Accepted, error) {
	m, err := g.control.current()
	if err != nil {
		return generation.Accepted{}, err
	}
	var matched *acceptanceScenario
	for _, s := range m.Scenarios {
		if cmd.OwnerID != s.OwnerID || cmd.SessionID != s.SessionID || cmd.OperationID != s.PlanID || s.PlanID == "" {
			continue
		}
		_, step, err := g.control.plan(ctx, s)
		if err != nil {
			continue
		}
		if cmd.PlanStepID != fmt.Sprintf("%d:%d", s.StepIndex, step.Retry) || cmd.IdempotencyKey != s.key(step.Retry) || cmd.PreparedInputID != step.PreparedInputID || cmd.AuthorizationRef != step.AuthorizationRef {
			continue
		}
		copy := s
		matched = &copy
		break
	}
	if matched == nil {
		return g.Gateway.SubmitPrepared(ctx, cmd)
	}
	s := *matched
	if s.Fault == "submit_before_acceptance" {
		claimed, err := g.control.claim(s, "submit")
		if err != nil {
			return generation.Accepted{}, err
		}
		if claimed {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			// Exercise a real kernel storage failure before acceptance. The original
			// PreparedInput and Grant remain persisted, with no lifecycle mutation.
			accepted, err := g.Gateway.SubmitPrepared(canceled, cmd)
			if err == nil {
				return generation.Accepted{}, errors.New("acceptance canceled submission unexpectedly succeeded")
			}
			if recErr := g.control.record("kernel_before_acceptance", s, accepted.TaskID, "", cmd.IdempotencyKey); recErr != nil {
				return generation.Accepted{}, recErr
			}
			return accepted, err
		}
	}
	lose := false
	if s.Fault == "submit_response_lost" {
		lose, err = g.control.claim(s, "submit")
		if err != nil {
			return generation.Accepted{}, err
		}
	}
	accepted, err := g.Gateway.SubmitPrepared(ctx, cmd)
	if err == nil && !accepted.Replayed {
		if recErr := g.control.record("accepted_task", s, accepted.TaskID, "", cmd.IdempotencyKey); recErr != nil {
			return generation.Accepted{}, recErr
		}
	}
	if err == nil && lose {
		if recErr := g.control.record("lost_response", s, accepted.TaskID, "", cmd.IdempotencyKey); recErr != nil {
			return generation.Accepted{}, recErr
		}
		return generation.Accepted{}, errors.New("acceptance lost submission response")
	}
	return accepted, err
}

func (g *acceptanceGateway) LookupByIdempotency(ctx context.Context, owner generation.OwnerID, key string) (generation.Task, error) {
	m, err := g.control.current()
	if err != nil {
		return generation.Task{}, err
	}
	for _, s := range m.Scenarios {
		if owner != s.OwnerID || (s.Fault != "submit_response_lost" && s.Fault != "submit_before_acceptance") || !g.control.consumed(s, "submit") {
			continue
		}
		_, step, err := g.control.plan(ctx, s)
		if err != nil || key != s.key(step.Retry) {
			continue
		}
		claimed, err := g.control.claim(s, "lookup_conflict")
		if err != nil {
			return generation.Task{}, err
		}
		if claimed {
			if err := g.control.record("injected_lookup_conflict", s, "", "", key); err != nil {
				return generation.Task{}, err
			}
			return generation.Task{}, &generation.Error{Code: generation.ErrConflict, Operation: "acceptance_lookup"}
		}
	}
	task, err := g.Gateway.LookupByIdempotency(ctx, owner, key)
	if err == nil {
		for _, s := range m.Scenarios {
			if owner == s.OwnerID && task.SessionID == s.SessionID && key == task.IdempotencyKey {
				_ = g.control.record("lookup_task", s, task.ID, "", key)
				break
			}
		}
	}
	return task, err
}

type acceptanceRegistry struct {
	generation.ProviderRegistry
	control *acceptanceController
}

func (r acceptanceRegistry) Provider(offering generation.Offering) (generation.ProviderPort, bool) {
	port, ok := r.ProviderRegistry.Provider(offering)
	if override, exists := r.control.imageAdapters[offering.AdapterKey]; exists {
		port, ok = override, true
	}
	if !ok {
		return nil, false
	}
	switch p := port.(type) {
	case *meshy.Adapter:
		return &acceptanceMeshy{Adapter: p, control: r.control}, true
	case *tripo.Adapter:
		return &acceptanceTripo{Adapter: p, control: r.control}, true
	case *mock.Adapter:
		return &acceptanceMock{Adapter: p, control: r.control}, true
	default:
		return &acceptancePort{ProviderPort: port, control: r.control}, true
	}
}

// Embedding the concrete adapter preserves its optional dependency resolver,
// webhook decoder and deletion port; it does not pretend unsupported ports exist.
type acceptanceMeshy struct {
	*meshy.Adapter
	control *acceptanceController
}

func (p *acceptanceMeshy) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	return p.control.start(ctx, req, p.Adapter.Start)
}

func (p *acceptanceMeshy) Observe(ctx context.Context, req generation.ObserveRequest) (generation.Observation, error) {
	return p.control.observe(ctx, req, p.Adapter.Observe)
}

// Tripo validates owned bytes before admission and resumes private upload/create
// checkpoints. Keep both optional ports while bounding every mutating dispatch.
type acceptanceTripo struct {
	*tripo.Adapter
	control *acceptanceController
}

func (p *acceptanceTripo) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	return p.control.start(ctx, req, p.Adapter.Start)
}

func (p *acceptanceTripo) Observe(ctx context.Context, req generation.ObserveRequest) (generation.Observation, error) {
	return p.control.observe(ctx, req, p.Adapter.Observe)
}

func (p *acceptanceTripo) ResumePreparation(ctx context.Context, req generation.PreparationRequest) (generation.Observation, error) {
	return p.control.resumePreparation(ctx, req, p.Adapter.ResumePreparation)
}

type acceptanceMock struct {
	*mock.Adapter
	control *acceptanceController
}

func (p *acceptanceMock) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	return p.control.start(ctx, req, p.Adapter.Start)
}

func (p *acceptanceMock) Observe(ctx context.Context, req generation.ObserveRequest) (generation.Observation, error) {
	return p.control.observe(ctx, req, p.Adapter.Observe)
}

type acceptancePort struct {
	generation.ProviderPort
	control *acceptanceController
}

func (p *acceptancePort) Start(ctx context.Context, req generation.StartRequest) (generation.Observation, error) {
	return p.control.start(ctx, req, p.ProviderPort.Start)
}

func (p *acceptancePort) Observe(ctx context.Context, req generation.ObserveRequest) (generation.Observation, error) {
	return p.control.observe(ctx, req, p.ProviderPort.Observe)
}

func (c *acceptanceController) start(ctx context.Context, req generation.StartRequest, forward func(context.Context, generation.StartRequest) (generation.Observation, error)) (generation.Observation, error) {
	m, err := c.current()
	if err != nil {
		return generation.Observation{}, err
	}
	if c.module == nil {
		return generation.Observation{}, errors.New("acceptance kernel is not assembled")
	}
	for _, s := range m.Scenarios {
		task, err := c.module.Get(ctx, s.OwnerID, req.TaskID)
		if err != nil || task.OwnerID != s.OwnerID || task.SessionID != s.SessionID || task.Offering.ID != req.Offering.ID || task.Capability != req.Offering.Capability || task.CurrentAttemptID != req.AttemptID {
			continue
		}
		if s.PlanID == "" {
			image := s.Fault == "image_insufficient_quota" && task.IdempotencyKey == s.IdempotencyKey && task.Offering.ID == s.OfferingID && task.Capability == s.Capability
			standalone := c.standaloneMatches(ctx, s, task) && req.OwnerID == task.OwnerID && req.SessionID == task.SessionID && req.Offering.Revision == task.Offering.Revision && bytes.Equal(req.Input, schema.WireInput(task.Offering, task.Input))
			if !image && !standalone {
				continue
			}
		} else {
			// Every step of a preregistered plan may complete; the fault itself applies
			// only to the exact selected step's first attempt.
			plan, _, err := c.plan(ctx, s)
			if err != nil {
				continue
			}
			allowed := false
			for index, step := range plan.Steps {
				if task.IdempotencyKey == fmt.Sprintf("processing:%s:%d:%d", plan.ID, index, step.Retry) && task.Capability == step.Capability && task.Offering.ID == step.OfferingID {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
			if s.Fault == "rigging_start_failed" && task.IdempotencyKey == s.key(0) && task.Capability == s.Capability && task.Offering.ID == s.OfferingID {
				claimed, err := c.claim(s, "start")
				if err != nil {
					return generation.Observation{}, err
				}
				if claimed {
					if err := c.record("injected_start", s, req.TaskID, req.AttemptID, task.IdempotencyKey); err != nil {
						return generation.Observation{}, err
					}
					return generation.Observation{Phase: generation.UpstreamFailed, Failure: &generation.Failure{Class: generation.FailureRejected, Retryable: true, SafeDetail: "acceptance definitive pre-outbound failure"}}, nil
				}
			}
		}
		reserved := false
		for slot := 0; slot < s.MaxProviderStarts; slot++ {
			claimed, err := c.claim(s, fmt.Sprintf("real_start_budget_%d", slot))
			if err != nil {
				return generation.Observation{}, err
			}
			if claimed {
				reserved = true
				break
			}
		}
		if !reserved {
			return generation.Observation{}, &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureInternal, Retryable: false, SafeDetail: "isolated acceptance start budget exhausted"}}
		}
		if err := c.record("real_start", s, req.TaskID, req.AttemptID, task.IdempotencyKey); err != nil {
			return generation.Observation{}, err
		}
		return forward(ctx, req)
	}
	// An unexpected Task never reaches a paid upstream, including jobs accidentally
	// left active in a snapshot. Returning an error keeps normal kernel ownership.
	return generation.Observation{}, &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureInternal, Retryable: false, SafeDetail: "task outside isolated acceptance scope"}}
}

func (c *acceptanceController) standaloneMatches(ctx context.Context, s acceptanceScenario, task generation.Task) bool {
	if s.Fault != "standalone" || s.PlanID != "" || task.OwnerID != s.OwnerID || task.SessionID != s.SessionID || task.IdempotencyKey != s.IdempotencyKey || task.Offering.ID != s.OfferingID || task.Offering.Revision != s.OfferingRevision || task.Capability != s.Capability {
		return false
	}
	if c.admissions == nil {
		return false
	}
	admitted, err := c.admissions.AdmittedPreparedInput(ctx, s.OwnerID, task.ID)
	if err != nil || admitted != s.PreparedInputID {
		return false
	}
	prepared, err := c.module.GetPreparedInput(ctx, s.OwnerID, s.PreparedInputID)
	return err == nil && prepared.InputHash == s.PreparedInputHash && prepared.SessionID == s.SessionID && prepared.OfferingID == s.OfferingID && prepared.OfferingRevision == s.OfferingRevision && prepared.Capability == s.Capability && bytes.Equal(prepared.NormalizedInput, task.Input)
}

func (c *acceptanceController) observationAllowed(ctx context.Context, s acceptanceScenario, task generation.Task) bool {
	if s.PlanID == "" {
		if s.Fault == "standalone" {
			return c.standaloneMatches(ctx, s, task) && c.consumed(s, "real_start_budget_0")
		}
		return s.Fault == "image_insufficient_quota" && task.IdempotencyKey == s.IdempotencyKey && task.Offering.ID == s.OfferingID && task.Capability == s.Capability
	}
	plan, _, err := c.plan(ctx, s)
	if err != nil {
		return false
	}
	for index, step := range plan.Steps {
		if task.IdempotencyKey == fmt.Sprintf("processing:%s:%d:%d", plan.ID, index, step.Retry) && task.Capability == step.Capability && task.Offering.ID == step.OfferingID {
			return true
		}
	}
	return false
}

func (c *acceptanceController) resumePreparation(ctx context.Context, req generation.PreparationRequest, forward func(context.Context, generation.PreparationRequest) (generation.Observation, error)) (generation.Observation, error) {
	m, err := c.current()
	if err != nil {
		return generation.Observation{}, err
	}
	if c.module == nil {
		return generation.Observation{}, errors.New("acceptance kernel is not assembled")
	}
	for _, s := range m.Scenarios {
		task, err := c.module.Get(ctx, s.OwnerID, req.TaskID)
		if err != nil || task.OwnerID != req.OwnerID || task.SessionID != s.SessionID || task.Offering.ID != req.Offering.ID || task.Offering.Revision != req.Offering.Revision || task.Capability != req.Offering.Capability || task.CurrentAttemptID != req.AttemptID || !bytes.Equal(schema.WireInput(task.Offering, task.Input), req.Input) || !c.observationAllowed(ctx, s, task) {
			continue
		}
		if err := c.record("real_resume_preparation", s, req.TaskID, req.AttemptID, task.IdempotencyKey); err != nil {
			return generation.Observation{}, err
		}
		return forward(ctx, req)
	}
	return generation.Observation{}, &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureInternal, Retryable: false, SafeDetail: "preparation outside isolated acceptance scope"}}
}

// Terminal cloned Tasks can still have post-success observations. Scope these
// calls too, so a snapshot never resumes unrelated private upstream jobs.
func (c *acceptanceController) observe(ctx context.Context, req generation.ObserveRequest, forward func(context.Context, generation.ObserveRequest) (generation.Observation, error)) (generation.Observation, error) {
	m, err := c.current()
	if err != nil {
		return generation.Observation{}, err
	}
	if c.module == nil {
		return generation.Observation{}, errors.New("acceptance kernel is not assembled")
	}
	for _, s := range m.Scenarios {
		task, err := c.module.Get(ctx, s.OwnerID, req.TaskID)
		if err != nil || task.OwnerID != s.OwnerID || task.SessionID != s.SessionID || task.Offering.ID != req.Offering.ID || task.Offering.Revision != req.Offering.Revision || task.Capability != req.Offering.Capability || task.CurrentAttemptID != req.AttemptID {
			continue
		}
		if c.observationAllowed(ctx, s, task) && (s.PlanID != "" || bytes.Equal(req.Input, schema.WireInput(task.Offering, task.Input))) {
			if err := c.record("real_observe", s, req.TaskID, req.AttemptID, task.IdempotencyKey); err != nil {
				return generation.Observation{}, err
			}
			return forward(ctx, req)
		}
	}
	return generation.Observation{}, &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureInternal, Retryable: false, SafeDetail: "observation outside isolated acceptance scope"}}
}

func acceptancePathWithin(root, path string) bool {
	clean, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, clean)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func acceptanceDigest(path string) (string, error) {
	hash := sha256.New()
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		_, err = io.Copy(hash, file)
		return hex.EncodeToString(hash.Sum(nil)), err
	}
	err = filepath.WalkDir(path, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("acceptance artifact contains symlink")
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(path, name)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(relative)+"\x00")
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(hash, file)
		return err
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func acceptanceWriteJSON(path string, value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, bytes, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func acceptanceSeedGuard(root, providerMode, emptyACK string) error {
	if _, err := os.Stat(filepath.Join(root, "SEED_FAILED")); err == nil {
		return errors.New("incomplete acceptance seed is forbidden")
	} else if !os.IsNotExist(err) {
		return err
	}
	bytes, err := os.ReadFile(filepath.Join(root, "seed-report.json"))
	if os.IsNotExist(err) && providerMode == "mock" && emptyACK == "offline-mock-only" {
		return nil
	}
	if err != nil {
		return errors.New("acceptance process requires an applied seed report")
	}
	var seed struct {
		Mode                  string `json:"mode"`
		DatabaseIsPrivate     bool   `json:"database_is_private"`
		LifecycleRowsModified bool   `json:"lifecycle_rows_modified"`
		OfflineMock           bool   `json:"offline_mock"`
	}
	if json.Unmarshal(bytes, &seed) != nil || seed.Mode != "applied" || !seed.DatabaseIsPrivate || seed.LifecycleRowsModified {
		return errors.New("acceptance seed report does not prove an applied private snapshot")
	}
	if seed.OfflineMock && providerMode != "mock" {
		return errors.New("mock source seed cannot be used for live acceptance")
	}
	return nil
}

// TestAcceptanceServer is inert in ordinary gates. It requires an explicit ACK,
// a private root, a source-bound test binary, and an exact private manifest.
func TestAcceptanceServer(t *testing.T) {
	if os.Getenv("ART_AI_ACCEPTANCE_ACK") != "isolated-test-only" {
		t.Skip("isolated acceptance server requires explicit acknowledgement")
	}
	root := os.Getenv("ART_AI_ACCEPTANCE_ROOT")
	manifestPath := os.Getenv("ART_AI_ACCEPTANCE_MANIFEST")
	if !filepath.IsAbs(root) || filepath.Base(root) == "." || root == "/" {
		t.Fatal("acceptance root must be an absolute private directory")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = canonical
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("acceptance root must have mode 0700")
	}
	if !acceptancePathWithin(root, manifestPath) {
		t.Fatal("acceptance manifest must be inside its private root")
	}
	manifestInfo, err := os.Stat(manifestPath)
	if err != nil || manifestInfo.Mode().Perm()&0077 != 0 {
		t.Fatal("acceptance manifest must be private")
	}
	controller := &acceptanceController{root: root, manifestPath: manifestPath}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &controller.manifest); err != nil {
		t.Fatal("invalid acceptance manifest")
	}
	m, err := controller.current()
	if err != nil {
		t.Fatal(err)
	}
	if acceptanceSourceSHA == "" || acceptanceSourceSHA != m.SourceSHA {
		t.Fatal("test binary and manifest source SHA do not match")
	}
	if !acceptancePathWithin(root, m.CatalogFile) {
		t.Fatal("acceptance catalog must be a private copy")
	}
	lock, err := os.OpenFile(filepath.Join(root, "process.lock"), os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal("acceptance root already has a running process")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := os.MkdirAll(filepath.Join(root, "markers"), 0700); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig()
	if err != nil {
		t.Fatal("load target runtime configuration failed")
	}
	for _, originalPath := range []string{config.DatabasePath, config.StorageRoot, config.LegacyGeneratedImagesRoot} {
		if originalPath != "" && filepath.IsAbs(originalPath) && acceptancePathWithin(root, originalPath) {
			t.Fatal("private acceptance root overlaps configured application data")
		}
	}
	if err := os.Remove(filepath.Join(root, "ready.json")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	acceptanceMode := "live"
	if config.ProviderMode != "live" {
		if config.ProviderMode != "mock" || os.Getenv("ART_AI_ACCEPTANCE_MODE") != "offline-mock" {
			t.Fatal("live acceptance requires live Provider mode; offline mocks require an explicit mode")
		}
		acceptanceMode = "offline-mock"
	}
	if err := acceptanceSeedGuard(root, config.ProviderMode, os.Getenv("ART_AI_ACCEPTANCE_EMPTY_ACK")); err != nil {
		t.Fatal(err)
	}
	// No production paths or runtime credentials are serialized to the manifest.
	secretPath := filepath.Join(root, "session-secret")
	secret, secretErr := os.ReadFile(secretPath)
	if os.IsNotExist(secretErr) {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			t.Fatal(err)
		}
		secret = []byte(hex.EncodeToString(random))
		file, err := os.OpenFile(secretPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(secret)
		syncErr := file.Sync()
		file.Close()
		if writeErr != nil || syncErr != nil {
			t.Fatal("persist isolated session secret failed")
		}
	} else if secretErr != nil {
		t.Fatal("read isolated session secret failed")
	}
	secretInfo, err := os.Lstat(secretPath)
	if err != nil || secretInfo.Mode()&os.ModeSymlink != 0 || secretInfo.Mode().Perm()&0077 != 0 || len(secret) < 32 {
		t.Fatal("isolated session secret is invalid")
	}
	config.SessionSecret = string(secret)
	config.MockAuth = true
	mockUser, _ := json.Marshal(map[string]any{"id": m.OwnerID, "name": "Isolated acceptance", "email": "acceptance@example.invalid", "roles": []string{}})
	config.MockUserJSON = string(mockUser)
	config.AuthMode = "development"
	config.DevelopmentUser = m.OwnerID
	config.DevelopmentRoles = nil
	config.AgentMode = "mock"
	config.FrontendPublicAppURL = "http://" + m.Address
	config.PublicBaseURL = ""
	config.AssetSigningKey = ""
	config.RuntimeSettings["public_base_url"] = ""
	config.RuntimeSettings["asset_signing_key"] = ""
	config.Environment = "development"
	config.Address = m.Address
	config.CatalogFile = m.CatalogFile
	config.WebAssetsRoot = m.WebAssetsRoot
	config.DatabasePath = filepath.Join(root, "acceptance.db")
	config.StorageRoot = filepath.Join(root, "assets")
	config.LegacyGeneratedImagesRoot = filepath.Join(root, "generated")
	for _, path := range []string{config.StorageRoot, config.LegacyGeneratedImagesRoot} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if !acceptancePathWithin(root, path) {
			t.Fatal("acceptance storage escaped private root")
		}
	}
	if existing, err := os.Stat(config.DatabasePath); err == nil && (existing.Mode()&os.ModeSymlink != 0 || !acceptancePathWithin(root, config.DatabasePath)) {
		t.Fatal("acceptance database escaped private root")
	}
	// The isolated image connection runs the actual Adapter against an exact wire
	// failure. Other connections retain their target-environment runtime settings.
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`)
	}))
	defer imageServer.Close()
	product, err := loadCatalog(config.CatalogFile)
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	privateSettings := []string{schema.Runtime.InputAssets.LegacyStorageRootSetting, schema.Administration.Generations.Assets.LegacyCacheRootSetting, schema.Runtime.AssetDelivery.Proxy.Cache.RootSetting, schema.Runtime.AssetDelivery.Proxy.Thumbnail.RootSetting}
	for index, name := range privateSettings {
		if name == "" {
			continue
		}
		directory := filepath.Join(root, fmt.Sprintf("legacy-private-%d", index))
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		config.RuntimeSettings[name] = directory
	}
	for _, connection := range schema.Runtime.ProviderConnections {
		if connection.Protocol == "image-sync" {
			if controller.imageAdapters == nil {
				controller.imageAdapters = map[string]generation.ProviderPort{}
			}
			adapter, err := schemaimages.New(schemaimages.Config{BaseURL: imageServer.URL, APIKey: "isolated-acceptance-wire-key", AuthHeader: connection.AuthHeader, AuthPrefix: connection.AuthPrefix, ErrorMapping: connection.ErrorMapping})
			if err != nil {
				t.Fatal(err)
			}
			controller.imageAdapters[connection.AdapterKey] = adapter
			config.RuntimeSettings[connection.BaseURLSetting] = imageServer.URL
			config.RuntimeSettings[connection.APIKeySetting] = "isolated-acceptance-wire-key"
		}
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// FAKE_UPSTREAM_STACK_URL replays the recovery scenarios against the local fake
	// upstream (issue #96): the same loopback-only mapping the fake stack uses
	// (see fake_upstream_stack_test.go). Unset, the connections keep their own
	// transports and the target environment's real Provider.
	var fakeTransport func(string, http.RoundTripper) http.RoundTripper
	if raw := os.Getenv("FAKE_UPSTREAM_STACK_URL"); raw != "" {
		fakeURL, parseErr := url.Parse(raw)
		if parseErr != nil || fakeURL.Scheme != "http" {
			t.Fatal("FAKE_UPSTREAM_STACK_URL must be the fake's http origin")
		}
		if host, _, splitErr := net.SplitHostPort(fakeURL.Host); splitErr != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			t.Fatal("the fake upstream must be on loopback")
		}
		fakeTransport = fakeStackHTTPTransport(fakeURL)
	}
	application, err := newWithDependencies(config, logger, assemblyOverrides{httpTransport: fakeTransport, providers: func(base generation.ProviderRegistry) generation.ProviderRegistry {
		return acceptanceRegistry{base, controller}
	}, processingGateway: func(base processing.Gateway) processing.Gateway { return &acceptanceGateway{base, controller} },
		taskAdmissions: func(store generation.TaskAdmissionStore) { controller.admissions = store }})
	if err != nil {
		t.Fatal("assemble isolated application failed", err)
	}
	controller.module = application.generation
	controller.plans = application.processing
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binaryDigest, err := acceptanceDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	frontendDigest, err := acceptanceDigest(m.WebAssetsRoot)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := acceptanceDigest(m.CatalogFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	client := &http.Client{Timeout: time.Second}
	ready := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + m.Address + application.schema.Runtime.HTTP.Routes.Readiness)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		select {
		case err := <-done:
			t.Fatal("acceptance server exited before readiness", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		cancel()
		<-done
		t.Fatal("acceptance server readiness timed out")
	}
	if err := acceptanceWriteJSON(filepath.Join(root, "ready.json"), map[string]any{"source_sha": m.SourceSHA, "address": m.Address, "test_binary_sha256": binaryDigest, "frontend_sha256": frontendDigest, "catalog_sha256": catalogDigest, "pid": os.Getpid(), "provider_mode": config.ProviderMode, "agent_mode": config.AgentMode, "acceptance_mode": acceptanceMode, "ready_at": time.Now().UTC()}); err != nil {
		cancel()
		<-done
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// These compile-time assertions preserve the concrete Meshy optional ports.
var _ generation.DependencyJobResolver = (*acceptanceMeshy)(nil)
var _ generation.ProviderWebhookDecoder = (*acceptanceMeshy)(nil)
var _ generation.ProviderInputValidator = (*acceptanceTripo)(nil)
var _ generation.ProviderPreparationResumer = (*acceptanceTripo)(nil)
var _ generation.ProviderTaskDeleter = (*acceptanceMeshy)(nil)

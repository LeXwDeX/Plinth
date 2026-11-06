package chat

import (
	"context"
	"errors"
	"time"
)

var (
	ErrBranchPendingApproval = errors.New("chat: branch has pending approval")
	ErrBranchScene           = errors.New("chat: branch has scene objects")
	ErrBranchUnavailable     = errors.New("chat: branch unavailable")
)

type BranchRequest struct {
	OwnerID, SessionID, MessageID, NewSessionID, TitleTemplate string
	CanvasProjectIDPrefix                                      string
	At                                                         time.Time
	NewID                                                      func(string) string
	ActiveStates                                               []string
}

type branchRepository interface {
	BranchSession(context.Context, BranchRequest) (Session, error)
}

func (service *Service) BranchSession(ctx context.Context, ownerID, sessionID, messageID string) (Session, error) {
	store, ok := service.repository.(branchRepository)
	if !ok || service.branchTitleTemplate == "" {
		return Session{}, ErrBranchUnavailable
	}
	return store.BranchSession(ctx, BranchRequest{OwnerID: ownerID, SessionID: sessionID, MessageID: messageID,
		NewSessionID: service.newID("session"), NewID: service.newID, At: service.clock(), TitleTemplate: service.branchTitleTemplate,
		CanvasProjectIDPrefix: service.canvasProjectIDPrefix,
		ActiveStates:          []string{service.turnQueued, service.turnRunning, service.turnWaitingTask}})
}

package agentparams

import (
	"encoding/json"
	"log/slog"
)

// StatePersister 把会话级 agent 状态落到持久层（SQLite）。写穿透 +
// 读穿透：写入同时进内存与持久层，内存未命中（进程重启后）从持久层
// 恢复——重启后待批准的同意门禁仍然生效，参数与绑定不丢。
type StatePersister interface {
	SaveAgentState(ownerID, scope, key string, value json.RawMessage) error
	DeleteAgentState(ownerID, scope string, keys ...string) error
	LoadAgentState(ownerID, scope string) (map[string]json.RawMessage, error)
}

// OfferingStatePersister atomically publishes one bounded Offering settings
// patch, including reasons and clear tombstones, if the read snapshot is current.
// Other legacy state mutations retain their existing persistence contract.
type OfferingStatePersister interface {
	PatchOfferingState(ownerID, scope string, expected, values map[string]json.RawMessage, deleted []string) error
}

// splitScope 反解 ScopeKey 的组合键；agentparams 拥有该格式。
func splitScope(scope string) (ownerID, subScope string, ok bool) {
	for index := 0; index < len(scope); index++ {
		if scope[index] == 0 {
			return scope[:index], scope[index+1:], true
		}
	}
	return "", "", false
}

// NewPersistentStore 返回带持久化挂钩的 store；持久层错误只记录日志、
// 不阻断旧 flat 内存语义。精确 Offering patch 使用独立原子端口，失败返回错误。
func NewPersistentStore(persister StatePersister, logger *slog.Logger) *Store {
	if persister == nil {
		return NewStore()
	}
	if logger == nil {
		logger = slog.Default()
	}
	store := NewStore()
	store.persister = persister
	store.logger = logger
	return store
}

// NewPersistentParameterStore exposes only ordinary request parameters to
// untrusted harness/agent edits even though SQLite stores private agent state
// in the same owner/session scope.
func NewPersistentParameterStore(persister StatePersister, logger *slog.Logger) *Store {
	store := NewPersistentStore(persister, logger)
	store.parameterOnly = true
	return store
}

func (s *Store) persistSet(scope string, params map[string]json.RawMessage) {
	if s.persister == nil {
		return
	}
	ownerID, subScope, ok := splitScope(scope)
	if !ok {
		return
	}
	for key, value := range params {
		if err := s.persister.SaveAgentState(ownerID, subScope, key, value); err != nil {
			s.logger.Warn("agent 状态持久化失败", "scope", subScope, "key", key, "error", err)
		}
	}
}

func (s *Store) persistDelete(scope string, keys ...string) {
	if s.persister == nil {
		return
	}
	ownerID, subScope, ok := splitScope(scope)
	if !ok {
		return
	}
	if err := s.persister.DeleteAgentState(ownerID, subScope, keys...); err != nil {
		s.logger.Warn("agent 状态删除失败", "scope", subScope, "error", err)
	}
}

// persistedGet 持久化模式下直读持久层：级联删除等外部变更必须
// 立即可见，不能依赖内存缓存。持久层故障时退回内存视图。
func (s *Store) persistedGet(scope string) (map[string]json.RawMessage, bool) {
	if s.persister == nil {
		return nil, false
	}
	ownerID, subScope, ok := splitScope(scope)
	if !ok {
		return nil, false
	}
	state, err := s.persister.LoadAgentState(ownerID, subScope)
	if err != nil {
		s.logger.Warn("agent 状态读取失败，退回内存视图", "scope", subScope, "error", err)
		return nil, false
	}
	return state, true
}

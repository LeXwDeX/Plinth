// Package agentparams 保存 AGENTS 模式下按会话/任务隔离的请求参数。
// harness（用户）与 agent（set_params 工具）双向读写同一 store；
// 任务创建以 store 中的参数为权威请求参数。
package agentparams

import (
	"encoding/json"
	"log/slog"
	"sync"
)

type Store struct {
	mu            sync.RWMutex
	scopes        map[string]map[string]json.RawMessage
	userOverrides map[string]map[string]json.RawMessage
	// persister 非空时启用写穿透/读穿透持久化（B10）。
	persister StatePersister
	logger    *slog.Logger
	// parameterOnly prevents untrusted harness and LLM parameters from writing
	// private task/consent control keys in the shared persistent state table.
	parameterOnly bool
}

func NewStore() *Store {
	return &Store{
		scopes:        map[string]map[string]json.RawMessage{},
		userOverrides: map[string]map[string]json.RawMessage{},
	}
}

// ScopeKey 组合 owner 与 scope（session/task），保证 store 键按用户隔离：
// 任何端点/工具都不得跨 owner 读写他人 scope。
func ScopeKey(ownerID, scope string) string {
	return ownerID + "\x00" + scope
}

func (s *Store) Get(scope string) map[string]json.RawMessage {
	return s.visibleParams(s.rawParams(scope))
}

func (s *Store) rawParams(scope string) map[string]json.RawMessage {
	if state, ok := s.persistedGet(scope); ok {
		return state
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.scopes[scope]
	if len(src) == 0 && len(s.userOverrides[scope]) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(src)+len(s.userOverrides[scope]))
	for key, value := range src {
		out[key] = append(json.RawMessage(nil), value...)
	}
	for key, value := range s.userOverrides[scope] {
		out[key] = append(json.RawMessage(nil), value...)
	}
	return out
}

func (s *Store) visibleParams(values map[string]json.RawMessage) map[string]json.RawMessage {
	for key := range values {
		if isOfferingStateKey(key) || (s.parameterOnly && isPrivateAgentStateKey(key)) {
			delete(values, key)
		}
	}
	return values
}

func (s *Store) allowedParams(params map[string]json.RawMessage) map[string]json.RawMessage {
	allowed := make(map[string]json.RawMessage, len(params))
	for key, value := range params {
		if !isOfferingStateKey(key) && (!s.parameterOnly || !isPrivateAgentStateKey(key)) {
			allowed[key] = value
		}
	}
	return allowed
}

// SetUser records explicit harness edits. They take precedence over agent
// suggestions until the user clears the field or switches to another task.
func (s *Store) SetUser(scope string, params map[string]json.RawMessage) {
	params = s.allowedParams(params)
	s.mu.Lock()
	defer s.mu.Unlock()
	overrides, ok := s.userOverrides[scope]
	if !ok {
		if len(s.userOverrides) >= maxScopes {
			return
		}
		overrides = map[string]json.RawMessage{}
		s.userOverrides[scope] = overrides
	}
	for key, value := range params {
		if len(value) == 0 || string(value) == "null" {
			delete(overrides, key)
			delete(s.scopes[scope], key)
			continue
		}
		if len(key) > maxValueKeyBytes || len(value) > maxValueKeyBytes {
			continue
		}
		if _, exists := overrides[key]; !exists &&
			len(overrides) >= maxScopeKeys {
			continue
		}
		overrides[key] = append(json.RawMessage(nil), value...)
	}
	s.persistSet(scope, params)
}

// storeLimits 防御无界内存增长：harness/agent 双向写入没有天然的
// 条数上限，恶意或失控的 LLM 输出不能把进程内存吃满。
const (
	maxKeysPerScope  = 64
	maxScopeBytes    = 64 << 10 // 64KiB
	maxScopeKeys     = 64
	maxScopes        = 100_000
	maxValueKeyBytes = 4 << 10 // 单键 JSON 上限
)

func boundedParams(
	scopes int,
	current map[string]json.RawMessage,
	params map[string]json.RawMessage,
) (map[string]json.RawMessage, bool) {
	if current == nil {
		if scopes >= maxScopes {
			return nil, false
		}
		current = map[string]json.RawMessage{}
	}
	for key, value := range params {
		if len(value) == 0 || string(value) == "null" {
			delete(current, key)
			continue
		}
		if len(key) > maxValueKeyBytes || len(value) > maxValueKeyBytes {
			continue
		}
		if _, exists := current[key]; !exists &&
			len(current) >= maxKeysPerScope {
			continue
		}
		current[key] = append(json.RawMessage(nil), value...)
	}
	if totalBytes(current) > maxScopeBytes {
		return nil, false
	}
	return current, true
}

func totalBytes(current map[string]json.RawMessage) int {
	total := 0
	for key, value := range current {
		total += len(key) + len(value)
	}
	return total
}

func (s *Store) Set(scope string, params map[string]json.RawMessage) {
	params = s.allowedParams(params)
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.scopes[scope]
	next, ok := boundedParams(len(s.scopes), current, params)
	if !ok {
		return
	}
	s.scopes[scope] = next
	s.persistSet(scope, params)
}

func (s *Store) Clear(scope string) {
	if s.parameterOnly {
		current := s.rawParams(scope)
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		if len(keys) > 0 {
			s.Delete(scope, keys...)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.scopes, scope)
	delete(s.userOverrides, scope)
	s.persistDelete(scope)
}

// Delete 只移除指定键（scopes 与 userOverrides 两层），保留同 scope
// 下的其他状态。供同意状态一次性消费等场景使用。
func (s *Store) Delete(scope string, keys ...string) {
	if s.parameterOnly {
		filtered := keys[:0]
		for _, key := range keys {
			if !isPrivateAgentStateKey(key) {
				filtered = append(filtered, key)
			}
		}
		keys = filtered
		if len(keys) == 0 {
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		delete(s.scopes[scope], key)
		delete(s.userOverrides[scope], key)
	}
	s.persistDelete(scope, keys...)
}

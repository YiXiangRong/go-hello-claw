package context

import (
	"sync"
	"time"

	"github.com/yixiangrong/go-hello-claw/internal/schema"
)

// Session 代表了一次持续的人机交互过程，它负责维护该会话完整的历史
type Session struct {
	ID        string
	WorkDir   string // 该会话绑定的物理工作区，为了模拟
	CreatedAt time.Time
	UpdatedAt time.Time

	// 【新增】用于统计该 Session 累计消耗的资源
	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalCostCNY          float64

	history []schema.Message // 存放此session中所有用户输入、大模型回复和工具调用结果
	mu      sync.RWMutex // 读写锁，防止并发读写历史时发生Data Race
}

func NewSession(id string, workDir string) *Session {
	return &Session{
		ID:        id,
		WorkDir:   workDir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		history:   make([]schema.Message, 0),
	}
}

// 线程安全的向Session中追加消息
func (s *Session) Append(msgs ...schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, msgs...)
	s.UpdatedAt = time.Now()

	// 持久化预留区：在真实的工业级实现中如claude code,
	// 会在这里以JSON的格式Append到workDir/.claw/sessions/xxx.jsonl中
	// s.SaveToDisk()
}

// 不返回全量历史，而是从后往前截取最近的N条消息，形成Agent的“短期工作记忆”
func (s *Session) GetWorkingMemory(limit int) []schema.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.history)
	if total <= limit || limit <= 0 {
		// 如果历史总量小于限制，或者不设限，全量返回
		res := make([]schema.Message, total)
		copy(res, s.history)
		return res
	}

	res := make([]schema.Message, limit)
	copy(res, s.history[total-limit:])

	// 处理截断边缘的 ToolResult 孤儿问题：如果我们截断后的第一条消息正好是一条toolResult(ToolCallID不为空)，但是ToolCall被我们截断了
	// 喂给大模型API会直接报400 Bad Request,所以必须把孤儿toolResult强行舍弃，顺延到下一条正常的User/RoleAssistant消息
	for len(res) > 0 {
		if res[0].Role == schema.RoleUser && res[0].ToolCallID != "" {
			res = res[1:]
		} else {
			break
		}
	}

	return res
}

// SessionManager 用于多用户、多终端隔离
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

var GlobalSessionMgr = &SessionManager{
	sessions: make(map[string]*Session),
}
// GetOrCreate 获取或创建一个会话
func (sm *SessionManager) GetOrCreate(id string, workDir string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sess, exists := sm.sessions[id]; exists {
		return sess
	}
	sess := NewSession(id, workDir)
	sm.sessions[id] = sess
	return sess
}

// RecordUsage 是一个给外部 Tracker 调用的辅助方法，用于累加账单
func (s *Session) RecordUsage(prompt int, completion int, cost float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalPromptTokens += prompt
	s.TotalCompletionTokens += completion
	s.TotalCostCNY += cost
}
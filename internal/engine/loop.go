package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	ctxpkg "github.com/yixiangrong/go-hello-claw/internal/context"
	"github.com/yixiangrong/go-hello-claw/internal/provider"
	"github.com/yixiangrong/go-hello-claw/internal/schema"
	"github.com/yixiangrong/go-hello-claw/internal/tools"
)

// TODO:待重构

type AgentEngine struct {
	provider       provider.LLMProvider
	registry       tools.Registry
	EnableThinking bool
	compactor      *ctxpkg.Compactor
}

func NewAgentEngine(p provider.LLMProvider, r tools.Registry, enableThinking bool) *AgentEngine {
	return &AgentEngine{
		provider:       p,
		registry:       r,
		EnableThinking: enableThinking,
		compactor:      ctxpkg.NewCompactor(3000, 6), // 测试时将阈值调低至 3000，保护区设为 6

	}
}

// 接收 Session 参数，动态加载工作区环境
func (e *AgentEngine) Run(ctx context.Context, session *ctxpkg.Session, reporter Reporter) error {
	log.Printf("[Engine] 唤醒会话 [%s]，锁定工作区: %s\n", session.ID, session.WorkDir)

	composer := ctxpkg.NewPromptComposer(session.WorkDir)
	systemMsg := composer.Build()

	for {
		availableTools := e.registry.GetAvailableTools()

		// 获取短期工作记忆,最近20条
		workingMemory := session.GetWorkingMemory(20)

		var contextHistory []schema.Message
		contextHistory = append(contextHistory, systemMsg)
		contextHistory = append(contextHistory, workingMemory...)

		// 【核心防线】在向大模型发起请求前，执行上下文双重降级压缩！
		compactedContext := e.compactor.Compact(contextHistory)

		// Phase 1: Thinking
		if e.EnableThinking {
			if reporter != nil {
				reporter.OnThinking(ctx)
			}
			thinkResp, err := e.provider.Generate(ctx, compactedContext, nil)
			if err != nil {
				return fmt.Errorf("Thinking 阶段失败: %w", err)
			}
			if thinkResp.Content != "" {
				session.Append(*thinkResp)
				compactedContext = append(compactedContext, *thinkResp)
			}
		}

		// Phase 2: Action
		actionResp, err := e.provider.Generate(ctx, compactedContext, availableTools)
		if err != nil {
			return fmt.Errorf("Action 阶段失败: %w", err)
		}

		session.Append(*actionResp) // 持久化，写入Session(硬盘、全量内存)的永远是全量的真实相应，不收compact影响
		compactedContext = append(compactedContext, *actionResp)

		if actionResp.Content != "" && reporter != nil {
			reporter.OnMessage(ctx, actionResp.Content)
		}

		if len(actionResp.ToolCalls) == 0 {
			break
		}

		observationMsgs := make([]schema.Message, len(actionResp.ToolCalls))
		var wg sync.WaitGroup

		for i, toolCall := range actionResp.ToolCalls {
			wg.Add(1)

			go func(idx int, call schema.ToolCall) {
				defer wg.Done()

				if reporter != nil {
					reporter.OnToolCall(ctx, call.Name, string(call.Arguments))
				}

				result := e.registry.Execute(ctx, call)

				if reporter != nil {
					displayOutput := result.Output
					if len(displayOutput) > 200 {
						displayOutput = displayOutput[:200] + "... (已截断)"
					}
					reporter.OnToolResult(ctx, call.Name, displayOutput, result.IsError)
				}

				observationMsgs[idx] = schema.Message{
					Role:       schema.RoleUser,
					Content:    result.Output,
					ToolCallID: call.ID,
				}
			}(i, toolCall)
		}

		wg.Wait()

		// 持久化观察结果
		session.Append(observationMsgs...)
	}

	return nil
}

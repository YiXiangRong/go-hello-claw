package main

import (
	"context"
	"log"
	"os"

	"github.com/yixiangrong/go-hello-claw/internal/engine"
	"github.com/yixiangrong/go-hello-claw/internal/provider"
	"github.com/yixiangrong/go-hello-claw/internal/tools"

	ctxpkg "github.com/yixiangrong/go-hello-claw/internal/context"
	"github.com/yixiangrong/go-hello-claw/internal/observability"
	"github.com/yixiangrong/go-hello-claw/internal/schema"
)

func main() {
	if os.Getenv("ZHIPU_API_KEY") == "" {
		log.Fatal("请先导出 ZHIPU_API_KEY 环境变量")
	}

	workDir, _ := os.Getwd()
	workDir += "/workspace"
	modelName := "glm-4.7"
	realProvider := provider.NewZhipuOpenAIProvider(modelName)
	sess := ctxpkg.GlobalSessionMgr.GetOrCreate("test_trace_001", workDir)



	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))

	trackedProvider := observability.NewCostTracker(realProvider, modelName, sess)
	eng := engine.NewAgentEngine(trackedProvider, registry, false, false)

	reporter := engine.NewTerminalReporter()

	// 触发一个跨工具类型的并发任务
	prompt := `
	为了加快执行速度，请你在一轮回复中，【同时并行】完成以下两件事：
	1. 使用 bash 工具执行 'sleep 2 && echo "系统环境检查完毕"'
	2. 使用 write_file 工具，在当前目录下创建一个 'trace_test.md'，内容写上 "测试并发的写入"。
	请确保你是分别调用两个不同的工具，不要试图把它们合并成一个命令！
	`
	sess.Append(schema.Message{Role: schema.RoleUser, Content: prompt})

	log.Println("\n>>> 🚀 启动带 Tracing 链路追踪的测试...")
	err := eng.Run(context.Background(), sess, reporter)
	if err != nil {
		log.Fatalf("引擎崩溃: %v", err)
	}
	
}

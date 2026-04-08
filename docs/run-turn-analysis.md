# runTurn 核心函数详解

## 概述

`runTurn()` 函数是 PicoClaw Agent Loop 的**核心心脏**，处理整个 AI 推理循环。

**位置：** `pkg/agent/loop.go:1653`

**职责：**
1. 管理单个"轮次"（Turn）的完整生命周期
2. 处理上下文（历史、摘要）
3. 加载和执行工具（Tools）
4. 调用 LLM 推理
5. 处理中断和错误重试
6. 支持异步工具和流式响应

---

## 📊 执行流程总览

```
runTurn() 开始
    ↓
1️⃣ 初始化 Context
    • 创建 turnContext（带 cancel）
    • 注入 turnState、AgentLoop 到 context
    • 注册活跃 Turn
    ↓
2️⃣ 上下文组装 ContextAssemble
    • contextManager.Assemble() - 获取预算内的历史
    • ContextBuilder.BuildMessages() - 组装消息列表
    • 检查是否超出 token 预算
    • 如果超出：执行压缩 + 重新组装
    ↓
3️⃣ 工具加载 ToolsLoading
    • ts.agent.Tools.ToProviderDefs() - 转换工具为 LLM 可用格式
    • 检测并加载 Web Search（原生支持）
    • 检测 Thinking 能力
    ↓
4️⃣ 主循环 TurnLoop
    ├─ 第1次迭代
    │  ├─ 检查 Steering 消息（用户指导）
    │  ├─ 处理 SubTurn 结果
    │  └─ 构建 LLM 请求
    │
    ├─ Hook: BeforeLLM
    │
    ├─ 调用 LLM.Chat()
    │  ├─ 含 Fallback 重试逻辑
    │  ├─ 超时重试（2次）
    │  └─ 上下文溢出自动压缩重试
    │
    ├─ Hook: AfterLLM
    │
    ├─ 如果 LLM 返回工具调用
    │  └─ Tool Loop
    │     ├─ Hook: BeforeTool
    │     ├─ Hook: ApproveTool（权限检查）
    │     ├─ Tools.ExecuteWithContext()
    │     ├─ Hook: AfterTool
    │     ├─ 处理工具结果（ForUser、ForLLM）
    │     ├─ 异步工具回调处理
    │     ├─ 检查 Steering 消息（tool 执行中）
    │     └─ 检查是否跳过剩余工具
    │
    ├─ 如果有待处理消息：回到 turnLoop
    └─ 直到无工具调用或达到最大迭代
    ↓
5️⃣ 最终化 Finalization
    • 保存最终响应到会话
    • 执行上下文总结压缩
    • 发送 turn.end 事件
    ↓
返回 turnResult
```

---

## 1️⃣ 上下文处理（Context Handling）

### 关键概念

**上下文窗口管理的三层架构：**
```
┌─────────────────────────────────────┐
│ ContextWindow (e.g., 8000 tokens)   │
├─────────────────────────────────────┤
│ MaxTokens (e.g., 2000 for response) │
├─────────────────────────────────────┤
│ Budget = ContextWindow - MaxTokens  │ ← 用于历史消息
├─────────────────────────────────────┤
│ History + Summary + System Prompt   │
└─────────────────────────────────────┘
```

### 流程：Assemble → BuildMessages → Check → Compact

#### 步骤 1: Assemble（获取历史）

**代码位置：** `loop.go:1705-1712`

```go
// ContextManager 组装预算内的会话历史
// AssembleRequest 包含：
//   - SessionKey: 会话标识
//   - Budget: 剩余 token 预算（ContextWindow - MaxTokens）
//   - MaxTokens: 响应最大 token 数
resp, err := al.contextManager.Assemble(turnCtx, &AssembleRequest{
    SessionKey: ts.sessionKey,        // 如: "agent_user_123"
    Budget:     ts.agent.ContextWindow, // 如: 8000
    MaxTokens:  ts.agent.MaxTokens,   // 如: 2000
})
// 返回结果：
// - resp.History: []providers.Message (过往对话)
// - resp.Summary: string (被总结的早期历史摘要)
```

**ContextManager 做什么？**
- 从 SessionStore 读取所有历史消息
- 按优先级排序（最近的消息优先）
- 计算每条消息的 token 数
- 保留在预算内的最新消息
- 返回超出预算的早期消息摘要

#### 步骤 2: BuildMessages（组装消息）

**代码位置：** `loop.go:1714-1722`

```go
// ContextBuilder 构建 LLM API 需要的消息格式
// 输入：
//   - history: 历史对话
//   - summary: 早期历史摘要
//   - ts.userMessage: 当前用户消息
//   - ts.media: 媒体文件 (图片、文档等)
//   - skillNames: 当前启用的技能列表
//
// 返回：LLM API 格式的消息列表
messages := ts.agent.ContextBuilder.BuildMessages(
    history,
    summary,
    ts.userMessage,           // "你好"
    ts.media,
    ts.channel,               // "pico"
    ts.chatID,                // "pico:session-456"
    ts.opts.SenderID,         // "pico-user"
    ts.opts.SenderDisplayName,
    activeSkillNames(ts.agent, ts.opts)...,
)

// 消息列表结构：
// [
//   {role: "system", content: "系统提示词..."},    ← 包含所有 skills 描述
//   {role: "user", content: "早期摘要..."},         ← 如果有压缩历史
//   {role: "assistant", content: "..."},
//   {role: "user", content: "..."},
//   {role: "assistant", content: "..."},
//   {role: "user", content: "你好"}               ← 当前消息
// ]
```

**BuildMessages 内部处理：**
1. 生成系统提示（包含 workspace 上下文、可用技能等）
2. 加入历史摘要（如果有）
3. 加入完整历史对话
4. 解析媒体引用（media:// URLs）并转为 base64

#### 步骤 3: 检查预算超出

**代码位置：** `loop.go:1723-1753`

```go
// 检查是否超出 token 预算
if isOverContextBudget(ts.agent.ContextWindow, messages, toolDefs, ts.agent.MaxTokens) {
    logger.WarnCF("agent", "Proactive compression: context budget exceeded before LLM call", ...)

    // 主动压缩：总结早期历史以节省空间
    if err := al.contextManager.Compact(turnCtx, &CompactRequest{
        SessionKey: ts.sessionKey,
        Reason:     ContextCompressReasonProactive,  // "proactive" 提前压缩
    }); err != nil {
        logger.WarnCF("agent", "Proactive compact failed", ...)
    }

    // 重新从压缩后的会话获取历史
    ts.refreshRestorePointFromSession(ts.agent)

    // 重新组装消息
    resp, _ := al.contextManager.Assemble(turnCtx, ...)
    history = resp.History
    summary = resp.Summary

    // 重新构建消息列表（会更短）
    messages = ts.agent.ContextBuilder.BuildMessages(...)
}
```

**压缩（Compact）做什么？**
- 将早期历史消息聚合总结
- 比如：50 条消息压缩成 1 条摘要
- 实现：调用 LLM 自己生成摘要
- 优点：保留信息，节省 token

### 媒体处理

**代码位置：** `loop.go:1724`

```go
// 解析 media:// 引用，转换为 base64
// 用于视觉 LLM（支持图片）
messages = resolveMediaRefs(messages, al.mediaStore, maxMediaSize)

// resolveMediaRefs 做什么？
// media:// URIs → 加载文件 → base64 编码 → 插入消息
// 例如：
// 输入：{role: "user", media: ["media://scope/image.jpg"]}
// 输出：{role: "user", content: "![image](data:image/jpeg;base64,...)"}
```

---

## 2️⃣ 工具加载（Tool Loading）

### 三层工具系统

```
┌─ Tools / MCP / Skills ────────────────┐
│                                        │
│ 1. Tools (工具)                        │
│    - read_file, write_file             │
│    - exec, list_dir                    │
│    - web_search, send_email            │
│    - send_message, send_tts            │
│    放在 ToolRegistry 中                │
│                                        │
│ 2. Skills (技能)                       │
│    - Workspace 下的 .md 文件           │
│    - 通过 ContextBuilder 加载         │
│    - 在系统提示中描述                  │
│    - LLM 可以引用但不调用              │
│                                        │
│ 3. MCP (Model Context Protocol)        │
│    - 外部 LLM 可读的工具               │
│    - 通过 Tool 的 Definition 暴露      │
│    - LLM 可以选择调用                  │
│                                        │
└────────────────────────────────────────┘
```

### 工具加载过程

**代码位置：** `loop.go:1769`

#### 第 1 步：获取工具定义

```go
// 将 ToolRegistry 转换为 LLM 提供商能理解的格式
toolDefs := ts.agent.Tools.ToProviderDefs()

// 返回结构：[]providers.ToolDefinition
// [
//   {
//     "type": "function",
//     "function": {
//       "name": "read_file",
//       "description": "Read a file from the workspace",
//       "parameters": {
//         "type": "object",
//         "properties": {
//           "path": {"type": "string"}
//         },
//         "required": ["path"]
//       }
//     }
//   },
//   ...
// ]

// ToolRegistry 来自 Agent 初始化
// 在 instance.go 中加载的 Tools：
// - read_file, write_file, list_dir, edit_file
// - exec, spawn, send_message, etc
// - web_search, send_email, send_tts
// - ... 其他工具
```

**ToolRegistry 是如何构建的？**

见 `pkg/agent/instance.go:76-150`：
```go
toolsRegistry := tools.NewToolRegistry()

if cfg.Tools.IsToolEnabled("read_file") {
    toolsRegistry.Register(tools.NewReadFileTool(...))
}
if cfg.Tools.IsToolEnabled("web_search") {
    toolsRegistry.Register(tools.NewWebSearchTool(...))
}
// ... 根据配置加载所有启用的工具
```

#### 第 2 步：加载 Skills（技能）

**代码位置：** `loop.go:1714（BuildMessages）的参数`

```go
// activeSkillNames 函数获取当前启用的技能
skillNames := activeSkillNames(ts.agent, ts.opts)

// 返回：[]string
// ["customer_support", "product_faq", "troubleshooting"]

// 这些技能名称在 BuildMessages 时会被转换为文本描述
// 在系统提示中添加：
//
// ## Available Skills:
// ### customer_support
// - Content: "How to handle customer issues..."
// - Path: ~/.picoclaw/workspace/customer_support.md
// - Keywords: support, customer, help
// ...
//
// LLM 会看到这些技能，但无法"调用"它们
// 技能是知识库，工具是可执行的函数
```

#### 第 3 步：检测 Web Search 原生支持

**代码位置：** `loop.go:1792-1812`

```go
// 检测 LLM 提供商是否原生支持 web search
_, hasWebSearch := ts.agent.Tools.Get("web_search")
useNativeSearch := al.cfg.Tools.Web.PreferNative &&
    hasWebSearch &&
    func() bool {
        // 询问提供商是否支持原生 web search
        if ns, ok := ts.agent.Provider.(interface{ SupportsNativeSearch() bool }); ok {
            return ns.SupportsNativeSearch()
        }
        return false
    }()

if useNativeSearch {
    // 如果 LLM 原生支持 web search（如 Claude、OpenAI）
    // 移除客户端实现的 web_search tool
    // 改用 LLM 内置的搜索能力（更快、更准确）
    filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
    for _, td := range providerToolDefs {
        if td.Function.Name != "web_search" {
            filtered = append(filtered, td)
        }
    }
    providerToolDefs = filtered

    // 在 LLM 选项中启用原生搜索
    llmOpts["native_search"] = true
}
```

#### 第 4 步：检测 Thinking 能力

**代码位置：** `loop.go:1829-1840`

```go
if ts.agent.ThinkingLevel != ThinkingOff {
    // 检查 LLM 是否支持 Thinking（如 o1, Claude Opus）
    if tc, ok := ts.agent.Provider.(providers.ThinkingCapable); ok && tc.SupportsThinking() {
        // 启用 thinking（内部推理）
        llmOpts["thinking_level"] = string(ts.agent.ThinkingLevel)

        // ThinkingLevel 可选：
        // - "off"     (默认)
        // - "simple"  (轻量思考)
        // - "extended" (深度思考)
    } else {
        logger.WarnCF("agent", "thinking_level is set but provider doesn't support it", ...)
    }
}
```

---

## 3️⃣ LLM 调用与重试

### 调用流程

**代码位置：** `loop.go:1855-1950`

```
llmOpts 构建
    ↓
Hook: BeforeLLM（允许修改请求）
    ↓
Select Candidates（选择模型）
    ├─ 如果有多个候选：使用 Fallback 机制
    │  └─ 尝试第 1 个模型 → 失败 → 尝试第 2 个 → ...
    └─ 如果只有 1 个：直接调用
    ↓
callLLM() 函数（含重试逻辑）
    ├─ 尝试 1（原始调用）
    ├─ 如果超时错误 && retry < 2
    │  ├─ 等待指数退避（5s、10s）
    │  └─ 重试
    ├─ 如果上下文溢出 && retry < 2
    │  ├─ 执行压缩
    │  ├─ 重新组装消息
    │  └─ 重试
    └─ 返回响应或错误
    ↓
Hook: AfterLLM（处理响应）
    ↓
处理 Thinking 内容（发送到推理频道）
    ↓
检查是否有工具调用
    ├─ 有 → 进入 Tool Loop
    └─ 无 → 直接返回响应
```

### Fallback 机制

**代码位置：** `loop.go:1903-1930`

```go
// Fallback 机制：多个 LLM 候选自动切换
if len(activeCandidates) > 1 && al.fallback != nil {
    fbResult, fbErr := al.fallback.Execute(
        providerCtx,
        activeCandidates,  // [Claude, GPT-4, Gemini]
        func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
            // 尝试回调函数
            return activeProvider.Chat(ctx, messagesForCall, toolDefsForCall, model, llmOpts)
        },
    )

    // fbResult.Attempts: [{Provider: "claude", Model: "opus", Error: ...}, ...]
    // fbResult.Response: 成功的响应（来自第 N 个候选）
}

// Fallback 策略：
// 1. Claude Opus → 失败（超时）
// 2. GPT-4 Turbo → 失败（context overflow）
// 3. Gemini 2.0 → 成功 ✓
// 返回 Gemini 的响应给用户
```

### 重试逻辑

**代码位置：** `loop.go:1931-2000`

```go
// 最多重试 2 次（总共最多 3 次调用）
maxRetries := 2

for retry := 0; retry <= maxRetries; retry++ {
    response, err = callLLM(callMessages, providerToolDefs)

    if err == nil {
        break  // 成功！
    }

    // 检查错误类型
    errMsg := strings.ToLower(err.Error())

    // 1. 超时错误
    isTimeoutError := errors.Is(err, context.DeadlineExceeded) ||
        strings.Contains(errMsg, "timeout") ||
        strings.Contains(errMsg, "timed out")

    if isTimeoutError && retry < maxRetries {
        backoff := time.Duration(retry+1) * 5 * time.Second  // 5s, 10s
        logger.WarnCF("agent", "Timeout error, retrying after backoff", ...)
        sleepWithContext(turnCtx, backoff)
        continue  // 重试
    }

    // 2. 上下文溢出错误
    isContextError := strings.Contains(errMsg, "context_length_exceeded") ||
        strings.Contains(errMsg, "context window") ||
        strings.Contains(errMsg, "token limit")

    if isContextError && retry < maxRetries && !ts.opts.NoHistory {
        logger.WarnCF("agent", "Context window error, attempting compression", ...)

        // 发送通知给用户
        al.bus.PublishOutbound(ctx, bus.OutboundMessage{
            Channel: ts.channel,
            ChatID:  ts.chatID,
            Content: "Context window exceeded. Compressing history and retrying...",
        })

        // 执行压缩
        al.contextManager.Compact(turnCtx, &CompactRequest{
            SessionKey: ts.sessionKey,
            Reason:     ContextCompressReasonRetry,
        })

        // 重新组装消息（会更短）
        resp, _ := al.contextManager.Assemble(...)
        messages = ts.agent.ContextBuilder.BuildMessages(...)

        continue  // 重试
    }

    break  // 其他错误，不重试
}
```

---

## 4️⃣ Tool Loop（工具执行循环）

### 执行流程

**代码位置：** `loop.go:2020-2290`

```
LLM 返回工具调用
    ↓
循环处理每个工具调用
    │
    ├─ 1. Hook: BeforeTool（允许修改工具调用）
    │
    ├─ 2. Hook: ApproveTool（权限批准）
    │
    ├─ 3. 执行工具
    │   └─ ts.agent.Tools.ExecuteWithContext(...)
    │
    ├─ 4. Hook: AfterTool（处理结果）
    │
    ├─ 5. 处理工具结果
    │   ├─ toolResult.ForUser（发送给用户）
    │   ├─ toolResult.ForLLM（发送给 LLM）
    │   ├─ toolResult.Media（处理媒体文件）
    │   └─ toolResult.ResponseHandled（标志）
    │
    ├─ 6. 检查中断
    │   ├─ 是否有 steering 消息（用户中断）
    │   ├─ 是否达到 graceful 中断
    │   └─ 是否跳过剩余工具
    │
    └─ 下一个工具 ...
    ↓
回到 LLM 调用（第 N+1 次迭代）
```

### 工具执行

**代码位置：** `loop.go:2085-2150`

```go
// 执行工具（同步）
toolResult := ts.agent.Tools.ExecuteWithContext(
    execCtx,          // 含 channel, chatID, messageID 等上下文
    toolName,         // "web_search"
    toolArgs,         // {query: "..."}
    ts.channel,       // "pico"
    ts.chatID,        // "pico:session-456"
    asyncCallback,    // 异步工具的回调
)

// ExecuteWithContext 做什么？
// 1. 查找工具
// 2. 验证参数
// 3. 执行工具逻辑
// 4. 返回 ToolResult（包含 ForUser、ForLLM、Media 等）

// 异步工具支持：
// 某些工具（如 send_tts、download_file）可能耗时
// 允许工具在后台运行，通过 asyncCallback 报告结果
asyncCallback := func(ctx context.Context, result *tools.ToolResult) {
    // 工具完成时被调用

    // 1. 发送 ForUser 内容给用户（立即反馈）
    if !result.Silent && result.ForUser != "" {
        al.bus.PublishOutbound(ctx, bus.OutboundMessage{
            Channel: ts.channel,
            ChatID:  ts.chatID,
            Content: result.ForUser,
        })
    }

    // 2. 将结果发布回 MessageBus（作为系统消息）
    // 这会被 AgentLoop 主循环接收
    al.bus.PublishInbound(ctx, bus.InboundMessage{
        Channel:  "system",
        SenderID: fmt.Sprintf("async:%s", asyncToolName),
        Content:  result.ForLLM,
    })
}
```

### 工具结果处理

**代码位置：** `loop.go:2151-2210`

```go
// 工具结果结构
type ToolResult struct {
    // ForUser: 发送给用户的内容（如 "文件已上传"）
    ForUser string

    // ForLLM: 发送给 LLM 的内容（工具执行结果）
    // 例如：web_search 返回的搜索结果
    ForLLM string

    // Media: 媒体文件引用（如 media://...)
    Media []string

    // ResponseHandled: 工具是否已处理响应
    // true: 如 send_tts（已发给用户），不需要 LLM 再回复
    // false: 如 web_search（需要 LLM 处理结果），LLM 继续推理
    ResponseHandled bool

    // Async: 工具是否异步执行
    Async bool

    // IsError: 工具是否失败
    IsError bool
}

// 处理 ForUser 内容
shouldSendForUser := !toolResult.Silent && toolResult.ForUser != "" &&
    (ts.opts.SendResponse || toolResult.ResponseHandled)

if shouldSendForUser {
    // 发送给用户
    al.bus.PublishOutbound(ctx, bus.OutboundMessage{
        Channel: ts.channel,
        ChatID:  ts.chatID,
        Content: toolResult.ForUser,
    })
}

// 处理媒体（如果工具返回文件）
if len(toolResult.Media) > 0 && toolResult.ResponseHandled {
    // 工具已处理，发送媒体
    outboundMedia := bus.OutboundMediaMessage{
        Channel: ts.channel,
        ChatID:  ts.chatID,
        Parts:   mediaPartsFromResult(toolResult),
    }
    al.channelManager.SendMedia(ctx, outboundMedia)
} else if len(toolResult.Media) > 0 && !toolResult.ResponseHandled {
    // 工具未处理，生成 artifact tags 让 LLM 知道
    // 例如：[file:document.pdf] 提示 LLM 有文件
    toolResult.ArtifactTags = buildArtifactTags(al.mediaStore, toolResult.Media)
}

// 将工具结果添加到消息列表（给下一次 LLM 迭代）
toolResultMsg := providers.Message{
    Role:       "tool",
    Content:    contentForLLM,
    ToolCallID: toolCallID,
    Media:      toolResult.Media,
}
messages = append(messages, toolResultMsg)
```

### 中断检查

**代码位置：** `loop.go:2211-2250`

```go
// 检查是否有 steering 消息（用户中断）
// 例如：用户在工具执行中途发送 "stop"
if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
    pendingMessages = append(pendingMessages, steerMsgs...)

    // 跳过剩余工具
    remaining := len(normalizedToolCalls) - i - 1
    if remaining > 0 {
        logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools", map[string]any{
            "reason": "queued user steering message",
            "skipped": remaining,
        })

        // 为每个被跳过的工具添加占位符消息
        for j := i + 1; j < len(normalizedToolCalls); j++ {
            skippedMsg := providers.Message{
                Role:       "tool",
                Content:    "Skipped due to user intervention.",
                ToolCallID: normalizedToolCalls[j].ID,
            }
            messages = append(messages, skippedMsg)
        }
    }
    break  // 退出工具循环
}

// 检查是否达到 graceful 中断
if gracefulPending, _ := ts.gracefulInterruptRequested(); gracefulPending {
    // Graceful 中断：完成当前工具，停止后续
    break
}
```

---

## 5️⃣ 流式逻辑（Streaming）

### 流式架构

虽然 `runTurn()` 本身**没有直接的流式逻辑**，但支持流式的设计如下：

```
runTurn() (不涉及流式)
    ├─ 调用 LLM.Chat()
    │  └─ Provider 实现决定是否流式
    │     ├─ 如果不流式：返回完整响应
    │     └─ 如果流式：返回逐块响应
    │
    ├─ Tool 执行支持异步
    │  └─ 异步工具通过 asyncCallback 回报结果
    │
    └─ 消息分块发送给用户
       └─ 通过 ChannelManager 的 Streamer 接口

ChannelManager（处理流式）
    ├─ 检测是否支持流式
    │  └─ GetStreamer(channel, chatID) → Streamer
    │
    ├─ 如果支持
    │  ├─ 开启流式session
    │  ├─ 逐块 Update() 发送内容
    │  └─ 完成时 Finalize()
    │
    └─ 如果不支持
       └─ 缓冲完整响应后发送
```

### 流式相关代码位置

**代码位置：** `pkg/bus/bus.go:18-31`

```go
// StreamDelegate 接口（由 ChannelManager 实现）
type StreamDelegate interface {
    // GetStreamer 返回一个 Streamer，用于增量发送内容
    // 如果频道不支持流式，返回 nil, false
    GetStreamer(ctx context.Context, channel, chatID string) (Streamer, bool)
}

// Streamer 接口（由频道实现）
type Streamer interface {
    // Update 发送增量内容（逐块流式）
    Update(ctx context.Context, content string) error

    // Finalize 完成流式传输
    Finalize(ctx context.Context, content string) error

    // Cancel 取消流式传输
    Cancel(ctx context.Context)
}
```

### 流式支持的频道

查看各频道是否实现了 `Streamer` 接口：

```bash
grep -r "type.*Streamer" pkg/channels/*/
```

例如，Telegram、Discord 等支持编辑消息的平台可以实现流式：
```
1. 发送占位符消息 → 获得 messageID
2. 逐块调用 Edit() → Streamer.Update()
3. 最后调用 Edit() → Streamer.Finalize()
```

### 为什么 runTurn 不涉及流式？

**设计隔离：**
- `runTurn()` 是**业务逻辑层**，关注 AI 推理
- 流式传输是**通讯层**，关注消息转发
- `ChannelManager` 负责管理流式传输
- `Provider.Chat()` 可自行决定是否返回流式响应

**代码位置：** `pkg/channels/manager.go` （负责实际流式处理）

---

## 6️⃣ Hook 系统（拦截点）

Hook 允许自定义代码在关键点插入逻辑：

```
BeforeLLM → 修改 LLM 请求（如替换模型、修改消息）
AfterLLM → 处理 LLM 响应（如解析格式、验证内容）
BeforeTool → 修改工具调用（如替换参数）
ApproveTool → 批准/拒绝工具调用（权限检查）
AfterTool → 处理工具结果（如转换格式）
```

**代码位置：** `loop.go:1853-1900, 2047-2066, 2152-2180`

---

## 📍 关键函数速查

| 函数 | 位置 | 职责 |
|------|------|------|
| `runTurn()` | loop.go:1653 | 主循环入口 |
| `contextManager.Assemble()` | context_manager.go | 获取预算内的历史 |
| `ContextBuilder.BuildMessages()` | context.go:512 | 组装 LLM 消息 |
| `isOverContextBudget()` | 工具函数 | 检查是否超出预算 |
| `contextManager.Compact()` | context_manager.go | 压缩早期历史 |
| `ts.agent.Tools.ToProviderDefs()` | tools/ | 转换工具为 LLM 格式 |
| `activeProvider.Chat()` | providers/ | 调用 LLM API |
| `ts.agent.Tools.ExecuteWithContext()` | tools/ | 执行工具 |
| `al.emitEvent()` | 事件系统 | 发送事件 |

---

## 总结

**runTurn 的 3 个核心机制：**

1. **上下文管理** - 智能组装和压缩会话历史
   - Assemble + BuildMessages + Check + Compact

2. **工具加载** - 统一抽象 Tools、Skills、MCP
   - ToProviderDefs() + Native Search + Thinking

3. **流程控制** - Hook、重试、中断、异步支持
   - BeforeLLM/AfterLLM + Timeout/Context Retry + Async Tools

**核心优势：**
✅ 自动压缩 - 永远不会超出上下文窗口
✅ Fallback 切换 - 多个 LLM 自动容错
✅ 超时重试 - 网络抖动自动恢复
✅ 工具异步 - 不阻塞推理循环
✅ Hook 拦截 - 灵活定制业务逻辑
✅ Steering - 用户可中断或指导 AI

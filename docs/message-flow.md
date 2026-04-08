# 消息流完整链路：从前端"你好"到 Agent Loop

本文档详细描述了前端发送"你好"消息，一直到 Agent Loop 处理的完整链路。

## 📊 整体架构图

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                                                                             │
│ 前端(Web/Mobile)                                                            │
│      ↓                                                                       │
│ "你好" (WebSocket message.send)                                             │
│      ↓                                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│ PicoClaw Gateway                                                            │
│                                                                             │
│ ┌─────────────────────────────────────────────────────────────────────────┐ │
│ │ 1. Pico 频道 (pkg/channels/pico/pico.go)                              │ │
│ │    - handleWebSocket() - WebSocket 升级                                │ │
│ │    - readLoop() - 读取消息                                             │ │
│ │    - handleMessage() - 路由消息类型                                    │ │
│ │    - handleMessageSend() - 处理 message.send 类型                      │ │
│ │      ↓                                                                  │ │
│ │ 2. BaseChannel.HandleMessage()                                         │ │
│ │    (pkg/channels/base.go:247)                                          │ │
│ │    - 检查权限 (allow_from)                                             │ │
│ │    - 构建 InboundMessage 对象                                          │ │
│ │      • channel: "pico"                                                 │ │
│ │      • senderID: "pico-user"                                           │ │
│ │      • chatID: "pico:{sessionID}"                                      │ │
│ │      • content: "你好"                                                 │ │
│ │      • metadata: {platform, session_id, conn_id}                       │ │
│ │    - 发送打字指示器、占位符消息                                        │ │
│ │    - 调用 bus.PublishInbound()                                         │ │
│ │      ↓                                                                  │ │
│ │ 3. MessageBus (pkg/bus/bus.go:86)                                      │ │
│ │    - 将 InboundMessage 发送到缓冲 Channel                              │ │
│ │    - ch := mb.inbound (大小: 64)                                       │ │
│ │      ↓                                                                  │ │
└─────────────────────────────────────────────────────────────────────────────┘
│
├─────────────────────────────────────────────────────────────────────────────┐
│ Agent Loop (pkg/agent/loop.go)                                            │ │
│                                                                             │ │
│ ┌─────────────────────────────────────────────────────────────────────────┐ │ │
│ │ 4. 主 Loop (loop.go:455)                                               │ │ │
│ │    select {                                                             │ │ │
│ │        case msg, ok := <-al.bus.InboundChan():                          │ │ │
│ │            // 接收来自 MessageBus 的 InboundMessage                    │ │ │
│ │            processMessage(ctx, msg)                                     │ │ │
│ │    }                                                                     │ │ │
│ │      ↓                                                                  │ │ │
│ │ 5. processMessage() (loop.go:1325)                                      │ │ │
│ │    - 记录日志: "Processing message from pico:pico-user"                │ │ │
│ │    - 音频转文本 (如果有音频)                                            │ │ │
│ │    - 路由消息 resolveMessageRoute()                                    │ │ │
│ │      • 根据 channel, peer, metadata 查找匹配的 Agent                    │ │ │
│ │      • 生成 session_key (如: "pico_session_user_123")                  │ │ │
│ │    - 构建 processOptions:                                              │ │ │
│ │      • sessionKey                                                       │ │ │
│ │      • channel: "pico"                                                  │ │ │
│ │      • userMessage: "你好"                                              │ │ │
│ │      • senderID, chatID, media 等                                       │ │ │
│ │    - 检查命令 handleCommand()                                           │ │ │
│ │    - 调用 runAgentLoop(ctx, agent, opts)                               │ │ │
│ │      ↓                                                                  │ │ │
│ │ 6. runAgentLoop() - 执行 Agent 推理                                     │ │ │
│ │    a) 加载会话历史 (session.SessionStore)                              │ │ │
│ │    b) 构建消息列表 (ContextBuilder.BuildMessages)                      │ │ │
│ │       - 系统提示                                                       │ │ │
│ │       - 会话历史                                                       │ │ │
│ │       - 用户消息: "你好"                                                │ │ │
│ │    c) 检查 token 预算和上下文窗口                                       │ │ │
│ │    d) 选择 LLM 候选模型                                                 │ │ │
│ │    e) 调用 LLM 推理 (provider.Chat)                                     │ │ │
│ │       返回: "你好！很高兴认识你。"                                       │ │ │
│ │    f) 执行 Tool Loop（如果需要）                                        │ │ │
│ │    g) 保存消息到会话                                                   │ │ │
│ │    h) 返回响应文本                                                      │ │ │
│ │      ↓                                                                  │ │ │
│ │ 7. 发送响应 (loop.go:518)                                               │ │ │
│ │    PublishResponseIfNeeded()                                            │ │ │
│ │    - 构建 OutboundMessage                                              │ │ │
│ │    - bus.PublishOutbound()                                             │ │ │
│ │                                                                          │ │ │
│ └─────────────────────────────────────────────────────────────────────────┘ │ │
│                                                                             │ │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 🔄 详细链路描述

### 第 1 步：前端发送消息

**客户端发送 WebSocket 消息：**
```json
{
  "type": "message.send",
  "id": "msg-123",
  "session_id": "session-456",
  "timestamp": 1649000000000,
  "payload": {
    "content": "你好"
  }
}
```

### 第 2 步：Pico 频道接收（pkg/channels/pico/pico.go）

**文件位置：** `pkg/channels/pico/pico.go`

#### 2.1 handleWebSocket() - WebSocket 升级
- 监听 `/pico/` 路由
- 升级 HTTP 连接到 WebSocket
- 验证 Token (Authorization Bearer)
- 创建 picoConn 对象，分配连接 ID

#### 2.2 readLoop() - 持续读取消息
- 每个连接一个 goroutine
- 循环读取 WebSocket 消息
- 调用 `handleMessage(pc, msg)`

#### 2.3 handleMessage() - 消息类型路由
```go
func (c *PicoChannel) handleMessage(pc *picoConn, msg PicoMessage) {
    switch msg.Type {
    case TypePing:
        // 心跳响应
    case TypeMessageSend:
        c.handleMessageSend(pc, msg)  // ← 我们的"你好"走这里
    }
}
```

#### 2.4 handleMessageSend() - 消息处理（第 550 行）
```go
func (c *PicoChannel) handleMessageSend(pc *picoConn, msg PicoMessage) {
    // 提取内容
    content, _ := msg.Payload["content"].(string)  // "你好"

    // 构建 ChatID 和 SenderID
    sessionID := msg.SessionID                      // "session-456"
    chatID := "pico:" + sessionID                   // "pico:session-456"
    senderID := "pico-user"

    // 构建元数据
    metadata := map[string]string{
        "platform":   "pico",
        "session_id": sessionID,
        "conn_id":    pc.id,
    }

    // 权限检查
    if !c.IsAllowedSender(sender) {
        return
    }

    // 发送到消息处理器
    c.HandleMessage(
        c.ctx,
        peer,
        msg.ID,                    // "msg-123"
        senderID,                  // "pico-user"
        chatID,                    // "pico:session-456"
        content,                   // "你好"
        nil,                       // 无媒体
        metadata,
        sender,
    )
}
```

### 第 3 步：BaseChannel 处理（pkg/channels/base.go:247）

```go
func (c *BaseChannel) HandleMessage(
    ctx context.Context,
    peer bus.Peer,
    messageID, senderID, chatID, content string,
    media []string,
    metadata map[string]string,
    senderOpts ...bus.SenderInfo,
) {
    // 1. 权限检查
    if !c.IsAllowedSender(sender) {
        return  // 不允许的发送者被忽略
    }

    // 2. 构建 InboundMessage 对象
    msg := bus.InboundMessage{
        Channel:    "pico",           // 频道名
        SenderID:   senderID,         // "pico-user"
        Sender:     sender,
        ChatID:     chatID,           // "pico:session-456"
        Content:    "你好",
        Media:      media,
        Peer:       peer,
        MessageID:  messageID,        // "msg-123"
        MediaScope: scope,
        Metadata:   metadata,
    }

    // 3. 发送打字指示器（"正在输入..."）
    if tc, ok := c.owner.(TypingCapable); ok {
        tc.StartTyping(ctx, chatID)
    }

    // 4. 发送占位符消息
    if pc, ok := c.owner.(PlaceholderCapable); ok {
        pc.SendPlaceholder(ctx, chatID)
    }

    // 5. 发布到 MessageBus
    if err := c.bus.PublishInbound(ctx, msg); err != nil {
        logger.ErrorCF("channels", "Failed to publish inbound message", ...)
    }
}
```

### 第 4 步：MessageBus 转发（pkg/bus/bus.go:86）

```go
func (mb *MessageBus) PublishInbound(ctx context.Context, msg InboundMessage) error {
    // 检查 bus 是否关闭
    if mb.closed.Load() {
        return ErrBusClosed
    }

    // 发送到缓冲 channel（大小为 64）
    select {
    case mb.inbound <- msg:       // ← InboundMessage 进入缓冲队列
        return nil
    case <-ctx.Done():
        return ctx.Err()
    case <-mb.done:
        return ErrBusClosed
    }
}
```

**MessageBus 内部结构：**
```go
type MessageBus struct {
    inbound chan InboundMessage      // 大小: 64
    // ... 其他通道
}
```

### 第 5 步：Agent Loop 主循环（pkg/agent/loop.go:455）

Agent Loop 持续监听 MessageBus 的 inbound channel：

```go
// Agent Loop 主循环
select {
case <-ctx.Done():
    return nil

case <-idleTicker.C:
    // 定期检查（空闲超时处理）

case msg, ok := <-al.bus.InboundChan():  // ← 接收 InboundMessage
    if !ok {
        return nil  // 消息总线关闭
    }

    // 消息接收到，调用处理函数
    response, err := al.processMessage(ctx, msg)
    // ... 错误处理和响应发送
}
```

### 第 6 步：processMessage() 处理（pkg/agent/loop.go:1325）

```go
func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
    // 1. 日志记录
    logger.InfoCF("agent",
        fmt.Sprintf("Processing message from %s:%s: %s",
            msg.Channel, msg.SenderID, msg.Content),  // "Processing message from pico:pico-user: 你好"
        ...)

    // 2. 音频转文本（如果有音频）
    msg, hadAudio := al.transcribeAudioInMessage(ctx, msg)

    // 3. 系统消息处理（单独路由）
    if msg.Channel == "system" {
        return al.processSystemMessage(ctx, msg)
    }

    // 4. 消息路由 - 确定哪个 Agent 处理
    route, agent, routeErr := al.resolveMessageRoute(msg)
    if routeErr != nil {
        return "", routeErr
    }
    // route 包含：匹配的 Agent ID、channel、account 等

    // 5. 构建 processOptions
    opts := processOptions{
        SessionKey:        sessionKey,      // 如: "pico_session_user_123"
        Channel:           "pico",
        ChatID:            "pico:session-456",
        MessageID:         "msg-123",
        SenderID:          "pico-user",
        UserMessage:       "你好",
        Media:             nil,
    }

    // 6. 检查命令（如果消息是命令）
    if response, handled := al.handleCommand(ctx, msg, agent, &opts); handled {
        return response, nil
    }

    // 7. 执行 Agent Loop
    return al.runAgentLoop(ctx, agent, opts)  // ← Agent 推理入口
}
```

### 第 7 步：Agent Loop 推理（runAgentLoop）

这是核心 AI 推理循环：

```go
func (al *AgentLoop) runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (string, error) {
    sessionKey := opts.SessionKey
    userMessage := opts.UserMessage  // "你好"

    // 1. 加载会话历史
    history := agent.Sessions.GetMessages(sessionKey)

    // 2. 加载或创建上下文
    // ... 上下文管理器处理

    // 3. 构建消息列表
    messages := agent.ContextBuilder.BuildMessages(
        history,
        summary,
        userMessage,  // "你好"
        media,
        channel,
        chatID,
        senderID,
        senderDisplayName,
        activeSkills...,
    )
    // messages 包含：
    //   - 系统提示词
    //   - 对话历史
    //   - 当前用户消息: {role: "user", content: "你好"}

    // 4. 验证上下文预算
    if isOverContextBudget(contextWindow, messages, toolDefs, maxTokens) {
        // 执行上下文压缩（总结旧消息）
        al.contextManager.Compact(...)
    }

    // 5. 保存用户消息到会话
    rootMsg := providers.Message{
        Role:    "user",
        Content: "你好",
    }
    agent.Sessions.AddMessage(sessionKey, rootMsg.Role, rootMsg.Content)

    // 6. 选择 LLM 模型和候选项
    activeCandidates, activeModel, usedLight := al.selectCandidates(agent, userMessage, messages)

    // 7. 调用 LLM API（核心推理）
    llmResponse, err := activeProvider.Chat(
        turnCtx,
        messages,      // 包含 "你好"
        toolDefs,
        activeModel,
        options,
    )
    // llmResponse 例如: {
    //   "content": "你好！很高兴认识你。",
    //   "stop_reason": "end_turn",
    //   "tool_calls": []
    // }

    // 8. Tool Loop（如果 LLM 调用了工具）
    if llmResponse.ToolCalls != nil && len(llmResponse.ToolCalls) > 0 {
        for _, toolCall := range llmResponse.ToolCalls {
            // 执行工具
            result := executeTool(toolCall)
            // 将工具结果添加到消息，再次调用 LLM
        }
    }

    // 9. 保存 AI 响应到会话
    finalContent := llmResponse.Content
    agent.Sessions.AddMessage(sessionKey, "assistant", finalContent)

    // 10. 返回响应
    return finalContent, nil  // 返回: "你好！很高兴认识你。"
}
```

### 第 8 步：响应发送（PublishResponseIfNeeded）

```go
func (al *AgentLoop) PublishResponseIfNeeded(
    ctx context.Context,
    channel string,
    chatID string,
    response string,
) {
    if response == "" {
        return
    }

    // 构建 OutboundMessage
    msg := bus.OutboundMessage{
        Channel:        "pico",
        ChatID:         "pico:session-456",
        Content:        "你好！很高兴认识你。",
        MessageID:      "",  // AI 响应没有原始 ID
        StreamingID:    "",
        ForceNoStream:  false,
    }

    // 发布到 outbound channel
    if err := al.bus.PublishOutbound(ctx, msg); err != nil {
        logger.ErrorCF("agent", "Failed to publish outbound message", ...)
    }
}
```

### 第 9 步：Channel Manager 处理响应

Channel Manager 监听 outbound channel，根据 channel 名称转发到对应的频道：

```go
// 在 Channel Manager 中
for msg := range mb.OutboundChan() {
    ch, ok := cm.GetChannel(msg.Channel)  // 获取 "pico" 频道
    if !ok {
        continue
    }

    // 调用 pico 频道的 Send() 方法
    messageIDs, err := ch.Send(ctx, msg)
}
```

### 第 10 步：Pico 频道发送回前端

```go
// pkg/channels/pico/pico.go - Send() 方法
func (c *PicoChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
    // 1. 获取会话下的所有连接
    conns := c.sessionConnectionsSnapshot(msg.ChatID)

    // 2. 构建 Pico Protocol 消息
    picoMsg := PicoMessage{
        Type:      "message.create",
        SessionID: sessionID,
        Timestamp: time.Now().UnixMilli(),
        Payload: map[string]any{
            "content": "你好！很高兴认识你。",
        },
    }

    // 3. 广播到所有连接
    for _, conn := range conns {
        conn.writeJSON(picoMsg)  // 发送回 WebSocket 连接
    }

    return messageIDs, nil
}
```

### 第 11 步：前端接收响应

WebSocket 客户端接收到消息：
```json
{
  "type": "message.create",
  "session_id": "session-456",
  "timestamp": 1649000001000,
  "payload": {
    "content": "你好！很高兴认识你。"
  }
}
```

## 🎯 完整时间流

| 时刻 | 操作 | 组件 |
|------|------|------|
| T+0ms | 前端发送 "你好" | Web/Mobile 客户端 |
| T+1ms | WebSocket 升级完成 | Pico Channel - handleWebSocket |
| T+2ms | 消息被 readLoop 读取 | Pico Channel - readLoop |
| T+3ms | handleMessageSend 处理 | Pico Channel - handleMessageSend |
| T+4ms | 消息发布到 MessageBus | BaseChannel - HandleMessage |
| T+5ms | 消息进入 inbound 队列 | MessageBus |
| T+10ms | Agent Loop 接收消息 | AgentLoop - main select |
| T+11ms | processMessage 被调用 | AgentLoop - processMessage |
| T+12ms | 消息被路由到 Agent | AgentLoop - resolveMessageRoute |
| T+50ms | 调用 LLM API | LLM Provider |
| T+200ms | LLM 返回响应 | Provider Chat |
| T+201ms | 响应发布到 outbound | AgentLoop - PublishResponseIfNeeded |
| T+202ms | Channel Manager 处理 | ChannelManager - outbound 监听器 |
| T+203ms | Pico Channel Send() | Pico Channel - Send |
| T+204ms | WebSocket 广播 | Pico Channel - writeJSON |
| T+205ms | 前端接收响应 | Web/Mobile 客户端 |

## 📍 关键文件位置速查

| 功能 | 文件 | 行号 |
|------|------|------|
| Pico 消息接收 | `pkg/channels/pico/pico.go` | 425-600 |
| BaseChannel 消息处理 | `pkg/channels/base.go` | 247-330 |
| MessageBus 发布 | `pkg/bus/bus.go` | 86-88 |
| MessageBus 定义 | `pkg/bus/bus.go` | 33-56 |
| Agent Loop 主循环 | `pkg/agent/loop.go` | 450-560 |
| processMessage | `pkg/agent/loop.go` | 1325-1415 |
| runAgentLoop | `pkg/agent/loop.go` | ~1600+ |
| 响应发送 | `pkg/agent/loop.go` | ~518 |

## 🔍 调试技巧

### 在各阶段打日志

```bash
# 1. 查看 Pico 频道接收日志
grep "Received message" ~/.picoclaw/logs/gateway.log

# 2. 查看 Agent 处理日志
grep "Processing message from" ~/.picoclaw/logs/gateway.log

# 3. 查看 LLM 调用日志
grep "Chat request" ~/.picoclaw/logs/gateway.log

# 4. 查看响应发送
grep "PublishResponseIfNeeded\|PublishOutbound" ~/.picoclaw/logs/gateway.log
```

### 设置调试级别

```bash
picoclaw gateway --debug
```

### 关键 Session Key

Session Key 格式：`{channel}_{platform}_{user_id}`

例如：`pico_session_pico-user_456`

## 总结

消息流通路：
```
前端(WebSocket)
    ↓
Pico Channel (handleMessageSend)
    ↓
BaseChannel.HandleMessage()
    ↓
MessageBus.PublishInbound()
    ↓
Agent Loop (processMessage)
    ↓
runAgentLoop (LLM 推理)
    ↓
Agent Loop (PublishResponseIfNeeded)
    ↓
MessageBus.PublishOutbound()
    ↓
ChannelManager 转发
    ↓
Pico Channel.Send()
    ↓
前端(WebSocket)
```

每一步都经过异步 channel 传递，确保了系统的解耦和高并发能力。

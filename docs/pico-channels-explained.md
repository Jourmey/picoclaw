# Pico 和 Pico_Client 频道详解

## 概览

PicoClaw 中的 `pico` 和 `pico_client` 是两个互补的频道，实现了**客户端-服务器**的双向通讯模式，都基于同一个 **Pico Protocol**（基于 WebSocket）。

| 维度 | `pico` 频道 | `pico_client` 频道 |
|------|-----------|------------------|
| **角色** | 🖥️ **服务器** | 💻 **客户端** |
| **职责** | 接收外部连接 | 连接到远程服务器 |
| **通讯方式** | WebSocket 服务器 | WebSocket 客户端 |
| **配置参数** | 监听地址、端口、Token | 服务器 URL、Token |
| **应用场景** | 提供开放接口 | 连接其他系统 |

---

## 详细对比

### 1. `pico` 频道（**服务器端**）

**文件位置：** `pkg/channels/pico/pico.go:53-64`

```go
type PicoChannel struct {
    *channels.BaseChannel
    config             config.PicoConfig
    upgrader           websocket.Upgrader
    connections        map[string]*picoConn            // connID -> 连接
    sessionConnections map[string]map[string]*picoConn // sessionID -> 多个连接
    connsMu            sync.RWMutex
    ctx                context.Context
    cancel             context.CancelFunc
}
```

**关键特点：**

✅ **服务器模式**
- 启动一个 WebSocket 服务器
- 监听来自客户端的连接请求
- 处理多个并发连接

✅ **多连接管理**
- 维护 `connections` 映射：`connID → 单个连接`
- 维护 `sessionConnections` 映射：`sessionID → 多个连接`
- 同一个会话可以有多个并发连接（多客户端访问）

✅ **身份认证**
```go
// 第 383-412 行：authenticate 函数
// 检查请求头中的 Token 或子协议（subprotocol）
func (c *PicoChannel) authenticate(r *http.Request) bool {
    // 验证 Authorization Bearer token
    // 或者检查 Sec-WebSocket-Protocol 子协议
}
```

✅ **HTTP 路由**
```go
func (c *PicoChannel) WebhookPath() string { return "/pico/" }
func (c *PicoChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 处理 HTTP 升级到 WebSocket
}
```

✅ **功能接口**
- `Send()` - 向指定会话发送消息
- `EditMessage()` - 编辑已发送的消息
- `StartTyping()` - 显示正在输入状态
- `SendPlaceholder()` - 发送占位符消息
- `broadcastToSession()` - 广播到会话内的所有连接

**启动流程（第 195-202 行）**
```go
func (c *PicoChannel) Start(ctx context.Context) error {
    c.ctx, c.cancel = context.WithCancel(ctx)
    // 注册 HTTP 处理器
    // 启动 WebSocket 服务器
    c.SetRunning(true)
    return nil
}
```

**配置示例：**
```json
{
  "channels": {
    "pico": {
      "enabled": true,
      "token": "pico-xxxxxx...",
      "allowOrigins": ["*", "https://example.com"],
      "maxConnectionsPerSession": 10
    }
  }
}
```

---

### 2. `pico_client` 频道（**客户端**）

**文件位置：** `pkg/channels/pico/client.go:22-30`

```go
type PicoClientChannel struct {
    *channels.BaseChannel
    config config.PicoClientConfig
    conn   *picoConn                // 单个出站连接
    mu     sync.Mutex
    ctx    context.Context
    cancel context.CancelFunc
}
```

**关键特点：**

✅ **客户端模式**
- 主动连接到远程 WebSocket 服务器
- 维护单个出站连接
- 不接受来自其他客户端的连接

✅ **单连接设计**
- 只保存一个 `conn` 指针（`*picoConn`）
- 与服务器保持长连接
- 自动重连机制

✅ **自动重连（第 50-64 行）**
```go
func (c *PicoClientChannel) Start(ctx context.Context) error {
    if err := c.dial(); err != nil {
        c.cancel()
        return fmt.Errorf("pico_client initial connect: %w", err)
    }
    c.SetRunning(true)
    go c.reconnectLoop()  // ← 后台重连循环
    return nil
}
```

✅ **主动拨号（第 82-94 行）**
```go
func (c *PicoClientChannel) dial() error {
    header := http.Header{}
    if c.config.Token.String() != "" {
        header.Set("Authorization", "Bearer "+c.config.Token.String())
    }

    // 主动连接到远程服务器
    ws, resp, err := websocket.DefaultDialer.DialContext(c.ctx, c.config.URL, header)
    // 建立连接后创建 picoConn 对象
}
```

✅ **消息转发**
- 从远程服务器接收消息
- 转发到本地消息总线（MessageBus）
- 从消息总线接收消息
- 发送到远程服务器

**配置示例：**
```json
{
  "channels": {
    "pico_client": {
      "enabled": true,
      "url": "ws://remote-server.com:8080/pico/",
      "token": "pico-xxxxxx..."
    }
  }
}
```

---

## 使用场景对比

### 场景 1：本地 AI Agent（使用 `pico` 服务器）

```
网页客户端          PicoClaw Gateway
    ↓                   ↓
    └─── WebSocket ──→ pico 频道（服务器）
             ↑            ↓
             └── 消息 ←── MessageBus
                          ↓
                      AI 引擎处理
```

**配置：**
- 启用 `pico` 频道
- 设置监听端口和 Token
- 网页通过 WebSocket 连接到 Gateway

---

### 场景 2：连接到其他 PicoClaw 实例（使用 `pico_client` 客户端）

```
本地 Gateway               远程 Gateway
    ↓                          ↑
    └─── WebSocket ────────────┘
    pico_client 频道      pico 频道（服务器）
         ↑                      ↓
         └── MessageBus ←────────
                ↓
          AI 引擎处理
```

**配置：**
- 启用 `pico_client` 频道
- 配置远程服务器 URL
- 本地 Gateway 主动连接到远程 Gateway

---

### 场景 3：混合模式（同时使用两个频道）

```
外部客户端 A              内部系统
    ↓                        ↓
    └─ pico (Server) ←─ pico_client (Client)
         ↑                    ↑
         └─── MessageBus ─────┘
              ↓
          AI 引擎

- 对外：作为 WebSocket 服务器（pico）
- 对内：连接到其他系统（pico_client）
- 消息在两个频道之间流转
```

---

## Pico 协议（Protocol）

**文件位置：** `pkg/channels/pico/protocol.go`

### 消息类型

**客户端 → 服务器：**
- `message.send` - 发送消息
- `media.send` - 发送媒体文件
- `ping` - 心跳

**服务器 → 客户端：**
- `message.create` - 新建消息
- `message.update` - 更新消息
- `media.create` - 新建媒体
- `typing.start` - 开始输入
- `typing.stop` - 停止输入
- `error` - 错误信息
- `pong` - 心跳回应

### 消息格式

```go
type PicoMessage struct {
    Type      string         `json:"type"`           // 消息类型
    ID        string         `json:"id,omitempty"`   // 消息 ID
    SessionID string         `json:"session_id,omitempty"` // 会话 ID
    Timestamp int64          `json:"timestamp,omitempty"`  // 时间戳
    Payload   map[string]any `json:"payload,omitempty"`    // 负载数据
}
```

**示例：**
```json
{
  "type": "message.send",
  "id": "msg-123",
  "session_id": "session-456",
  "timestamp": 1649000000000,
  "payload": {
    "content": "Hello, AI!",
    "format": "text"
  }
}
```

---

## 核心差异总结

| 方面 | `pico` | `pico_client` |
|------|-------|--------------|
| **初始化** | 启动 HTTP 服务器 | 主动拨号连接 |
| **连接管理** | 多个客户端连接 | 单一服务器连接 |
| **消息流向** | 接收→处理→发送 | 转发←→远程 |
| **故障恢复** | 等待客户端重连 | 自动重连循环 |
| **用途** | 提供本地接口 | 连接外部系统 |
| **监听** | HTTP 端口 | N/A（主动发起） |
| **认证** | 验证客户端 Token | 使用服务器 Token |

---

## 代码架构图

```
pkg/channels/pico/
├── init.go                  ← 注册两个频道工厂
│   ├── RegisterFactory("pico", NewPicoChannel)
│   └── RegisterFactory("pico_client", NewPicoClientChannel)
│
├── pico.go                  ← PicoChannel 服务器实现
│   ├── NewPicoChannel()
│   ├── Start()              → 启动 WebSocket 服务器
│   ├── handleWebSocket()    → 处理升级请求
│   ├── readLoop()           → 读取客户端消息
│   ├── pingLoop()           → 心跳检测
│   └── broadcastToSession() → 广播到会话
│
├── client.go                ← PicoClientChannel 客户端实现
│   ├── NewPicoClientChannel()
│   ├── Start()              → 主动拨号连接
│   ├── dial()               → WebSocket 连接
│   ├── reconnectLoop()      → 自动重连
│   └── readLoop()           → 读取服务器消息
│
└── protocol.go              ← 通讯协议定义
    ├── PicoMessage 类型
    ├── 消息类型常量
    └── 消息构造函数
```

---

## 配置参考

### PicoConfig（pico 服务器）
```go
type PicoConfig struct {
    Enabled     bool
    Token       Secret           // 认证 Token
    AllowOrigins []string         // 允许的 Origin
    MaxConnPerSession int         // 每个会话最大连接数
}
```

### PicoClientConfig（pico_client 客户端）
```go
type PicoClientConfig struct {
    Enabled bool
    URL     string              // 远程服务器 URL（必需）
    Token   Secret              // 认证 Token
}
```

---

## 何时选择哪个频道？

### 使用 `pico` 频道当：
- ✅ 需要对外提供 WebSocket 接口
- ✅ 支持多个客户端同时连接
- ✅ 作为中央 AI Gateway 接收请求
- ✅ 运行在可以暴露 HTTP 端口的服务器上

### 使用 `pico_client` 频道当：
- ✅ 需要连接到另一个 PicoClaw 实例
- ✅ 需要出站连接（不接受入站）
- ✅ 与远程系统通讯
- ✅ 需要自动重连能力

### 同时使用两个当：
- ✅ 建立分布式 AI 系统
- ✅ 对外提供服务同时连接内部系统
- ✅ 消息需要跨多个 Gateway 转发

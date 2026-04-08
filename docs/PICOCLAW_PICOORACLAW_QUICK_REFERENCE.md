# PicoClaw vs PicoOraClaw - 快速参考

## 📊 核心对比矩阵

```
┌─────────────────────┬──────────────────┬──────────────────┐
│ 维度                │ PicoClaw         │ PicoOraClaw      │
├─────────────────────┼──────────────────┼──────────────────┤
│ 定位                │ 个人完整套件     │ 企业数据库版本   │
│ 存储                │ JSONL文件        │ Oracle数据库     │
│ 向量搜索            │ ✗                │ ✅               │
│ GUI/TUI             │ ✅ 完整          │ ✗ CLI only       │
│ 多用户支持          │ ✗                │ ✅               │
│ Go文件数            │ 554              │ 131              │
│ 依赖数              │ 46               │ 17               │
│ 代码行数            │ 较多             │ 精简             │
│ 部署复杂度          │ 🟢 低            │ 🟡 中            │
│ 学习曲线            │ 🟡 中            │ 🟢 低            │
│ 可维护性            │ 🟡 中等          │ 🟢 高            │
│ 扩展性              │ 🟡 中等          │ 🟢 高            │
└─────────────────────┴──────────────────┴──────────────────┘
```

## 🔑 关键差异速查

### 存储方案
```
PicoClaw:    文件系统 (JSONL)
             └─ 本地目录结构
             └─ 每会话两个文件
             └─ 简单易用
             └─ 无水平扩展

PicoOraClaw: Oracle数据库
             └─ 7张表 Schema
             └─ 向量嵌入支持
             └─ 连接池管理
             └─ 高可用部署
```

### 命令行工具
```
PicoClaw:    分模块架构 (14个子目录)
             - cmd/picoclaw/internal/
             - cmd/picoclaw-launcher-tui/

PicoOraClaw: 单文件实现 (2572行)
             - cmd/picooraclaw/main.go
             - 新增: setup-oracle, oracle-inspect, seed-demo
```

### 依赖对比
```
PicoClaw (46个):
  ├─ GUI框架: fyne, tview, tcell, systray (4个)
  ├─ 数据库: sqlite, sqlc (2个)
  ├─ 通信: whatsapp, webrtc, mcp (3个)
  ├─ 音频: (内含模块)
  ├─ 文件: filetype (1个)
  └─ 其他: cobra, markdown, pty, clipboard等

PicoOraClaw (17个):
  ├─ 核心: cobra, readline (2个)
  ├─ 数据库: go-ora/v2, sqlmock (2个)
  └─ 其他: 常见库
```

### 模块差异

**仅PicoClaw有**
```
pkg/audio/              - 音频处理
pkg/credential/         - 凭证管理
pkg/fileutil/           - 文件工具
pkg/gateway/            - API网关
pkg/identity/           - 身份验证
pkg/mcp/                - MCP协议
pkg/media/              - 媒体处理
pkg/memory/             - JSONL存储
pkg/pid/                - 进程管理
pkg/updater/            - 自更新
web/                    - Web UI
docker/                 - 多Dockerfile
examples/               - 示例代码
cmd/picoclaw-launcher-tui/  - TUI应用
```

**仅PicoOraClaw有**
```
pkg/oracle/             - 22个文件，完整Oracle集成
pkg/voice/              - 语音处理
deploy/oci/             - OCI部署配置
oci-genai/              - Python OCI客户端
cmd/picooraclaw/main.go - 单文件2572行
```

## ⚡ 快速命令对比

### 启动
```bash
# PicoClaw - 二选一
$ picoclaw agent interact --user-id alice      # CLI模式
$ picoclaw-launcher-tui                        # TUI模式

# PicoOraClaw - 仅CLI
$ picooraclaw agent interact --agent-id alice
```

### 初始化
```bash
# PicoClaw
$ picoclaw onboard

# PicoOraClaw
$ picooraclaw onboard
$ picooraclaw setup-oracle --host ... --user ... --password ...
```

### 查看数据
```bash
# PicoClaw
$ cat ~/.picoclaw/workspace/memory/{session}/*.jsonl

# PicoOraClaw
$ picooraclaw oracle-inspect --agent-id alice --table PICO_MEMORIES
```

### 迁移
```bash
# PicoClaw → PicoOraClaw
$ picooraclaw migrate from-picoclaw \
    --source ~/.picoclaw/workspace \
    --agent-id alice \
    --dry-run                           # 先预检查
```

## 📁 配置位置

```
PicoClaw:       ~/.picoclaw/
                ├── config.json
                ├── workspace/
                │   ├── memory/
                │   ├── skills/
                │   └── AGENT.md

PicoOraClaw:    ~/.picooraclaw/
                ├── config.json (含Oracle配置)
                └── workspace/
```

## 🗄️ 数据库Schema对比

### PicoClaw (文件系统)
```
~/.picoclaw/workspace/memory/
├── session_key_1.jsonl          # 消息记录
├── session_key_1.meta.json      # 元数据（summary, offset）
├── session_key_2.jsonl
└── session_key_2.meta.json
```

### PicoOraClaw (Oracle)
```
PICO_META           元数据
PICO_MEMORIES       记忆 + 向量
PICO_DAILY_NOTES    日记 + 向量
PICO_SESSIONS       会话
PICO_STATE          状态
PICO_CONFIG         配置
PICO_PROMPTS        提示词
PICO_TRANSCRIPTS    转录
```

## 🎯 何时选择

### 选择 PicoClaw
- 个人或小团队使用
- 需要GUI/TUI界面
- 完全离线使用
- 快速原型开发
- 隐私优先级最高

### 选择 PicoOraClaw
- 企业级部署
- 多用户/多代理
- 需要向量搜索
- 已有Oracle基础设施
- 高可用性要求
- 合规审计需求

## 📈 性能特性

### 查询性能
```
                    PicoClaw        PicoOraClaw
新增记忆            10-50ms         50-200ms (含向量化)
查询历史            100-500ms       20-100ms (有索引)
相似搜索            全表扫描        <10ms (向量索引)
并发连接            1-5             25+
```

### 存储效率
```
1000条记录:
PicoClaw (JSONL):       ~500KB (纯文本)
PicoOraClaw (Oracle):   ~200KB (二进制) + 向量
```

## 🔧 配置关键差异

### PicoClaw
```json
{
  "agents": [],
  "channels": [],
  "modelList": {},
  "gateway": {},
  "hooks": {
    "onMessage": [],
    "onTurn": []
  }
}
```

### PicoOraClaw
```json
{
  "agents": [],
  "channels": [],
  "providers": {},
  "gateway": {},
  "oracle": {
    "host": "oracle.example.com",
    "port": 1521,
    "service": "ORCL",
    "username": "pico",
    "poolMaxOpen": 25
  }
}
```

## 🚀 部署差异

### PicoClaw
```
单机部署
├─ 无外部依赖
├─ 启动即用
└─ 自给自足
```

### PicoOraClaw
```
需要基础设施
├─ Oracle数据库 (11g+)
├─ 网络连通性
├─ 存储空间
└─ 备份策略
```

## 📦 Go依赖top 5

**PicoClaw**
1. github.com/spf13/cobra (CLI框架)
2. github.com/rivo/tview (TUI)
3. fyne.io/systray (系统托盘)
4. go.mau.fi/whatsmeow (WhatsApp)
5. modernc.org/sqlite (本地DB)

**PicoOraClaw**
1. github.com/spf13/cobra (CLI框架)
2. github.com/sijms/go-ora/v2 (Oracle驱动)
3. github.com/chzyer/readline (交互)
4. github.com/DATA-DOG/go-sqlmock (Mock)
5. 其他常见库

## 🔄 迁移路径

```
PicoClaw (JSONL)
    ↓
    ├─ [备份] ~/.picoclaw
    ├─ [导出] 会话数据
    ├─ [部署] Oracle环境
    ├─ [转换] 数据格式
    ├─ [导入] 到Oracle
    └─ [验证] 数据完整性
    ↓
PicoOraClaw (Oracle + Vector)
```

## 💡 架构演进

```
PicoClaw:
  本地优先 → 完整功能 → 快速开发

PicoOraClaw:
  企业就绪 → 数据驱动 → 可扩展
```

## 📞 关键联系点

| 维度 | 最大差异 |
|------|--------|
| 存储 | JSONL ↔ Oracle (向量支持) |
| 接口 | CLI + TUI ↔ CLI only |
| 扩展 | 文件系统 ↔ 数据库 |
| 维护 | 独立 ↔ 依赖外部基础设施 |

---

**快速参考卡** - 完整详情见 `PICOCLAW_VS_PICOORACLAW.md`

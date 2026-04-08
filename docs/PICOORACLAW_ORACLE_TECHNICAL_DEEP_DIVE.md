# PicoOraClaw Oracle集成 - 技术深度解析

> 本文档深入分析PicoOraClaw中Oracle集成的技术实现细节，适合开发者和架构师参考。

## 目录

1. [Oracle模块架构](#oracle模块架构)
2. [数据库Schema设计](#数据库schema设计)
3. [向量嵌入系统](#向量嵌入系统)
4. [连接管理与优化](#连接管理与优化)
5. [存储实现对比](#存储实现对比)
6. [迁移机制](#迁移机制)
7. [性能优化](#性能优化)
8. [故障处理](#故障处理)

---

## Oracle模块架构

### 文件映射表

```
pkg/oracle/
├── schema.go (★)              - 数据库初始化和DDL
├── connection.go              - 连接管理和连接池
├── memory_store.go (★)        - 记忆存储实现 (核心)
├── session_store.go           - 会话状态存储
├── state_store.go             - 应用状态存储
├── config_store.go            - 配置数据存储
├── prompt_store.go            - 提示词存储
├── embedding.go (★)           - 向量嵌入服务
├── vector_store.go (★)        - 向量存储和查询
├── validate.go                - 数据验证
├── interface_compliance_test.go - 接口检验
├── integration_test.go        - 集成测试
└── test_helpers_test.go       - 测试工具
```

### 核心流程

```
┌─────────────────────────────────────────────────────┐
│ Application Layer                                   │
├─────────────────────────────────────────────────────┤
│ Agent / Skills / Tools                              │
├─────────────────────────────────────────────────────┤
│ Storage Interface                                   │
│ ├─ MemoryStore                                      │
│ ├─ SessionStore                                     │
│ ├─ StateStore                                       │
│ └─ ConfigStore                                      │
├─────────────────────────────────────────────────────┤
│ Oracle Implementation Layer                         │
│ ├─ embedding.go (ONNX向量生成)                      │
│ ├─ vector_store.go (向量搜索)                       │
│ └─ {type}_store.go (表操作)                         │
├─────────────────────────────────────────────────────┤
│ Connection Pool Layer                               │
│ ├─ 连接池管理                                       │
│ ├─ 健康检查                                         │
│ └─ 自动重连                                         │
├─────────────────────────────────────────────────────┤
│ Go-ORA Driver (github.com/sijms/go-ora/v2)          │
├─────────────────────────────────────────────────────┤
│ Oracle Server (11g+, 21c, Autonomous DB)            │
└─────────────────────────────────────────────────────┘
```

### 接口设计

所有存储实现都遵循统一接口（类似PicoClaw）：

```go
// MemoryStore 接口
type MemoryStore interface {
    // 基础操作
    AddMessage(ctx context.Context, sessionKey, role, content string) error
    AddFullMessage(ctx context.Context, sessionKey string, msg Message) error
    GetHistory(ctx context.Context, sessionKey string) ([]Message, error)

    // 高级操作
    ReadShortTerm() string
    WriteShortTerm(content string) error
    ReadLongTerm() string
    WriteLongTerm(content string) error

    // 向量搜索 (Oracle特有)
    SearchByEmbedding(ctx context.Context, query string, limit int) ([]Message, error)
    RecallMemories(ctx context.Context, importance float64) ([]Message, error)

    // 管理操作
    SetSummary(ctx context.Context, sessionKey, summary string) error
    TruncateHistory(ctx context.Context, sessionKey string, keepLast int) error
    Compact(ctx context.Context, sessionKey string) error
    Close() error
}
```

---

## 数据库Schema设计

### Table: PICO_META (元数据)

```sql
CREATE TABLE PICO_META (
    meta_id         NUMBER PRIMARY KEY,
    agent_id        VARCHAR2(255) NOT NULL,
    meta_key        VARCHAR2(255) NOT NULL,
    meta_value      CLOB,
    version         NUMBER DEFAULT 1,
    created_at      TIMESTAMP DEFAULT SYSDATE,
    updated_at      TIMESTAMP DEFAULT SYSDATE,

    UNIQUE (agent_id, meta_key),
    FOREIGN KEY (agent_id) REFERENCES PICO_AGENTS(agent_id)
);
```

**用途**: 存储代理元数据、版本信息、配置

### Table: PICO_MEMORIES (★ 核心表)

```sql
CREATE TABLE PICO_MEMORIES (
    memory_id           NUMBER PRIMARY KEY,
    agent_id            VARCHAR2(255) NOT NULL,
    session_id          VARCHAR2(255),

    -- 内容存储
    content             CLOB NOT NULL,
    content_type        VARCHAR2(50),           -- 'text', 'image', 'audio'

    -- 向量嵌入 (Oracle Vector)
    content_vector      VECTOR(384, FLOAT32),  -- 384维向量

    -- 元数据
    importance          FLOAT(5, 2) DEFAULT 0.5,   -- 0.0 - 1.0
    accessed_count      NUMBER DEFAULT 0,
    first_accessed_at   TIMESTAMP,
    last_accessed_at    TIMESTAMP,
    created_at          TIMESTAMP DEFAULT SYSDATE,

    -- 索引
    CONSTRAINT fk_mem_agent FOREIGN KEY (agent_id)
        REFERENCES PICO_AGENTS(agent_id),
    CONSTRAINT fk_mem_session FOREIGN KEY (session_id)
        REFERENCES PICO_SESSIONS(session_id)
);

-- 向量索引
CREATE INDEX pico_mem_vector_idx ON PICO_MEMORIES (content_vector)
    INDEXTYPE IS VECTOR_INDEX
    PARAMETERS ('DISTANCE=COSINE');

-- 性能索引
CREATE INDEX pico_mem_agent_idx ON PICO_MEMORIES(agent_id);
CREATE INDEX pico_mem_importance_idx ON PICO_MEMORIES(importance DESC);
CREATE INDEX pico_mem_accessed_idx ON PICO_MEMORIES(last_accessed_at DESC);
```

**特点**:
- VECTOR类型支持（Oracle 23ai+）
- COSINE距离度量
- 复合索引优化查询
- 时间衰减权重

### Table: PICO_DAILY_NOTES (日记 + 向量)

```sql
CREATE TABLE PICO_DAILY_NOTES (
    note_id         NUMBER PRIMARY KEY,
    agent_id        VARCHAR2(255) NOT NULL,
    note_date       DATE NOT NULL,

    -- 内容
    content         CLOB NOT NULL,
    summary         CLOB,

    -- 向量和情感分析
    content_vector  VECTOR(384, FLOAT32),
    sentiment       VARCHAR2(10),               -- 'positive', 'neutral', 'negative'
    sentiment_score FLOAT(3, 2),

    -- 元数据
    mood            VARCHAR2(50),
    tags            VARCHAR2(1000),

    created_at      TIMESTAMP DEFAULT SYSDATE,
    updated_at      TIMESTAMP DEFAULT SYSDATE,

    CONSTRAINT fk_note_agent FOREIGN KEY (agent_id)
        REFERENCES PICO_AGENTS(agent_id),
    UNIQUE (agent_id, note_date)
);

-- 向量索引
CREATE INDEX pico_note_vector_idx ON PICO_DAILY_NOTES (content_vector)
    INDEXTYPE IS VECTOR_INDEX;
```

### Table: PICO_SESSIONS (会话)

```sql
CREATE TABLE PICO_SESSIONS (
    session_id      VARCHAR2(255) PRIMARY KEY,
    agent_id        VARCHAR2(255) NOT NULL,

    -- 会话元信息
    title           VARCHAR2(500),
    description     CLOB,
    session_type    VARCHAR2(50),               -- 'chat', 'voice', 'planning'

    -- 统计信息
    message_count   NUMBER DEFAULT 0,
    turn_count      NUMBER DEFAULT 0,

    -- 状态
    status          VARCHAR2(50),               -- 'active', 'archived', 'completed'

    -- 时间戳
    created_at      TIMESTAMP DEFAULT SYSDATE,
    updated_at      TIMESTAMP DEFAULT SYSDATE,
    archived_at     TIMESTAMP,

    CONSTRAINT fk_sess_agent FOREIGN KEY (agent_id)
        REFERENCES PICO_AGENTS(agent_id)
);

CREATE INDEX pico_sess_agent_idx ON PICO_SESSIONS(agent_id, created_at DESC);
CREATE INDEX pico_sess_status_idx ON PICO_SESSIONS(status);
```

### Table: PICO_STATE (应用状态)

```sql
CREATE TABLE PICO_STATE (
    state_id        NUMBER PRIMARY KEY,
    agent_id        VARCHAR2(255) NOT NULL,

    state_key       VARCHAR2(255) NOT NULL,
    state_value     CLOB,

    version         NUMBER DEFAULT 0,
    updated_at      TIMESTAMP DEFAULT SYSDATE,

    CONSTRAINT fk_state_agent FOREIGN KEY (agent_id)
        REFERENCES PICO_AGENTS(agent_id),
    UNIQUE (agent_id, state_key)
);
```

### Table: PICO_CONFIG (配置)

```sql
CREATE TABLE PICO_CONFIG (
    config_id       NUMBER PRIMARY KEY,
    agent_id        VARCHAR2(255) NOT NULL,

    config_key      VARCHAR2(255) NOT NULL,
    config_value    CLOB NOT NULL,

    config_type     VARCHAR2(50),               -- 'string', 'json', 'number'
    is_sensitive    NUMBER DEFAULT 0,           -- 0=false, 1=true

    updated_at      TIMESTAMP DEFAULT SYSDATE,
    updated_by      VARCHAR2(255),

    CONSTRAINT fk_config_agent FOREIGN KEY (agent_id)
        REFERENCES PICO_AGENTS(agent_id),
    UNIQUE (agent_id, config_key)
);
```

### Table: PICO_PROMPTS (提示词)

```sql
CREATE TABLE PICO_PROMPTS (
    prompt_id       NUMBER PRIMARY KEY,
    agent_id        VARCHAR2(255) NOT NULL,

    prompt_name     VARCHAR2(255) NOT NULL,
    prompt_content  CLOB NOT NULL,

    -- 分类
    category        VARCHAR2(100),              -- 'system', 'user', 'example'
    version         NUMBER DEFAULT 1,

    -- 向量化 (用于提示词搜索)
    prompt_vector   VECTOR(384, FLOAT32),

    is_active       NUMBER DEFAULT 1,
    created_at      TIMESTAMP DEFAULT SYSDATE,
    updated_at      TIMESTAMP DEFAULT SYSDATE,

    CONSTRAINT fk_prompt_agent FOREIGN KEY (agent_id)
        REFERENCES PICO_AGENTS(agent_id),
    UNIQUE (agent_id, prompt_name)
);
```

### Table: PICO_TRANSCRIPTS (转录)

```sql
CREATE TABLE PICO_TRANSCRIPTS (
    transcript_id   NUMBER PRIMARY KEY,
    agent_id        VARCHAR2(255) NOT NULL,
    session_id      VARCHAR2(255),

    -- 源信息
    source_type     VARCHAR2(50),               -- 'voice', 'video', 'audio'
    source_file     VARCHAR2(1000),

    -- 转录内容
    transcript_text CLOB NOT NULL,
    transcript_json CLOB,                       -- 带时间戳的JSON格式

    -- 向量化
    transcript_vector VECTOR(384, FLOAT32),

    -- 质量指标
    confidence      FLOAT(3, 2),
    language        VARCHAR2(10) DEFAULT 'en',

    created_at      TIMESTAMP DEFAULT SYSDATE,
    processed_at    TIMESTAMP,

    CONSTRAINT fk_transcript_agent FOREIGN KEY (agent_id)
        REFERENCES PICO_AGENTS(agent_id),
    CONSTRAINT fk_transcript_session FOREIGN KEY (session_id)
        REFERENCES PICO_SESSIONS(session_id)
);
```

### 索引策略

```sql
-- 向量索引 (VECTOR_INDEX 类型)
CREATE INDEX pico_mem_vec_idx ON PICO_MEMORIES(content_vector)
    INDEXTYPE IS VECTOR_INDEX
    PARAMETERS ('DISTANCE=COSINE, ACCURACY=80');

-- 复合B-Tree索引 (高频查询)
CREATE INDEX pico_mem_composite_idx ON PICO_MEMORIES(
    agent_id,
    importance DESC,
    last_accessed_at DESC
);

-- 分区索引 (大表优化)
CREATE INDEX pico_session_part_idx ON PICO_SESSIONS(agent_id, created_at)
    LOCAL PARTITION BY RANGE (created_at) (
        PARTITION p_2024 VALUES LESS THAN (TO_DATE('2025-01-01', 'YYYY-MM-DD')),
        PARTITION p_2025 VALUES LESS THAN (TO_DATE('2026-01-01', 'YYYY-MM-DD')),
        PARTITION p_future VALUES LESS THAN (MAXVALUE)
    );
```

---

## 向量嵌入系统

### 架构

```go
// embedding.go - 向量生成核心

type EmbeddingService struct {
    modelPath   string              // ONNX模型路径
    modelName   string              // 模型标识
    dimension   int                 // 向量维数 (384)
    session     *ort.Session        // ONNX Runtime会话
    mu          sync.RWMutex
}

type EmbeddingBatch struct {
    texts      []string
    embeddings [][]float32          // 批量向量结果
    errors     []error
}
```

### 支持的模型

| 模型 | 维数 | 参数量 | 适用场景 |
|------|------|--------|---------|
| sentence-transformers/all-MiniLM-L6-v2 | 384 | 22M | 通用文本 (推荐) |
| sentence-transformers/all-mpnet-base-v2 | 768 | 109M | 高精度语义 |
| intfloat/e5-small-v2 | 384 | 33M | 检索任务 |
| intfloat/e5-base-v2 | 768 | 109M | 高质量检索 |

### 嵌入流程

```
输入文本
    ↓
分词 (Tokenization)
    ↓
ONNX模型推理
    ↓
384维向量 (FLOAT32)
    ↓
归一化 (L2 norm)
    ↓
存储到PICO_MEMORIES.content_vector
```

### Go实现片段

```go
func (es *EmbeddingService) EmbedText(ctx context.Context, text string) ([]float32, error) {
    // 文本预处理
    cleanText := preprocessing.Clean(text)

    // 分词
    tokens := tokenizer.Encode(cleanText)

    // 转换为ONNX输入格式
    inputIDs := convertToONNXInput(tokens)

    // 模型推理
    outputs, err := es.session.Run(nil, inputIDs, nil)
    if err != nil {
        return nil, fmt.Errorf("onnx inference failed: %w", err)
    }

    // 提取输出（取CLS token的768维 → 降维到384）
    embedding := extractEmbedding(outputs[0])

    // L2归一化
    normalized := normalize(embedding)

    return normalized, nil
}

// 批量处理优化
func (es *EmbeddingService) EmbedBatch(ctx context.Context, texts []string) (*EmbeddingBatch, error) {
    // 分批处理大量文本
    const batchSize = 32

    allEmbeddings := make([][]float32, len(texts))

    for i := 0; i < len(texts); i += batchSize {
        end := i + batchSize
        if end > len(texts) {
            end = len(texts)
        }

        batch := texts[i:end]
        embeddings, err := es.embedBatch(ctx, batch)
        if err != nil {
            // 记录但继续处理
            continue
        }

        copy(allEmbeddings[i:end], embeddings)
    }

    return &EmbeddingBatch{
        texts:      texts,
        embeddings: allEmbeddings,
    }, nil
}
```

### 性能特性

```
单条文本嵌入:      20-50ms (取决于文本长度)
批量嵌入 (32条):   100-150ms
并发嵌入 (8线程):   Parallel execution
缓存命中:          <1ms (带hash缓存)
```

### 内存使用

```
ONNX模型加载:      ~200MB (对于384维模型)
批处理缓存:        ~50MB (32条文本)
向量存储:          384 * 4 bytes = 1536 bytes per vector
```

---

## 连接管理与优化

### 连接池配置 (connection.go)

```go
type ConnectionConfig struct {
    // 连接参数
    Host              string
    Port              int              // 默认1521
    Service           string           // 服务名/SID
    Username          string
    Password          string

    // 连接池参数
    MaxOpenConns      int              // 默认25
    MaxIdleConns      int              // 默认5
    ConnMaxLifetime   time.Duration    // 默认1小时
    ConnMaxIdleTime   time.Duration    // 默认15分钟

    // 企业功能
    IsADB             bool             // Autonomous Database标志
    WalletPath        string           // ADB钱包路径
    Threads           int              // 并发线程数
}
```

### DSN构建

```go
// 本地数据库
dsn := fmt.Sprintf("%s/%s@%s:%d/%s",
    username, password, host, port, service)
// 结果: pico/password@oracle.example.com:1521/ORCL

// Autonomous Database (ADB)
dsn := fmt.Sprintf("oracle://%s:%s@%s?walletDir=%s&wallet=%s",
    username, password, service, walletPath, walletFile)
```

### 连接生命周期

```
初始化
    ↓
sql.Open() - 延迟连接 (go-ora驱动特性)
    ↓
首次查询时 - 建立连接
    ↓
连接状态维护
    ├─ 定期心跳检查 (Ping)
    ├─ 自动重连
    └─ 超时处理
    ↓
关闭
```

### 健康检查

```go
func (cp *ConnectionPool) HealthCheck(ctx context.Context) error {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    err := cp.db.PingContext(ctx)
    if err != nil {
        log.Warn("Health check failed, attempting reconnect", "error", err)
        return cp.reconnect()
    }
    return nil
}

func (cp *ConnectionPool) reconnect() error {
    cp.db.Close()

    newDB, err := sql.Open("oracle", cp.dsn)
    if err != nil {
        return err
    }

    cp.db = newDB
    return cp.HealthCheck(context.Background())
}
```

### 性能优化

```sql
-- 1. 启用查询缓存
ALTER SESSION SET QUERY_RESULT_CACHE_MODE = FORCE;

-- 2. 并行执行
ALTER SESSION SET PARALLEL_FORCE_LOCAL = TRUE;
ALTER SESSION SET PARALLEL_DEGREE_POLICY = 'ADAPTIVE';

-- 3. 向量搜索优化
-- 使用approximate nearest neighbor search
SELECT /*+ VECTOR_DISTANCE_INDEX(m content_vector) */
    *
FROM PICO_MEMORIES m
WHERE agent_id = ?
ORDER BY VECTOR_DISTANCE(content_vector, ?, 'COSINE')
FETCH FIRST 10 ROWS ONLY;
```

---

## 存储实现对比

### 接口一致性

两个版本都实现相同的接口，但底层存储完全不同：

```go
// 接口定义 (pkg/memory/store.go - PicoClaw)
type Store interface {
    AddMessage(ctx, sessionKey, role, content) error
    GetHistory(ctx, sessionKey) ([]Message, error)
    SetSummary(ctx, sessionKey, summary) error
    Close() error
}

// PicoClaw实现 (JSONL)
type JSONLStore struct {
    baseDir string
}

// PicoOraClaw实现 (Oracle)
type OracleMemoryStore struct {
    db        *sql.DB
    agentID   string
    embedding *EmbeddingService
}
```

### 性能对比

#### 新增消息

**PicoClaw (文件系统)**
```
1. 打开文件 (O_APPEND)       ~1ms
2. Marshal JSON             ~2ms
3. Write & Sync             ~5-10ms
4. 更新元数据文件           ~2ms
总计: 10-15ms
```

**PicoOraClaw (Oracle)**
```
1. 生成向量嵌入             ~30ms
2. INSERT + 向量索引        ~50ms
3. 事务提交                 ~30ms
总计: 110ms (首次), 50ms (缓存)
```

#### 查询历史

**PicoClaw**
```
1. 读取JSONL文件            100-500ms (取决于文件大小)
2. 逐行反序列化             ~10ms/行
3. 内存中过滤               ~1ms
总计: 100-1000ms (线性增长)
```

**PicoOraClaw**
```
1. SQL查询 (带索引)         10-50ms
2. 游标提取                 ~5ms
3. 反序列化                 ~5ms
总计: 20-100ms (对数增长，有索引）
```

#### 相似性搜索

**PicoClaw**
```
无原生支持 - 需要应用层实现
1. 加载所有消息到内存       100-1000ms
2. 计算相似度 (所有对)      O(n²) 算法
3. 排序                     ~10ms
总计: 秒级（不适合大规模）
```

**PicoOraClaw**
```
向量索引搜索
1. 生成查询向量             ~20ms
2. 向量索引查询             <10ms (使用VECTOR_INDEX)
3. 排序和限制               ~2ms
总计: 32ms (恒定时间)
```

---

## 迁移机制

### 迁移命令

```bash
$ picooraclaw migrate from-picoclaw \
    --source ~/.picoclaw/workspace \
    --agent-id alice \
    --dry-run                    # 可选：预检查
```

### 迁移步骤

```
PICO_META表
└─ schema_version
└─ last_migration_time

迁移过程：
1. 验证源数据完整性
2. 创建临时表
3. 从JSONL读取并转换
4. 生成向量嵌入 (批量)
5. 插入到Oracle
6. 验证行数
7. 构建索引
8. 清理临时数据
9. 更新元数据
```

### 数据转换

**JSONL → Oracle**

```go
// 源格式 (JSONL)
{"role": "user", "content": "...", "timestamp": "2024-04-08T10:00:00Z"}
{"role": "assistant", "content": "...", "timestamp": "2024-04-08T10:00:30Z"}

// 转换步骤
1. 解析JSON
2. 生成向量 (embedding.EmbedText)
3. 计算importance (基于timestamp/访问频率)
4. 构造INSERT语句
5. 批量执行 (txn.ExecContext)

// 目标格式 (Oracle)
INSERT INTO PICO_MEMORIES (
    memory_id, agent_id, content, content_vector,
    importance, created_at
) VALUES (
    seq_memory_id.NEXTVAL, 'alice', '...',
    TO_VECTOR('[0.123, 0.456, ...]'),
    0.8, SYSDATE
);
```

### 恢复与回滚

```bash
# 回滚到迁移前状态
$ picooraclaw migrate rollback --agent-id alice

# 从备份恢复
$ rman restore database from tag BEFORE_MIGRATION;
```

---

## 性能优化

### 查询优化

**优化前** (全表扫描)
```sql
SELECT * FROM PICO_MEMORIES
WHERE agent_id = 'alice'
  AND importance > 0.5
ORDER BY created_at DESC;

-- 执行计划: FULL TABLE SCAN - 1000ms
```

**优化后** (复合索引)
```sql
-- 创建索引
CREATE INDEX pico_mem_opt_idx ON PICO_MEMORIES(
    agent_id,
    importance DESC,
    created_at DESC
);

-- 同样查询
SELECT * FROM PICO_MEMORIES
WHERE agent_id = 'alice'
  AND importance > 0.5
ORDER BY created_at DESC;

-- 执行计划: FAST INDEX RANGE SCAN - 10-20ms
```

### 向量搜索优化

**基础实现**
```go
// 遍历所有向量，计算距离
SELECT memory_id, content
FROM PICO_MEMORIES
WHERE agent_id = 'alice'
ORDER BY VECTOR_DISTANCE(content_vector, ?, 'COSINE')
LIMIT 10;
-- 时间: 100-500ms (没有索引)
```

**优化后** (向量索引)
```go
-- 创建向量索引
CREATE INDEX pico_vec_idx ON PICO_MEMORIES (content_vector)
    INDEXTYPE IS VECTOR_INDEX
    PARAMETERS ('DISTANCE=COSINE');

-- 同样查询
SELECT /*+ VECTOR_DISTANCE_INDEX(m content_vector) */
    memory_id, content
FROM PICO_MEMORIES m
WHERE agent_id = 'alice'
ORDER BY VECTOR_DISTANCE(content_vector, ?, 'COSINE')
LIMIT 10;
-- 时间: 5-10ms (使用向量索引)
```

### 批处理优化

```go
// 单条INSERT (性能差)
for _, msg := range messages {
    _, err := db.ExecContext(ctx,
        "INSERT INTO PICO_MEMORIES (...) VALUES (...)",
        msg)
}
// 时间: 100 * 50ms = 5s

// 批量INSERT
tx := db.BeginTx(ctx, nil)
stmt := tx.PrepareContext(ctx,
    "INSERT INTO PICO_MEMORIES (...) VALUES (...)")

for _, msg := range messages {
    stmt.ExecContext(ctx, msg)
}
stmt.Close()
tx.Commit()
// 时间: 200-500ms (相同数据)
```

### 缓存策略

```go
type CachedMemoryStore struct {
    db           *sql.DB
    cache        *lru.Cache        // LRU缓存
    embedCache   *lru.Cache        // 向量缓存
    cacheTTL     time.Duration
}

func (cms *CachedMemoryStore) GetHistory(ctx, sessionKey) {
    // 1. 查缓存
    if cached, ok := cms.cache.Get(sessionKey); ok {
        return cached, nil
    }

    // 2. 数据库查询
    result := db.Query(...)

    // 3. 放入缓存
    cms.cache.Set(sessionKey, result, cms.cacheTTL)

    return result, nil
}
```

---

## 故障处理

### 常见错误

| 错误 | 原因 | 解决 |
|------|------|------|
| ORA-12514 TNS listener 不知道服务 | 服务名错误 | 检查 LISTENER.ORA |
| ORA-01017 无效用户名/密码 | 认证失败 | 验证凭证 |
| ORA-04031 内存不足 | 连接池泄漏 | 调整池大小 |
| ORA-03106 致命通信协议错误 | 网络连接断裂 | 检查防火墙 |

### 连接故障自动恢复

```go
func (store *OracleMemoryStore) withRetry(fn func(*sql.DB) error) error {
    maxRetries := 3
    backoff := 100 * time.Millisecond

    for attempt := 0; attempt < maxRetries; attempt++ {
        err := fn(store.db)

        if err == nil {
            return nil
        }

        // 检查是否为临时错误
        if isTemporaryError(err) {
            time.Sleep(backoff)
            backoff *= 2
            continue
        }

        // 永久错误
        return err
    }

    return fmt.Errorf("max retries exceeded")
}
```

### 监控和告警

```go
type MetricsCollector struct {
    queryDuration    prometheus.Histogram
    connectionErrors prometheus.Counter
    cacheHitRate     prometheus.Gauge
}

// 在查询时记录
start := time.Now()
result, err := store.db.QueryContext(ctx, query)
duration := time.Since(start)
collector.queryDuration.Observe(duration.Seconds())

if err != nil {
    collector.connectionErrors.Inc()
}
```

---

## 最佳实践

### 1. 索引策略

```sql
-- 必须创建
CREATE INDEX on (agent_id);
CREATE INDEX on (created_at DESC);
CREATE INDEX on (importance DESC);
CREATE INDEX on (content_vector) INDEXTYPE IS VECTOR_INDEX;

-- 定期维护
ALTER INDEX pico_mem_composite_idx REBUILD ONLINE;
ANALYZE TABLE PICO_MEMORIES COMPUTE STATISTICS;
```

### 2. 连接池调优

```json
{
  "oracle": {
    "poolMaxOpen": 25,      // 小于Oracle的processes参数
    "poolMaxIdle": 5,       // 约为poolMaxOpen的1/5
    "connMaxLifetime": 3600, // 1小时，防止会话过期
    "connMaxIdleTime": 900   // 15分钟，清理空闲连接
  }
}
```

### 3. 向量嵌入维护

```bash
# 定期重新生成向量 (模型更新时)
$ picooraclaw oracle-inspect --regenerate-vectors \
    --model sentence-transformers/all-MiniLM-L6-v2

# 验证向量有效性
$ picooraclaw oracle-inspect --validate-vectors
```

### 4. 备份策略

```bash
# 每日备份
RMAN> BACKUP DATABASE PLUS ARCHIVELOG;

# 跨地域复制 (DataGuard)
$ dgmgrl
DGMGRL> CREATE CONFIGURATION pico_dg ...

# 快照备份
$ aws rds create-db-snapshot --db-instance-identifier picoclaw-db
```

---

**技术深度解析文档** - 更新于 2026-04-08

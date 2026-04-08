# Main Loop 循环条件详解

## 代码位置

`pkg/agent/loop.go:1883`

```go
for ts.currentIteration() < ts.agent.MaxIterations || len(pendingMessages) > 0 || func() bool {
    graceful, _ := ts.gracefulInterruptRequested()
    return graceful
}() {
```

---

## 核心逻辑

这个循环条件由 **3 个部分** 组成，用 **OR** 连接：

```
condition1 || condition2 || condition3
   ↓
满足任何一个 → 继续循环
全部不满足 → 退出循环
```

---

## 三个条件详解

### 条件 1️⃣：迭代次数限制

```go
ts.currentIteration() < ts.agent.MaxIterations
```

**含义：** 当前迭代次数 < 最大迭代次数

**例子：**
```
MaxIterations = 10（最多执行 10 次 LLM 推理）

迭代 1: currentIteration = 0 < 10 ✓ 继续
迭代 2: currentIteration = 1 < 10 ✓ 继续
...
迭代 10: currentIteration = 9 < 10 ✓ 继续
迭代 11: currentIteration = 10 < 10 ✗ 不满足此条件
         → 检查其他条件
```

**作用：**
- ✅ 限制 LLM 推理的最多次数
- ✅ 防止无限循环（如 LLM 不断调用工具）
- ✅ 节省 token 和计算资源

**什么时候触发？**
- LLM 调用工具 → 工具返回结果 → 继续推理 → 再次调用工具
- 如此循环，最多 10 次

---

### 条件 2️⃣：待处理消息队列

```go
len(pendingMessages) > 0
```

**含义：** 有待处理的消息（不是空队列）

**待处理消息包括：**

```
pendingMessages 队列包含：
├─ Steering 消息（用户指导）
│  └─ 用户在工具执行中途发送的新指令
│  └─ 例如："停止搜索，直接回答"
│
└─ SubTurn 结果
   └─ 子任务完成后的反馈
   └─ 例如："子任务返回 100 条搜索结果"
```

**作用：**
- ✅ 即使达到 MaxIterations，如果用户有新消息，仍继续处理
- ✅ 保证用户的指导命令不被忽略
- ✅ 支持实时交互和用户干预

**场景示例：**

```
场景：用户觉得 LLM 推理时间太长，想打断

时间线：
T1: 用户发送"你好，帮我查询..."
T2: AI 开始迭代 1、2、3...
T3: 第 9 次迭代时，用户发送"停止，直接回答"
     → 这条消息进入 pendingMessages 队列

T4: 第 10 次迭代完成
T5: currentIteration = 10，达到 MaxIterations
    → 但 len(pendingMessages) > 0（用户的"停止"消息）
    → 继续循环，处理用户的 Steering 消息

T6: 处理完"停止"消息后
    → currentIteration = 10 ✗
    → pendingMessages = [] ✗
    → graceful = false ✗
    → 退出循环
```

---

### 条件 3️⃣：优雅中断请求

```go
func() bool {
    graceful, _ := ts.gracefulInterruptRequested()
    return graceful
}()
```

**含义：** 用户是否请求了 Graceful Interrupt（优雅中断）

**什么是 Graceful Interrupt？**

```
Hard Abort（强行停止）：
  → 立即中止，不管当前工具是否执行完成
  → 可能丢失信息

Graceful Interrupt（优雅中断）：
  → 完成当前工作再停止
  → 确保数据一致性和用户体验
```

**作用：**
- ✅ 用户可以请求"完成后停止"
- ✅ 避免中断正在执行的工具
- ✅ 确保返回有意义的结果而不是残缺数据

**场景示例：**

```
场景：用户觉得结果已经够好，请求停止

时间线：
T1: AI 在执行第 10 次迭代的工具调用
T2: 用户发送"停止"→ Graceful Interrupt 请求
T3: 第 10 次迭代继续执行工具
T4: 工具调用完成，收集工具结果
T5: AI 检查：currentIteration = 10 ✗，no pending ✗，graceful = true ✓
    → 继续循环一次（处理 graceful 中断）
T6: 进入第 11 次迭代
T7: 执行 LLM 调用前检查：检测到 graceful 中断
    → 设置 gracefulTerminal = true
    → 添加中断提示消息
    → 禁用工具调用（providerToolDefs = nil）
T8: LLM 返回最终响应（无工具调用）
T9: 退出循环，返回结果
```

---

## 完整场景演示

### 场景 A：正常工作流

```
配置：MaxIterations = 10

迭代 1-8：成功调用工具，获得结果
迭代 9：LLM 判断信息充足，直接返回答案
        → 无工具调用
        → 条件 1: 9 < 10 ✓（但会 break）
        → 退出循环

条件状态：
condition1 = false (已 break)
condition2 = false (无待处理)
condition3 = false (无中断请求)
→ 正常退出循环
```

### 场景 B：用户干预

```
配置：MaxIterations = 10

迭代 1-9：AI 不断调用工具
迭代 9 完成时：
  condition1 = true (9 < 10) ✓ 继续

迭代 9 中途：用户发送"换个思路"
  → Steering 消息进入 pendingMessages

迭代 10：
  condition1 = true (10 < 10) ✗ 不满足
  condition2 = true (有待处理消息) ✓ 继续
  → 继续循环

处理完 Steering 消息后，迭代 11：
  condition1 = false (11 < 10) ✗
  condition2 = false (已处理) ✗
  condition3 = false (无中断) ✗
  → 退出循环
```

### 场景 C：优雅中断

```
配置：MaxIterations = 10

迭代 9 完成时：
  condition1 = true (9 < 10) ✓ 继续

用户发送中断信号（不是 Steering，是 Graceful Interrupt）
  → gracefulInterruptRequested() = true

迭代 10：
  condition1 = true (10 < 10) ✗ 不满足
  condition2 = false (无待处理) ✗ 不满足
  condition3 = true (graceful 中断) ✓ 继续
  → 继续循环

第 11 次迭代：
  ✓ 检测到 graceful = true
  ✓ 执行最后一次 LLM 调用（禁用工具）
  ✓ 返回最终答案
  ✓ 然后退出循环
```

---

## 循环继续 vs 循环停止

### ✅ 循环继续条件（满足任意一个）

1. **还有迭代次数剩余**
   - `currentIteration < MaxIterations`
   - 例如：第 5 次迭代时，MaxIterations=10

2. **有用户消息等待处理**
   - `len(pendingMessages) > 0`
   - Steering 消息、SubTurn 结果

3. **用户请求了优雅中断**
   - `gracefulInterruptRequested() == true`
   - 需要完成当前工作后停止

### ✗ 循环停止条件（同时满足）

```
condition1 = false  →  达到最大迭代次数
AND
condition2 = false  →  没有待处理消息
AND
condition3 = false  →  没有 graceful 中断请求
```

---

## 关键设计思想

### 1. 灵活的迭代限制

```
"不是绝对的 MaxIterations，而是最小的迭代次数"
```

- 达到 MaxIterations 后，如果用户有新消息，仍要处理
- 防止丢弃用户的实时指导

### 2. 用户优先

```
"用户的指导消息优先于迭代限制"
```

- Steering 消息可以"打破"MaxIterations 的限制
- 实现真正的实时交互

### 3. 优雅退出

```
"不是粗暴中断，而是完成当前工作"
```

- Graceful Interrupt 允许完成正在执行的工作
- 避免数据不一致或用户困惑

---

## 代码位置与工作流

```
turnLoop: (标签，用于 goto）
  ↓
for 循环条件检查
  ├─ condition1: 迭代次数 < MaxIterations？
  ├─ OR condition2: 有待处理消息？
  └─ OR condition3: 有 graceful 中断？
  ↓
继续循环体
  ├─ 检查 hard abort（强行停止）→ break
  ├─ 获取迭代次数和 Steering 消息
  ├─ 调用 LLM
  ├─ 执行工具
  ├─ 处理结果
  └─ 循环回顶部（goto turnLoop）
  ↓
若条件不满足
  └─ 退出循环 → 最终化 → 返回结果
```

---

## 总结表

| 条件 | 含义 | 作用 | 触发场景 |
|------|------|------|---------|
| **1** | `迭代 < MaxIterations` | 限制最多推理次数 | LLM 多次推理、工具调用 |
| **2** | `待处理消息 > 0` | 处理用户实时指导 | 用户在推理中途发送指令 |
| **3** | `graceful 中断` | 完成工作后停止 | 用户请求优雅停止 |

---

## 特殊情况

### Hard Abort（硬中断）

在循环开始立即检查：

```go
if ts.hardAbortRequested() {
    turnStatus = TurnEndStatusAborted
    return al.abortTurn(ts)
}
```

作用：立即停止，不遵守循环条件

### Break 语句

在循环体内的多处会执行 `break`：

```go
// 场景 1：父 Turn 结束（SubTurn）
if !ts.critical {
    break
}

// 场景 2：检查到中断且已处理
if skipReason != "" && remaining > 0 {
    break
}

// 场景 3：响应已被处理（ResponseHandled）
if allResponsesHandled {
    break
}
```

这些 `break` 会直接跳出循环，不再检查循环条件。

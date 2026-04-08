# Launcher TUI Gateway 启动管理

## 概述

PicoClaw Launcher TUI（picoclaw-launcher-tui）是一个轻量级的终端用户界面应用，提供了对 PicoClaw Gateway 服务的生命周期管理功能。本文档详细描述了其中的 Gateway 启动、停止和状态监控机制。

## 项目结构

```
cmd/picoclaw-launcher-tui/
├── main.go              # 应用入口点
├── config/              # 配置管理
├── ui/                  # TUI 界面组件
│   ├── app.go          # 核心应用框架
│   ├── gateway.go      # Gateway 生命周期管理 ⭐
│   ├── home.go         # 主仪表板
│   ├── channels.go     # 通讯频道配置
│   ├── models.go       # 数据结构
│   ├── schemes.go      # AI 模型方案管理
│   └── users.go        # 用户和 API 密钥管理
└── README.md
```

## Gateway 启动机制

### 1. 启动流程（startGateway）

位置：`cmd/picoclaw-launcher-tui/ui/gateway.go:45-97`

#### 步骤详解

**第 1 步：状态检查**
```go
status := getGatewayStatus()
if status.running {
    return fmt.Errorf("gateway is already running (PID: %d)", status.pid)
}
```
- 通过 `ppid.ReadPidFileWithCheck()` 检查 PID 文件
- 防止重复启动，如果 gateway 已运行则返回错误

**第 2 步：跨平台命令启动**

Windows 平台：
```go
cmd = exec.Command("cmd", "/C", "start /B picoclaw gateway > NUL 2>&1")
```
- 使用 `cmd /C` 执行命令
- `/B` 参数：在后台启动（不创建新窗口）
- `> NUL 2>&1`：重定向输出到空设备

Unix/Linux/macOS 平台：
```go
cmd = exec.Command("sh", "-c", "nohup picoclaw gateway > /dev/null 2>&1 &")
```
- 使用 `nohup` 防止进程挂起（immune to hangups）
- `&` 符号：后台运行
- `> /dev/null 2>&1`：丢弃所有输出

**第 3 步：启动并等待**
```go
err := cmd.Start()
if err != nil {
    return err
}
time.Sleep(1 * time.Second)  // 给进程启动时间
```

**第 4 步：验证启动成功**

Windows 特殊处理：
```go
if runtime.GOOS == "windows" {
    cmd := exec.Command(
        "wmic",
        "process",
        "where",
        "name='picoclaw.exe' and commandline like '%gateway%'",
        "get",
        "processid",
    )
    output, err := cmd.Output()
    // 解析进程 ID
}
```
- 使用 Windows 管理规范命令行（WMIC）查询进程
- 过滤 `picoclaw.exe` 进程且命令行包含 `%gateway%`

**第 5 步：最终检查**
```go
status = getGatewayStatus()
if !status.running {
    return fmt.Errorf("failed to start gateway")
}
return nil
```
- 再次检查 PID 文件确认启动成功
- 失败则返回错误信息

### 2. 停止流程（stopGateway）

位置：`cmd/picoclaw-launcher-tui/ui/gateway.go:99-124`

```go
func stopGateway() error {
    status := getGatewayStatus()
    if !status.running {
        return fmt.Errorf("gateway is not running")
    }

    // 根据操作系统发送终止信号
    var err error
    if runtime.GOOS == "windows" {
        err = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(status.pid)).Run()
    } else {
        err = exec.Command("kill", strconv.Itoa(status.pid)).Run()
    }

    if err != nil {
        return err
    }

    // 轮询等待进程真正停止
    for i := 0; i < 5; i++ {
        if !getGatewayStatus().running {
            break
        }
        time.Sleep(200 * time.Millisecond)
    }

    return nil
}
```

**关键点：**
- Windows：使用 `taskkill /F /PID <pid>` 强制终止
- Unix：使用 `kill <pid>` 发送终止信号
- 轮询 5 次（每次间隔 200ms），等待进程真正停止
- PID 文件会在进程停止时由 `ReadPidFileWithCheck()` 自动清理

### 3. 状态检查（getGatewayStatus）

位置：`cmd/picoclaw-launcher-tui/ui/gateway.go:33-43`

```go
func getGatewayStatus() gatewayStatus {
    data := ppid.ReadPidFileWithCheck(picoHome())
    if data == nil {
        return gatewayStatus{running: false}
    }
    return gatewayStatus{
        running: true,
        pid:     data.PID,
        version: data.Version,
    }
}
```

**数据结构：**
```go
type gatewayStatus struct {
    running bool
    pid     int
    version string
}
```

状态检查通过读取 `~/.picoclaw/` 目录下的 PID 文件实现。

## UI 界面管理

### 1. 页面构建（newGatewayPage）

位置：`cmd/picoclaw-launcher-tui/ui/gateway.go:126-229`

**界面布局：**
```
┌─────────────────────────────────┐
│  GATEWAY MANAGEMENT             │
├─────────────────────────────────┤
│                                 │
│  GATEWAY RUNNING                │
│  PID: 12345                     │
│  Version: v1.0.0                │
│                                 │
│         [START]  [STOP]         │
│                                 │
└─────────────────────────────────┘
```

**配色方案：**
- 标题颜色：青色（`#00f0ff`）
- 背景色：深紫色（`#050510`）
- 运行状态：绿色（`#39ff14`）
- 停止状态：红色（`#ff2a2a`）

### 2. 按钮交互

**START 按钮：**
```go
buttons.AddItem(" [lime]START[white]   ", "", 0, func() {
    if !getGatewayStatus().running {
        err := startGateway()
        if err != nil {
            a.showError(err.Error())
        }
        updateStatus()
    }
})
```

**STOP 按钮：**
```go
buttons.AddItem(" [red]STOP[white]    ", "", 0, func() {
    if getGatewayStatus().running {
        err := stopGateway()
        if err != nil {
            a.showError(err.Error())
        }
        updateStatus()
    }
})
```

### 3. 自动状态更新

位置：`cmd/picoclaw-launcher-tui/ui/gateway.go:181-212`

**更新函数：**
```go
updateStatus = func() {
    status := getGatewayStatus()
    if status.running {
        versionInfo := ""
        if status.version != "" {
            versionInfo = fmt.Sprintf("\nVersion: %s", status.version)
        }
        statusTV.SetText(fmt.Sprintf("[#39ff14::b]GATEWAY RUNNING[-]\n\nPID: %d%s", status.pid, versionInfo))
        buttons.SetItemText(0, " [gray]START[white]   ", "")    // 禁用 START 按钮
        buttons.SetItemText(1, " [red]STOP[white]    ", "")     // 启用 STOP 按钮
    } else {
        statusTV.SetText("[#ff2a2a::b]GATEWAY STOPPED[-]\n\nPID: N/A")
        buttons.SetItemText(0, " [lime]START[white]   ", "")    // 启用 START 按钮
        buttons.SetItemText(1, " [gray]STOP[white]    ", "")    // 禁用 STOP 按钮
    }
}
```

**定时更新：**
```go
done := make(chan struct{})
go func() {
    ticker := time.NewTicker(2 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            a.tapp.QueueUpdateDraw(updateStatus)
        case <-done:
            return
        }
    }
}()
```

- 每 2 秒检查一次状态
- 使用 `QueueUpdateDraw()` 安全地在 UI 线程更新
- ESC 键退出时通过 `close(done)` 关闭后台 goroutine

### 4. 键盘快捷键

```go
flex.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
    if event.Key() == tcell.KeyEscape {
        close(done)
        return a.goBack()
    }
    if originalInputCapture != nil {
        return originalInputCapture(event)
    }
    return event
})
```

- **ESC**：返回上一页面

## 设计优势

### ✅ 跨平台兼容性
- 自动检测操作系统（`runtime.GOOS`）
- Windows：使用 `cmd`, `start`, `taskkill`, `wmic`
- Unix/Linux/macOS：使用 `sh`, `nohup`, `kill`

### ✅ 后台守护进程
- 使用 `nohup` 和 `&` 防止进程挂起
- 输出重定向到 `/dev/null`，不影响终端

### ✅ 可靠的状态追踪
- 通过 PID 文件机制检测进程存活
- 包含版本信息，便于调试
- 自动清理过期的 PID 文件

### ✅ 实时 UI 同步
- 每 2 秒自动刷新状态
- 按钮根据状态动态启用/禁用
- 启动失败时显示错误信息

### ✅ 完整的错误处理
- 检测重复启动
- 验证启动成功
- 轮询等待进程停止
- 友好的错误消息

## 相关依赖包

| 包                  | 用途                  |
|-------------------|----------------------|
| `github.com/gdamore/tcell/v2` | 终端单元库           |
| `github.com/rivo/tview`       | TUI 框架             |
| `github.com/sipeed/picoclaw/pkg/config` | 配置管理 |
| `github.com/sipeed/picoclaw/pkg/pid`    | PID 文件操作 |
| `os/exec`         | 执行外部命令         |
| `runtime`         | 运行时信息           |

## PID 文件位置

- **Unix/Linux/macOS**: `~/.picoclaw/picoclaw.pid`
- **Windows**: `%USERPROFILE%\.picoclaw\picoclaw.pid`

PID 文件格式包含：
- `PID`：进程 ID
- `Version`：Gateway 版本号
- 检查间隔时由 `ReadPidFileWithCheck()` 自动验证和清理

## 快速使用

### 构建 Launcher TUI

```bash
# 从项目根目录
make build-launcher-tui

# 输出位置
build/picoclaw-launcher-tui-<platform>-<arch>
build/picoclaw-launcher-tui  # 符号链接
```

### 运行

```bash
# 默认配置
picoclaw-launcher-tui

# 自定义配置文件
picoclaw-launcher-tui /path/to/config.json
```

## 故障排除

| 问题 | 原因 | 解决方案 |
|-----|------|---------|
| "gateway is already running" | Gateway 已在运行 | 使用 STOP 按钮或手动 kill 进程 |
| "failed to start gateway" | 启动失败 | 检查 `picoclaw` 命令是否在 PATH 中 |
| 状态不更新 | 后台更新 goroutine 被关闭 | 重新进入 Gateway 管理页面 |
| Windows 启动失败 | WMIC 不可用 | 需要管理员权限或检查 Windows 版本 |

## 扩展参考

- 详见 `cmd/picoclaw-launcher-tui/README.md` 了解完整的 Launcher TUI 功能
- 详见 `pkg/pid/` 了解 PID 文件管理实现
- 详见 `pkg/config/` 了解配置管理机制

package pico

import (
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

// init 在包初始化时自动执行，注册两个 Pico 协议相关的频道工厂。
// 这是一个工厂注册模式，用来解耦频道的创建和使用。
func init() {
	// RegisterFactory("pico", ...) 注册 Pico 服务器频道工厂
	// 角色: 🖥️ 服务器
	// 职责: 作为 WebSocket 服务器，接收来自外部客户端的连接请求
	// 应用场景:
	//   - 网页前端连接
	//   - 移动应用连接
	//   - 外部系统连接
	// 特点:
	//   - 监听 HTTP 端口，使用 /pico/ 路由处理 WebSocket 升级请求
	//   - 支持多个客户端同时连接
	//   - 同一会话(sessionID)可以有多个并发连接
	//   - 通过 Token 认证保护
	channels.RegisterFactory("pico", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewPicoChannel(cfg.Channels.Pico, b)
	})

	// RegisterFactory("pico_client", ...) 注册 Pico 客户端频道工厂
	// 角色: 💻 客户端
	// 职责: 作为 WebSocket 客户端，主动连接到远程 Pico Protocol 服务器
	// 应用场景:
	//   - 连接到其他 PicoClaw 实例
	//   - 集成第三方服务
	//   - 建立分布式 AI 系统
	// 特点:
	//   - 主动发起出站连接，不接受入站连接
	//   - 维护单一长连接
	//   - 自动重连机制(reconnectLoop)
	//   - 网络中断后能自动恢复连接
	channels.RegisterFactory("pico_client", func(cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
		return NewPicoClientChannel(cfg.Channels.PicoClient, b)
	})
}

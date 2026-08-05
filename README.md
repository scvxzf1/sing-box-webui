# sing-box WebUI

面向 Linux 客户端场景的本地 sing-box 控制面。项目使用系统浏览器作为界面，
不提供桌面 GUI；后端负责本地 API、配置事务和 sing-box 生命周期管理。

当前仓库已经包含可运行的项目基座，以及订阅拉取与自动更新、节点选择、节点延迟
测试、节点池、规则与规则池管理、实时流量下载池策略、系统代理/TUN 应用和运行状态界面。手动规则与订阅来源规则
均由后端持久化和编译，规则池支持整池文本原子替换，不依赖浏览器本地状态。

## 已确定的技术基线

- Linux 首发，Windows 仅保留清晰的平台边界。
- Go 后端，标准库优先。
- React、TypeScript 和 Vite 前端。
- REST 用于查询和命令，SSE 用于单向实时事件。
- 官方 sing-box 初始版本随 Go 程序内嵌，运行时作为受管独立进程释放和执行。
- Web API 只以普通用户权限运行并只监听回环地址。
- TUN、路由和 DNS 等特权操作不得进入 Web API 进程。
- 开发期从源码启动，不依赖 Docker；sing-box 核心可独立手动更新和回滚。

## 文档

- [架构与安全边界](docs/architecture.md)
- [源码开发指南](docs/development.md)
- [架构决策索引](docs/adr/README.md)
- [核心管理与更新](docs/core-management.md)
- [已归档：技术规格 v0.1](docs/archive/spec-v0.1.md)（仅供历史查阅，不作为开发依据）

## 源码启动

前置工具：Go、Node.js 22 和 npm。无需另行安装 sing-box。

```bash
npm --prefix web install
./scripts/dev.sh
```

然后访问 `http://127.0.0.1:5173`。Go API 默认监听
`http://127.0.0.1:11872`，两个开发服务都拒绝监听非回环地址。

也可以分别启动：

```bash
go run ./cmd/webui
npm --prefix web run dev
```

检查命令见[源码开发指南](docs/development.md)。

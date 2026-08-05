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
- [Web 鉴权配置与运维](docs/web-authentication.md)
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

## TUN 模式（Linux）

Web/API 必须继续以普通用户运行。TUN 只要求实际执行的 sing-box 核心持有
`CAP_NET_ADMIN`；授予一次后，通过项目反复开启、关闭 TUN 都不再需要授权：

```bash
CORE="$(readlink -f var/data/core/sing-box)"
sudo setcap cap_net_admin+ep "$CORE"  # 仅首次或核心版本切换后执行
./scripts/dev-tun.sh                  # 日常启动，不使用 sudo
```

`dev-tun.sh` 会设置 `SING_BOX_WEBUI_ENABLE_TUN=1` 并在启动前验证 capability；缺失时
只给出修复命令，不会自动提权。托管核心更新或回滚会切换到另一个版本文件，届时需要
对新的 `var/data/core/sing-box` 目标再授予一次。不要对 `go`、Vite、WebUI 后端或整个
开发脚本授予权限，也不要使用 `sudo ./scripts/dev.sh`。

Ubuntu 使用 systemd-resolved 时，sing-box 会通过 `resolvectl` 为 `singtun0` 设置和恢复
DNS。首次使用可安装项目提供的最小 Polkit 规则：

```bash
./scripts/install-tun-polkit.sh
```

安装过程只认证一次。规则仅允许执行安装脚本的本机活动用户完成 TUN 生命周期需要的
四个 resolved action，不使用 `org.freedesktop.resolve1.*` 通配授权。卸载时执行
`sudo rm /etc/polkit-1/rules.d/49-sing-box-webui-resolved.rules`。

检查命令见[源码开发指南](docs/development.md)。

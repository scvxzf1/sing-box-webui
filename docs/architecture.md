# 架构与安全边界

本文解释系统组件、信任边界和代码所有权。

## 1. 组件模型

```text
System browser
    |
    | HTTP on explicit loopback origin
    v
Web/API process (ordinary user)
    |-- serves API and, in production, built frontend assets
    |-- owns browser sessions and SSE fan-out
    |-- never owns CAP_NET_ADMIN
    |
    | typed local IPC when privileged mode is enabled
    v
Core/Helper boundary
    |-- owns the sing-box supervisor in privileged mode
    |-- exposes only capability-oriented operations
    v
external sing-box process
```

默认运行时由 Core Manager 从 Go 内嵌资源释放 sing-box，并通过受管版本入口交给
supervisor。sing-box 仍是独立子进程，不作为 Go library 链接到 Web/API 进程。

开发模式下，浏览器先访问 Vite，Vite 只把 `/api` 代理到 Go；SSE 也位于
`/api/v1` 下。生产静态
资源服务属于未来构建阶段，不要求 Node.js 常驻。

## 2. 信任边界

### 2.1 浏览器

浏览器输入、URL 参数、localStorage、扩展和第三方页面均不可信。前端类型不能替代
后端校验。任何能够改变配置、进程或网络状态的请求都必须在后端重新认证和验证。

### 2.2 Web/API 进程

Web/API 进程以当前桌面用户运行，只能写自己的 XDG 目录。它可以读取非特权运行
状态、管理浏览器会话并转发经过验证的 Core 命令，但不能直接修改全局路由、DNS、
防火墙或 TUN。

### 2.3 Core/Helper

Core/Helper 不是“另一个通用后端”。它的 API 必须围绕少量能力设计，例如应用一份
已验证配置或请求一个明确的状态转换。它不得接受 shell 字符串、任意 argv、任意
文件路径或任意网络规则。

如果首期只运行普通代理模式，Core 可以使用非特权实现。启用 TUN 前必须替换为通过
安全 ADR 审核的 Linux 实现。

## 3. 后端模块边界

建议代码边界如下，实际包名可以在实现时微调：

```text
cmd/
  webui/                 process entrypoint
internal/
  api/                   HTTP transport, auth, error mapping
  application/           use cases and orchestration
  events/                typed event model and bounded fan-out
  subscription/          subscription parsing, storage and sanitized views
  routing/               manual/imported rules, persistence and compilation
  latency/               isolated sing-box end-to-end node probes
  poolhealth/            active-pool health state, backoff and route selection
  trafficpolicy/         aggregate traffic detection and download-pool handover
  core/                  embedded seed, managed versions, update and rollback
  netsafety/             shared outbound address restrictions
  netresolve/            direct DNS-over-HTTPS resolution outside system Fake-IP
  supervisor/            sing-box lifecycle state machine
  configstore/           atomic config transactions
  singbox/               version, check and management API adapter
  platform/
    paths/               XDG and future Windows paths
    process/             OS process primitives
    privilege/           Linux privilege boundary
web/                     React application
```

不要创建包含所有系统能力的 `Platform` 巨型接口。按能力拆分接口，使测试可以替换
单个边界，也避免未来 Windows 实现继承 Linux 假设。

## 4. 调用方向

允许的依赖方向：

```text
transport -> application -> domain interfaces
                         -> supervisor
                         -> configstore
infrastructure adapters -> domain interfaces
```

禁止：

- HTTP handler 直接调用 `os/exec`。
- React 直接依赖 sing-box Clash API。
- supervisor 直接拼接 shell。
- 配置存储通过日志事件决定提交是否成功。
- 平台包导入 API 或前端概念。

## 5. 进程监督

supervisor 是 sing-box 生命周期的唯一写入者。所有命令进入单一串行队列，状态变更
携带单调递增 generation。异步输出只有在 generation 与当前实例一致时才可更新
状态，以避免旧进程退出覆盖新进程状态。

停止顺序：

1. 将状态切换为 `stopping`，拒绝新的启动请求。
2. 请求优雅终止。
3. 等待配置的短超时。
4. 终止整个受管进程组，避免遗留子进程。
5. 回收 stdout/stderr reader 并发布最终状态。

崩溃重启必须有限次、带抖动退避。用户主动停止不会触发自动重启。

## 6. 配置事务与运行版本

`ConfigStore` 保存不可变版本，而不是原地编辑当前文件。一次应用操作引用明确的
配置版本：

```text
draft bytes
  -> structural validation
  -> sing-box check
  -> committed version N
  -> supervisor starts generation G with version N
```

状态必须同时报告“已保存版本”和“当前运行版本”。因此重启失败不会使 WebUI 错误地
声称新配置已经生效。

所有文件路径由 `ConfigStore` 生成；API 只接收配置标识和内容，不接收宿主任意路径。

## 7. API 与事件

HTTP handler 应保持薄层：解析和限制请求、认证、调用 application service、映射
结构化结果。长操作不应占用无限期 HTTP 请求；若无法在短超时内完成，应返回操作
标识，并通过状态查询或 SSE 更新结果。

节点延迟测试由独立服务读取后端内部的节点配置，并生成权限为 `0600` 的临时
sing-box 配置。每个真实出站绑定独立的随机回环 mixed 入站，Go HTTP 客户端通过相应
代理访问固定 HTTPS 204 地址并直接计时；测试完成后进程、进程组和临时配置必须清理。
节点域名先在 Go 进程中解析为允许的公网 IP，再写入临时配置，以阻止回环、私网、
链路本地、多播、Fake-IP 和未指定地址。解析器使用独立 DNS over HTTPS，避免系统代理
的 Fake-IP DNS 污染。API 只返回节点 ID、状态和毫秒值，不返回节点凭据。
不得再用 TCP 建连时间冒充代理链路延迟。

当应用节点池时，生成的 sing-box 配置使用 `selector` 包装 `urltest`。
`poolhealth` 只通过随机回环端口和每次运行生成的密钥访问内核 Clash API，不向
浏览器暴露该端口或密钥。它对池内成员执行有界并发的多目标 HTTPS 探测，
维护健康、降级和隔离状态，并以退避方式复测。全部成员不可用时选择
`block` 出站，默认 fail-closed，禁止隐式直连。

路由规则由 `routing.Manager` 单独持久化，不写入订阅节点模型。手动规则和已由用户
启用的订阅规则按确定顺序编译到 `route.rules`，固定的 `route.final = proxy` 作为
不可删除的全局兜底。订阅刷新以规范化规则内容生成稳定标识：新规则默认关闭，内容
未变的规则保留用户启用状态，上游删除的规则同步移除。不支持的条件、动作或缺失
出站引用仍保留来源摘要和原因，但后端拒绝启用。规则变更和订阅自动刷新在 sing-box
运行时必须重新生成并校验当前配置，不能只更新 WebUI 展示。

规则池是 `routing.Manager` 中的一等资源，池内嵌已校验的手动规则并拥有独立顺序。
整池文本保存必须先校验全部规则，再以一次原子文件替换提交；任何成员非法时都不得
改变已存储规则池。编译顺序为独立手动规则、已启用规则池（池顺序及成员顺序）、
已启用订阅规则，最后由全局代理兜底。

`trafficpolicy.Manager` 只读取后端已鉴权的 sing-box Clash API 累计流量，浏览器
不得获取内核端口或密钥。首版以当前节点池的总下行速率判定大流量；
达到持续阈值后保存原运行目标，重新应用指定的下载节点池，回落后恢复原节点池。
该过程会重启 sing-box 并中断所有现有代理连接，依赖客户端断点续传；不得宣称为单连接
无感迁移。策略只在当前目标为节点池且内核流量统计可用时监控；手动改变运行
目标必须结束当前自动接管。

SSE 连接只负责通知。状态的最终真相来自查询 API 或连接建立后的权威快照。事件丢失
不会破坏一致性，日志类数据允许在到达硬上限后丢弃最旧记录并显式报告 gap。

## 8. 浏览器安全

认证、CSRF、Origin/Host 校验和安全响应头属于 API transport 的统一中间件，禁止由
各 handler 自行选择是否启用。健康端点可以公开最少信息，但不得泄漏路径、版本细节
或 sing-box 配置。

开发模式不能关闭整个安全模型。允许的差异仅为精确增加 Vite origin；这样可以避免
“只在生产启用安全”导致开发和测试无法覆盖真实边界。

## 9. Windows 技术铺垫

首期仅要求以下类型不包含 Linux 专属语义：

- 配置和状态领域模型。
- HTTP API 和 SSE envelope。
- supervisor 的抽象状态机。
- `ProcessController`、`PlatformPaths` 和 `PrivilegeController` 接口。

信号、进程组、Unix Socket、XDG 和文件权限属于 Linux adapter。不得在业务层使用
散落的 `runtime.GOOS` 分支。没有测试语义的空 Windows 文件不提供兼容价值，因此
首期不创建。

## 10. 架构验收

实现变更只有在满足以下条件时才符合本架构：

- Web/API 进程可在普通用户权限下完成非 TUN 开发。
- HTTP handler 中不存在 shell 执行和任意路径操作。
- 配置验证失败不会改变当前运行版本。
- 并发命令不能启动两个 sing-box 实例。
- 后端和 Vite 均不会意外监听非回环接口。
- 慢客户端和持续日志不会导致内存无界增长。
- Linux 专属逻辑集中在可替换 adapter 中。

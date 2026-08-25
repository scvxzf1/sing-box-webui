# 源码开发指南

本文定义项目的源码开发契约。Go 与前端工程已经建立，本文列出的启动和检查命令均
应保持可运行。

## 1. 开发原则

- 所有开发服务只监听回环地址。
- 不使用 Docker。
- 不以 root 或 `sudo` 启动 Vite、npm、`go run` 或整个开发脚本。
- 默认不接触用户真实的 sing-box 配置和系统网络设置。
- 单元测试使用 fake sing-box；显式集成测试才允许调用真实二进制。
- 依赖版本通过 `go.mod`、`package-lock.json` 和 Node 版本文件锁定。

## 2. 预期前置工具

代码初始化后需要：

- Go，版本以 `go.mod` 为准。
- Node.js，版本以仓库 Node 版本文件为准。
- npm；首期不额外要求 pnpm、Bun 或全局任务运行器。
- 无需预装 sing-box；默认使用内嵌的官方 Linux amd64 核心。

开发环境不需要 Docker、Nginx、数据库或桌面 WebView。

## 3. 目标目录

```text
cmd/webui/                 Go 入口
internal/                  后端实现
api/                       API schema
web/                       React/Vite 工程
scripts/                   无特权开发脚本
testdata/                  fake sing-box 和固定样例
var/                       被 Git 忽略的本地运行数据
docs/                      规格、架构和 ADR
```

首期使用一个根 `go.mod` 和一个 `web/package.json`，不建立 Go workspace 或前端
monorepo。

## 4. 开发服务器

目标端口：

```text
Vite:  127.0.0.1:33333
Go:    127.0.0.1:33334
```

Vite 只代理：

```text
/api     -> http://127.0.0.1:33334
/healthz -> http://127.0.0.1:33334
```

`/api` 的代理目标同样是 `http://127.0.0.1:33334`。SSE 端点使用 `/api/v1`
前缀，因此不需要第二条事件代理规则。`/healthz` 是用于开发验收的唯一额外代理。

禁止配置任意路径代理或 `host: true`。Go 开发模式只允许精确的 Vite origin，不能用
`Access-Control-Allow-Origin: *`。

目标手动启动方式：

```bash
# 终端 1
go run ./cmd/webui

# 终端 2
npm --prefix web run dev
```

脚手架完成后提供 `./scripts/dev.sh` 作为单命令入口。脚本必须转发信号、在任一子进程
失败时退出并清理另一个进程，同时拒绝以 root 身份运行。它会等待 Go API 的
`/healthz` 返回成功后再启动 Vite，避免页面的 SSE 代理连接抢在后端监听之前建立。

## 5. 开发数据

默认开发配置写入仓库的 `var/`，该目录必须加入 `.gitignore`。建议环境变量：

```text
SING_BOX_WEBUI_ADDR=127.0.0.1:33334
SING_BOX_WEBUI_DATA_DIR=./var/data
SING_BOX_WEBUI_RUNTIME_DIR=./var/run
SING_BOX_WEBUI_LOG_LEVEL=debug
SING_BOX_BIN=/absolute/path/to/sing-box
SING_BOX_WEBUI_TUN_ADDRESS=198.20.0.1/30
```

`SING_BOX_BIN` 是可选的开发覆盖项。未设置时使用托管核心；设置后程序必须规范化并
验证该绝对路径，且禁用托管更新和回滚。测试不得从任意 PATH 条目隐式选择二进制。

`SING_BOX_WEBUI_TUN_ADDRESS` 只接受 IPv4 `/30` 网段。它不能与 Fake IP 地址池、Docker
网络或本机局域网重叠；配置生成时会拒绝已知的 Fake IP 重叠。默认值 `198.20.0.1/30`
用于避开项目默认的 Fake IP 网段 `198.18.0.0/15`。

环境变量不得承载长期认证秘密。测试秘密使用测试夹具或进程内注入，并确保不写日志。

Web 鉴权默认开启，访问令牌保存在项目的 `var/config.json` 中。首次启动会自动生成权限为 `0600` 的
随机令牌；也可以在停止服务后编辑 `web.token`（至少 8 个字符）。通过
`SING_BOX_WEBUI_CONFIG` 可以指定另一个配置文件路径。令牌只用于登录，浏览器后续使用
HttpOnly、SameSite=Strict 的短期会话 Cookie，不把令牌写入 Web Storage。
将 `web.enabled` 显式设为 `false` 并重启服务可禁用 Web 鉴权；省略该配置项仍视为开启。

### 5.1 节点布局与延迟测试

节点页的每行 `1/2/3/4` 列偏好保存在浏览器 `localStorage` 的
`sing-box-webui:nodes-grid-columns` 键中。该值只包含非敏感界面偏好，可跨浏览器
重启保留；订阅地址、节点凭据和运行配置不得写入 `localStorage`。

连接页的目标类型、订阅选项和节点池选项分别保存在
`sing-box-webui:connection-target-type`、`sing-box-webui:connection-subscription-id`
和 `sing-box-webui:connection-pool-id`。后两者只保存不透明 ID，不包含订阅地址、
节点配置或运行参数；已删除的 ID 会在列表加载后自动回退到有效选项。

首次开启代理会把所有有效单节点和可解析节点池编译进同一运行目录。代理运行中，连接页
选择不同单节点或节点池后显示“热切换”；当代理模式与局域网监听保持不变且目标目录未过期时，
后端只更新根 selector。新导入节点、节点池成员变更、规则/DNS 变更仍可能触发完整重载。

链式代理管理页负责链路的创建、编辑、删除和端到端测试，连接页将链路作为第三类运行
目标。资源 API 位于 `/api/v1/chains`，测试接口位于 `/api/v1/chains/{id}/latency`。
链路或入口/出口依赖变化会使运行目录签名失效，下一次应用自动走完整配置校验与重载；
没有变化时复用根 selector 热切换语义。

`POST /api/v1/subscriptions/{id}/latency` 支持单节点或批量手动测试，默认使用托管
sing-box 核心。后端生成隔离的临时 sing-box 配置，通过各节点真实出站访问
`https://cp.cloudflare.com/generate_204`。每个出站映射到独立的随机回环 mixed 入站，
Go HTTP 客户端通过对应代理完成 HTTPS 请求并计时。单次 HTTP 测试超时 6 秒、最多并发 16 个、
单批最多 128 个，并且同一时间只运行一个批次。Hysteria2、TUIC 等协议由 sing-box
真实出站处理，不再用 TCP 建连时间代替。节点域名解析结果必须固定为允许的公网 IP，
解析使用直连 DNS over HTTPS，避免系统 Fake-IP 污染；临时配置权限为 `0600` 且测试
结束后删除。

### 5.2 节点池健康检查

只监控当前已应用的节点池。sing-box 内核仍使用 `urltest` 完成常规选路，
后端通过受密钥保护的回环 Clash API 对具体成员执行主探测地址和最多
4 个备用地址的真实出站探测。同时最多探测 4 个成员，不启动第二个
sing-box 进程。

节点在连续完全探测失败达到配置次数后进入隔离，退避从 15 秒开始，
上限由节点池设置决定。隔离节点连续恢复成功后才重新参与选路。所有
节点失效时选择 `block` 而不是 `direct`。当前决策信号是主动 HTTPS 探测；
内核尚未向项目提供可靠的每节点业务连接错误率，不得通过解析人类日志
伪造该指标。

健康管理器使用 Clash API 的累计上传、下载量和活动连接数判定池是否
空闲。达到 `idle_timeout` 后将 selector 切回 sing-box 的 `auto` 组，停止常规
多目标探测，但每个 `max_backoff` 周期仍执行一次全池安全探测，避免
首个恢复连接拨号失败时因 Clash API 无流量计数而永久停留在空闲状态。
检测到新流量时立即将所有成员排入复测。健康等级相同时，候选节点
必须比当前节点快超过 `tolerance` 才切换；健康节点替换降级节点
不受该容差限制。

### 5.3 路由规则

规则持久化在 `var/data/routing/rules.json`，权限为 `0600`。内置“全局代理”规则不写入
该文件，而是映射为 sing-box 的 `route.final = proxy`。手动规则支持以下条件：

```text
domain, domain_suffix, domain_keyword, ip_cidr, ip_is_private,
port, port_range, process_name, network, protocol
```

动作只允许 `proxy`、`direct` 和 `block`，编译后分别引用项目生成配置中的同名
outbound。匹配顺序固定为手动规则、订阅规则、全局兜底；手动规则可排序，订阅规则
保持各订阅内的上游顺序。订阅 JSON 的 `route.rules` 会在刷新时导入；新导入规则一律关闭，内容未变
时保留启用状态。`rule_set`、逻辑嵌套、反向匹配、未知动作和缺失出站引用当前作为
不兼容规则保留，不得绕过后端强制开启。

订阅下载与节点延迟测试共用 `internal/netresolve` 的直连 DNS over HTTPS 解析，避免
系统代理的 Fake-IP 结果被误判为真实私网地址。解析后的地址仍逐个经过 `netsafety`
校验；回环、私网、链路本地、多播和 Fake-IP 网段不会因使用公共解析器而放行。

规则 API 的写操作会在代理运行时重新应用当前节点或节点池。订阅手动刷新和自动刷新
也通过同一路径重载已启用规则。运行配置仍须先通过内嵌 sing-box 的 `check`，校验失败
不能启动新实例；API 会明确报告“规则已保存但运行时重载失败”。

### 5.4 实时流量策略

流量策略持久化在 `var/data/traffic-policy/traffic-policy.json`，权限为 `0600`。
`poolhealth` 每秒读取一次随机回环 Clash API 的累计上下行和活动连接数；
节点延迟探测仍使用节点池配置的原间隔，不会每秒发起 HTTPS 探测。

首版只支持“当前节点池 -> 下载节点池 -> 原节点池”整体切换。切换经过
`control.Service.Apply`；目标在当前运行目录时只改变根 selector，既有连接保持原出站，
新连接进入下载池。目录陈旧而退回完整应用时仍会中断现有连接。
下载池启用时必须至少有 2 个可用成员；当前运行目标是单节点、代理未运行或流量统计
尚未就绪时，策略只等待且不触发切换。

## 6. 非特权与 TUN 开发

默认源码启动只启用普通用户可运行的适配器。若某项能力需要 TUN 权限，API 应返回
明确的 `capability_unavailable`，不能自动调用 sudo。

禁止：

```bash
sudo npm run dev
sudo go run ./cmd/webui
sudo ./scripts/dev.sh
```

TUN 开发开始前，必须先完成 [ADR-0001](adr/README.md#阻塞性决策队列)。临时人工实验也应只提升明确
构建出的 Core/sing-box 二进制，而不是整个源码工具链；实验步骤不得进入默认开发
命令。

本机实验使用 `./scripts/dev-tun.sh` 启动。该脚本仍以普通用户运行，仅设置功能开关并
验证当前 sing-box 文件已有 `CAP_NET_ADMIN`，绝不调用 `sudo` 或 `pkexec`。首次授权及
托管核心更新/回滚后的重新授权命令见项目 README；capability 必须加在解析符号链接后
得到的版本文件上，不能加在 `var/data/core/sing-box` 符号链接本身。
需要一键完成授权和启动时可执行仓库根目录的 `./start-tun.sh`；它只用 `sudo setcap`
授予 sing-box 核心能力，不会以 root 启动 Go 或 Vite。

## 7. fake sing-box

后端测试需要一个可控的 fake executable，支持通过测试参数模拟：

- 返回版本和能力。
- 配置校验成功或失败。
- 正常启动并等待信号。
- 延迟启动、拒绝退出或异常崩溃。
- 输出大量 stdout/stderr。
- 派生子进程，用于验证进程组清理。

测试断言应基于结构化状态和退出原因，而不是匹配人类可读日志全文。

## 8. 检查命令

Go：

```bash
go fmt ./...
go vet ./...
go test -race ./...
```

前端：

```bash
npm --prefix web run generate:api
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
```

代理通道的本机联调必须在 sing-box 运行后进行。可分别使用
`curl --proxy socks5h://127.0.0.1:<port>`、`curl --proxy http://127.0.0.1:<port>` 验证 SOCKS5/HTTP；
HTTPS 代理还应使用 `--proxy-cacert` 指向从 `/api/v1/channels/certificate` 下载的证书。
共享入口需在局域网另一台设备上使用宿主机实际 IP 和用户名/密码验证，并确认防火墙只放行预期网段。

`generate:api` 根据 `api/openapi.yaml` 更新 TypeScript 类型。生成后如果出现非预期差异，
必须先修正 API schema 或 handler，不能手工修改生成文件。

端到端测试：

```bash
npm --prefix web run test:e2e
```

Playwright 首期只覆盖少量高价值冒烟路径，不能替代 Go 的 supervisor 和配置事务
测试。

## 9. 提交质量要求

每项实现应根据风险包含相应测试：

- 进程代码：成功、超时、崩溃、重复命令和退出清理。
- 配置代码：非法 JSON、校验失败、并发冲突、原子替换和回滚。
- API：错误 envelope、大小限制、超时、认证、Origin 和幂等。
- SSE：初始快照、重连、慢客户端和缓存溢出。
- 前端：加载、空、错误、断线和操作中的禁用状态。

任何涉及凭据、路径、命令或权限的变更需要安全测试，不能只做正常路径验证。

## 10. 调试约束

- 日志使用结构化字段和稳定的组件名。
- 不记录完整配置、订阅 URL、令牌或代理密码。
- 请求 ID 应贯穿 API、application service 和 Core 命令。
- debug 日志同样遵循脱敏要求。
- 日志和 SSE 缓冲必须设置硬上限。
- `healthz` 只检查 Web 进程；sing-box 状态使用独立诊断端点。

## 11. 脚手架完成标准

项目初始化任务必须持续满足：

1. 两条手动启动命令可在干净 Linux 开发环境运行。
2. `scripts/dev.sh` 能安全启动和停止两个开发进程。
3. 浏览器通过 Vite 访问后端健康端点。
4. `ss` 或等价检查确认两个端口只绑定回环地址。
5. Go 和前端全部目标检查命令通过。
6. fake sing-box 测试不依赖开发者机器上的真实配置。
7. README 不再把尚未实现的命令描述为可用功能。

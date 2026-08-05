# 技术规格

- 状态：已归档
- 版本：0.1
- 目标平台：Linux
- 最后更新：2026-08-05

> 本文档仅作为历史记录保留。自 2026-08-05 起，项目决策、设计、实现和评审均不得
> 将本文内容作为依据；本文没有现行规范效力。

本文定义项目的技术边界和质量约束，不展开具体产品功能设计。文中的
“必须”“禁止”“应该”分别对应 MUST、MUST NOT、SHOULD。

## 1. 产品定位

本项目是运行在用户本机的 sing-box 控制面：Go 后端在回环地址提供 API 和
WebUI，用户通过系统浏览器操作本机代理。它不是远程服务器面板，也不是桌面
GUI 应用。

### 1.1 首期范围

- 仅支持 Linux 源码开发和本地运行。
- 管理一个由本项目拥有的 sing-box 实例。
- 使用外部 sing-box 可执行文件，并在启动时探测版本和能力。
- 提供版本化 HTTP API 和单向事件流。
- 为 TUN 场景保留独立的最小特权边界。
- 数据使用受控目录中的文件保存，不引入数据库服务。

### 1.2 明确不在首期范围

- 桌面 GUI、托盘、Electron、Wails 或 Tauri。
- Docker 或其他容器运行方式。
- deb、rpm、AppImage、安装器和自动更新。
- Windows 实现；只要求平台相关代码可替换。
- 局域网或公网远程管理。
- 多用户、多主机和集群管理。
- 将 sing-box 作为 Go library 嵌入本进程。
- SSR、Node.js 服务端或独立数据库。

## 2. 技术栈

### 2.1 后端

- Go，单一根模块。
- `net/http`、`encoding/json`、`log/slog`、`os/exec` 等标准库优先。
- 非标准依赖必须解决明确问题，并在合入时记录原因。
- 禁止通过 shell 字符串执行 sing-box 或系统命令；必须使用固定可执行文件和
  结构化参数。

### 2.2 前端

初始基线：

- React
- TypeScript
- Vite
- TanStack Query
- 原生 `fetch` 封装
- CSS Modules 或普通 CSS

以下依赖按真实需求引入，不属于初始化基线：React Router、Zustand、React Hook
Form、Radix UI、ECharts、Monaco、TanStack Table 和 TanStack Virtual。当前只有
一个真实页面，暂不引入路由；新增依赖必须说明其运行体积、维护成本和无法由现有
技术合理解决的需求。

### 2.3 数据与实时通信

- REST API 承担查询、命令和配置提交。
- SSE 承担服务端到浏览器的状态和日志事件。
- 首期禁止为了实时刷新默认引入 WebSocket；出现必要的双向流需求后再提交
  架构决策记录。
- JSON 文件承担持久化；不得因便利而引入 Redis、SQLite 或远程数据库。
- 浏览器 `localStorage` 只允许保存非敏感界面偏好，不得保存订阅地址、节点凭据、
  CSRF token 或运行配置。

## 3. 运行拓扑

目标拓扑包含三个信任等级：

1. 浏览器中的 React WebUI，不受信任。
2. 普通用户权限的 Go Web/API 进程。
3. 可选的最小特权 Core/Helper，以及由其管理的 sing-box。

Web API 进程禁止以 root 身份运行，也禁止获得 `CAP_NET_ADMIN`。TUN、路由、
DNS 和防火墙操作必须由独立、参数受限的边界执行。具体 Linux 提权机制必须在
实现 TUN 前通过 ADR 确定；不得以 `sudo go run` 或 root Vite 作为替代方案。

普通代理模式允许在没有特权 Helper 的情况下开发。此时 TUN 能力必须明确报告
为不可用，而不是隐式提权或静默失败。

## 4. sing-box 所有权和生命周期

本项目只管理由自己启动的单个 sing-box 实例，不接管任意外部实例。唯一的
Core supervisor 必须串行处理所有状态转换，前端不得直接操作 PID 或执行命令。

状态至少包含：

```text
stopped -> starting -> running -> stopping -> stopped
                \-> failed <-/
```

supervisor 必须定义：

- sing-box 绝对路径、工作目录和配置路径。
- 版本及能力探测失败的处理。
- 启动超时和运行健康判定。
- stdout/stderr 的有界采集和敏感信息脱敏。
- 优雅停止超时后的强制退出。
- 退出码、信号和最近错误的结构化记录。
- 崩溃重启上限、指数退避和稳定运行后的退避重置。
- 后端退出时的子进程语义及孤儿进程清理。
- 并发启动、停止和重启请求的幂等行为。

Linux 信号、进程组和路径处理不得泄漏到平台无关业务层。Windows 后续可以替换
进程实现，但首期不创建无行为的 Windows 空壳代码。

## 5. 配置事务

配置存储必须是单写者。保存流程必须满足：

1. 对请求大小和 JSON 结构做边界校验。
2. 检查编辑版本或 ETag，冲突时拒绝覆盖。
3. 在受控目录创建同文件系统临时文件。
4. 使用限制性权限写入并 `fsync`。
5. 调用受支持版本的 `sing-box check` 验证候选配置。
6. 验证成功后以原子 rename 替换目标文件。
7. 对目录执行必要的同步，更新内存中的版本号。
8. 应用失败时保留可恢复的上一版本，并返回结构化错误。

禁止直接覆盖正在运行的配置文件。用户编辑态、已验证候选和当前运行版本必须在
概念上分离，即使首期物理存储仍然很简单。

配置、令牌和备份文件使用 `0600`，承载目录使用 `0700`。文件操作必须防止符号
链接替换和路径穿越。日志及 API 错误不得输出订阅凭据、代理密码或完整令牌。

## 6. API 契约

- 所有业务 API 使用 `/api/v1` 前缀。
- `GET /healthz` 只表示 Web 进程存活，不等同于 sing-box 正常。
- 运行状态必须分别表达 Web、Core 和 sing-box 的健康情况。
- 修改类命令必须有明确的同步或异步语义，并保证重复提交不会制造并发进程。
- 错误响应使用统一 envelope，不向浏览器回显命令行、敏感路径或原始秘密。
- 请求体必须有大小限制，handler 必须设置合理的读取、写入和操作超时。
- OpenAPI 3.1 文档是 HTTP API 的契约源；开始实现业务端点时必须同步提交 schema，
  由其生成 TypeScript API 类型，并以契约测试验证 Go handler 没有漂移。
- 节点探测类接口必须限制节点数、并发和单节点超时，并在 DNS 解析后阻止访问回环、
  私网、链路本地、多播和未指定地址；响应不得包含节点认证材料。

建议错误 envelope：

```json
{
  "error": {
    "code": "config_invalid",
    "message": "Configuration validation failed",
    "details": {}
  },
  "requestId": "..."
}
```

错误 `code` 是稳定契约；`message` 用于展示，不能作为前端控制流依据。

## 7. SSE 事件模型

事件至少包含：

```text
id
type
timestamp
payload
```

- 建立连接后先发送权威状态快照，再发送增量事件。
- 浏览器重连后不得假设丢失的状态事件仍可完整重放；应重新获取快照。
- 日志事件使用有界环形缓冲并携带游标，慢客户端不得阻塞 Core。
- 心跳只用于连接存活判断，不触发 React 业务状态更新。
- 事件流使用与其他 API 相同的认证和 Origin 策略。

## 8. 本地 Web 安全基线

localhost 不是认证边界。首期必须实现：

- Go API 与 Vite 默认只监听 `127.0.0.1`；IPv6 回环必须显式开启。
- 禁止通过普通配置意外切换到 `0.0.0.0`。
- 精确校验 `Host`、`Origin` 和浏览器 Fetch Metadata。
- 生产模式不启用通配 CORS；开发模式只允许精确的 Vite origin。
- 所有修改接口均使用非 GET 方法，并同时具备会话认证和 CSRF 防护。
- 首次运行使用高熵一次性配对秘密建立本地会话；长期秘密不得放入 URL、日志或
  `localStorage`。会话使用 `HttpOnly`、`SameSite=Strict` Cookie，并配合独立
  CSRF token。具体交互应在实现前形成安全 ADR 和威胁测试。
- 设置 CSP、`frame-ancestors 'none'`、`X-Content-Type-Options` 和合理的
  Referrer Policy。
- sing-box 的管理 API 必须使用独立随机密钥，并只监听回环或 Unix Socket。

最小特权 Helper 若存在，IPC 必须使用权限受限的 Unix Socket，验证 peer UID，
只接受版本化、定长受限、参数白名单化的命令。禁止开放通用 shell、任意路径读写
或任意网络配置接口。

## 9. 文件与目录

开发默认值遵循 XDG，但允许显式覆盖：

```text
$XDG_CONFIG_HOME/sing-box-webui/
$XDG_STATE_HOME/sing-box-webui/
$XDG_RUNTIME_DIR/sing-box-webui/
```

仓库内测试和演示数据只能写入被 Git 忽略的 `var/`。测试必须使用临时目录，禁止
修改开发者真实的 sing-box 配置。

环境变量采用 `SING_BOX_WEBUI_` 前缀。秘密不通过命令行参数或环境变量长期传递。

## 10. 资源与依赖预算

以下为 Web 管理面预算，不包含浏览器和外部 sing-box：

- 空闲状态管理面所有 Go 进程合计 RSS 目标不高于 50 MiB。
- 无连接变化时，五分钟平均 CPU 目标低于单核 1%。
- 队列、日志和事件缓存必须有硬上限。
- 基础首屏 JavaScript 目标 gzip 后不高于 300 KiB；重型功能必须动态加载。
- 不允许遥测、远程字体或运行时 CDN 依赖。

预算是持续测量指标，不得通过删除必要的权限隔离或错误处理来达成。

## 11. 质量门槛

实现开始后，变更至少应通过：

```text
go test -race ./...
go vet ./...
npm run lint
npm run typecheck
npm test
npm run build
```

测试分层：

- Go 单元测试：supervisor、配置事务、错误映射和安全校验。
- 集成测试：使用 fake sing-box 可执行文件覆盖启动、超时、崩溃和错误输出。
- API 契约测试：验证 schema、状态码、幂等和请求大小限制。
- 前端测试：组件状态和 API 数据边界。
- Playwright 冒烟测试：本地启动、页面加载、断线和错误态。
- 安全测试：回环绑定、跨 Origin 请求、路径穿越、符号链接和命令注入。
- 资源基准：空闲 CPU/RSS、有界日志和慢 SSE 客户端。

## 12. 实现前仍需 ADR 的事项

以下不是可忽略项，而是必须在相关代码开始前单独决策：

1. Linux TUN 最小特权机制及 Core/Helper 生命周期。
2. 本地首次配对、会话恢复和秘密轮换流程。
3. 支持的 sing-box 最低/最高版本和能力矩阵。
4. Core 崩溃后的自动重启上限及后端退出语义。

这些 ADR 不阻塞普通用户模式的项目骨架，但阻塞 TUN、危险修改接口和稳定进程
监督的发布级实现。状态统一记录在 [ADR 索引](adr/README.md)。

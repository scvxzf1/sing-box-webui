export const meta = {
  name: 'backend-audit',
  description: '审计 sing-box-webui 后端 Go 代码：多维度审查 + 对抗性验证',
  phases: [
    { title: 'Review', detail: '并行多维度审查后端代码' },
    { title: 'Verify', detail: '逐条对抗性验证发现' },
    { title: 'Synthesize', detail: '汇总确认的发现' },
  ],
}

const PROJECT_CONTEXT = `
项目：sing-box-webui（Go 1.24 后端 + React 前端），一个 sing-box 代理核心的控制面。
工作目录：/home/scv/nvme0n1p1/构思-预备/sing-box-webui
后端代码位于 internal/ 和 cmd/webui/。模块路径：sing-box-webui。
关键包：
- cmd/webui/main.go（169行，入口）
- internal/api/（HTTP 路由、认证、中间件、订阅/池/规则/连接/DNS 端点）
- internal/core/（manager，整合 sing-box 客户端）
- internal/singbox/（config.go 452行 生成 sing-box 配置；client.go 调用 sing-box API）
- internal/supervisor/（manager_linux.go 管理 sing-box 进程）
- internal/subscription/（parser.go 632行、manager.go 631行、fetcher.go 抓取订阅、url.go 解析订阅URL）
- internal/connmon/（monitor.go 613行 连接监控，可能调用 sing-box clash API）
- internal/routing/（manager.go 579行 路由策略）
- internal/nodepool/（manager.go 551行 节点池）
- internal/poolhealth/（manager.go 574行 池健康检查）
- internal/connectivity/（manager.go 549行 连通性测试）
- internal/latency/（service.go 测速）
- internal/dnsprofile/（manager.go DNS 配置）
- internal/trafficpolicy/（manager.go 流量策略）
- internal/configstore/store.go（224行 配置持久化）
- internal/application/config.go（应用配置）
- internal/platform/systemproxy/gnome_linux.go（改 GNOME 系统代理）
- internal/platform/privilege/elevated_linux.go（特权提升）
- internal/netsafety/address.go（地址安全校验）

审计目标：找出真实缺陷——崩溃、并发问题、安全漏洞（注入、SSRF、路径穿越、认证/授权绕过、命令注入、不安全反序列化、密钥泄漏）、资源泄漏、错误处理缺失、跨包数据竞态、sing-box 配置生成中的逻辑错误。
重要：这是一个本地控制面，通常 localhost 监听，但仍要审查认证（internal/api/auth.go）和 SSRF（订阅抓取）路径。
`

const FINDINGS_SCHEMA = {
  type: 'object',
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          file: { type: 'string', description: '仓库相对路径，如 internal/singbox/config.go' },
          line: { type: 'integer', description: '1-indexed 行号' },
          category: { type: 'string', description: 'kebab-case 分类：crash/concurrency/security-injection/security-ssrf/security-auth/path-traversal/resource-leak/error-handling/logic/config-gen/crypto/secrets/key-mgmt' },
          severity: { type: 'string', enum: ['critical', 'high', 'medium', 'low'] },
          short_summary: { type: 'string', description: '≤60字 缺陷标签，仅结论' },
          summary: { type: 'string', description: '一句话描述缺陷' },
          failure_scenario: { type: 'string', description: '具体输入/状态 → 错误输出/崩溃，可复现' },
        },
        required: ['file', 'line', 'category', 'severity', 'short_summary', 'summary', 'failure_scenario'],
      },
    },
  },
  required: ['findings'],
}

const VERDICT_SCHEMA = {
  type: 'object',
  properties: {
    verdict: { type: 'string', enum: ['CONFIRMED', 'REFUTED', 'UNCERTAIN'] },
    is_real: { type: 'boolean', description: 'true=真实缺陷（CONFIRMED/UNCERTAIN 算真），false=误报（REFUTED）' },
    severity: { type: 'string', enum: ['critical', 'high', 'medium', 'low'] },
    reasoning: { type: 'string', description: '基于代码的验证理由，引用具体行号/函数' },
    correction: { type: 'string', description: '若原发现不准确，给出修正（更正行号/分类/场景）' },
  },
  required: ['verdict', 'is_real', 'severity', 'reasoning'],
}

// 审查维度：每个维度一个 agent，各管一个文件子集 + 一个审查视角
const DIMENSIONS = [
  {
    key: 'api-security',
    label: 'api-layer-security',
    prompt: `审查 internal/api/ 全部文件（auth.go context.go middleware.go response.go core.go connectivity.go decode.go dns.go links.go pools.go rule_pools.go rules.go runtime.go server.go subscriptions.go traffic_policy.go）+ cmd/webui/main.go。
聚焦：HTTP 认证/授权（auth.go 的密码/会话/token 校验、是否常量时间比较、是否默认空密码）、中间件（CORS、CSRF、路由保护、可选 allow-lan 监听 0.0.0.0 时的暴露面）、请求解码（decode.go 是否有资源耗尽/ReDoS/超大 body）、SSRF 与路径穿越在 API 入参的处理、响应是否泄漏内部信息、错误码是否区分认证失败与内部错误。读取每个文件完整内容。`,
  },
  {
    key: 'subscription-ssrf',
    label: 'subscription-fetch-parse',
    prompt: `审查 internal/subscription/ 全部文件（fetcher.go manager.go parser.go model.go url.go）。
聚焦：SSRF（fetcher.go 抓取订阅 URL——是否限制 scheme/http重定向/私网地址/端口/重定向次数；url.go 解析订阅URL 的各种形式是否安全）、解析器健壮性（parser.go 632行——畸形输入是否 panic/无限循环/内存爆炸；base64 解码、split、URL 解析的边界）、并发抓取时的资源管理、订阅内容是否被原样写入 sing-box 配置（注入风险）。读取每个文件完整内容。`,
  },
  {
    key: 'singbox-config',
    label: 'singbox-config-generation',
    prompt: `审查 internal/singbox/config.go（452行）、client.go、internal/core/manager.go（547行）、internal/core/assets.go。
聚焦：sing-box 配置生成的逻辑正确性（JSON 构造、路由规则、DNS、inbounds/outbounds、TUN、mixed inbound 监听地址、allow-lan 0.0.0.0 暴露）、与 sing-box 字段名/枚举值是否匹配、可被外部输入（节点URL/订阅字段）注入的配置字段、client.go 调用 sing-box Clash/API 的错误处理与超时、core manager 的并发与生命周期。读取每个文件完整内容。`,
  },
  {
    key: 'supervisor-process',
    label: 'process-supervisor-privilege',
    prompt: `审查 internal/supervisor/manager_linux.go（193行）、state.go、internal/platform/privilege/elevated_linux.go、internal/platform/systemproxy/gnome_linux.go、internal/platform/paths/paths.go、internal/control/service.go（463行）。
聚焦：命令/进程执行（是否 exec.Command 用户输入字段、参数注入、shell 调用）、setcap/特权提升路径、gsettings 调用是否安全拼接、systemd 服务文件生成、路径拼接（paths.go 是否被用户输入影响导致穿越）、进程生命周期与僵尸进程、信号处理。读取每个文件完整内容。`,
  },
  {
    key: 'concurrency',
    label: 'concurrency-state-mgmt',
    prompt: `审查并发与状态管理。重点看 internal/connmon/monitor.go（613行）、internal/routing/manager.go（579行）、internal/nodepool/manager.go（551行）、internal/poolhealth/manager.go（574行）、internal/connectivity/manager.go（549行）、internal/events/broker.go、internal/latency/service.go。
聚焦：数据竞态（共享 map/slice 无锁访问、goroutine 读写、定时器 tick 闭包捕获循环变量）、context 取消传播、goroutine 泄漏（无退出的 for-select、ticker 未 Stop、通道无人接收）、定时任务并发执行同一资源、WaitGroup 误用、panic 在 goroutine 内未 recover 导致进程退出。读取每个文件完整内容。`,
  },
  {
    key: 'configstore-persist',
    label: 'persistence-config-loading',
    prompt: `审查配置持久化与加载。看 internal/configstore/store.go（224行）、internal/application/config.go、internal/dnsprofile/manager.go、internal/trafficpolicy/manager.go、internal/routing/pools.go、internal/routing/model.go、internal/netresolve/resolver.go、internal/netsafety/address.go。
聚焦：文件读写原子性（写配置是否 temp+rename、损坏文件恢复）、JSON 解码对未知字段/类型错误的处理、路径穿越（持久化目录是否被外部输入影响）、并发读写同一存储、默认值与边界（空列表、nil map）、netsafety 地址校验是否被订阅 URL 绕过、TLS/证书校验。读取每个文件完整内容。`,
  },
]

const results = await pipeline(
  DIMENSIONS,
  d => agent(d.prompt, { label: d.label, phase: 'Review', schema: FINDINGS_SCHEMA }),
  (review, d) => {
    const fs = (review && review.findings) || []
    if (fs.length === 0) return []
    return parallel(fs.map(f => () =>
      agent(
        `对抗性验证以下后端审计发现。默认怀疑——除非代码确实如此，否则标记 REFUTED。\n\n${PROJECT_CONTEXT}\n\n发现：\n- 文件: ${f.file}:${f.line}\n- 分类: ${f.category}\n- 严重性: ${f.severity}\n- 摘要: ${f.summary}\n- 失败场景: ${f.failure_scenario}\n\n任务：\n1. 用 Read 读取 ${f.file} 的相关行（至少读该行周围 ±30 行，必要时读整个文件确认上下文）。\n2. 判断该发现是否为真实缺陷。常见误报：把有保护的代码当漏洞、忽略调用方的校验、行号不准但问题真实（这种算 CONFIRMED 但在 correction 注明真实行号）、把测试/示例代码当生产代码、忽略了已有的锁/边界检查。\n3. 若问题真实但分类或行号不准，verdict 给 CONFIRMED 或 UNCERTAIN，并在 correction 修正。\n4. 若发现是臆测（未读代码就断言"可能有"），判 REFUTED。\n5. reasoning 必须引用具体代码（函数名/行号/关键表达式）作为证据。`,
        { label: `verify:${f.file}:${f.line}`, phase: 'Verify', schema: VERDICT_SCHEMA }
      ).then(v => ({ ...f, dimension: d.key, verdict: v }))
    ))
  }
)

const confirmed = results.flat()
  .filter(Boolean)
  .filter(f => f.verdict && f.verdict.is_real)

phase('Synthesize')
log(`审查完成，原始发现 ${results.flat().filter(Boolean).length} 条，确认 ${confirmed.length} 条`)

return {
  total_raw: results.flat().filter(Boolean).length,
  confirmed_count: confirmed.length,
  confirmed: confirmed
    .sort((a, b) => {
      const order = { critical: 0, high: 1, medium: 2, low: 3 }
      return (order[a.severity] ?? 9) - (order[b.severity] ?? 9)
    })
    .map(f => ({
      file: f.file,
      line: f.line,
      category: f.category,
      severity: f.verdict.severity || f.severity,
      short_summary: f.short_summary,
      summary: f.summary,
      failure_scenario: f.failure_scenario,
      verdict: f.verdict.verdict,
      reasoning: f.verdict.reasoning,
      correction: f.verdict.correction || '',
      dimension: f.dimension,
    })),
}

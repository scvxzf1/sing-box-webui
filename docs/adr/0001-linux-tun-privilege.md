# ADR-0001: Linux TUN 最小特权边界

- 状态：accepted
- 日期：2026-08-25

## 背景

TUN 需要网络管理能力，但浏览器可达的 Web/API 进程不应以 root 运行，也不应持有
`CAP_NET_ADMIN`。最初设想过独立特权 Helper；实际 Linux 实现已经采用更小的文件能力
边界，并经过本机 TUN 启停验证。

## 决策

1. Web/API 始终以桌面普通用户运行，检测到有效用户 ID 为 0 时拒绝启动。
2. sing-box 作为独立受管进程运行。只有解析符号链接后得到的实际 sing-box 版本文件可
   被授予 `cap_net_admin+ep`；Go、Node.js、Vite、Web/API 和启动脚本不得获得该能力。
3. supervisor 只以固定参数执行已选择的 sing-box 文件，不接受来自 HTTP 的 shell、
   任意 argv 或任意配置路径。运行配置必须先经过后端校验和 sing-box `check`。
4. `systemd-resolved` 集成使用绑定到当前本机活动用户的 Polkit 规则，并只允许 TUN
   生命周期需要的四个明确 action；禁止授权 `org.freedesktop.resolve1.*` 通配范围。
5. 当前实现不引入常驻特权 Helper。若未来需要防火墙、任意路由或其他不能由单个文件
   capability 表达的权限，必须以新 ADR 设计受限 IPC，不能扩大 Web/API 权限。

## 影响

- 托管核心更新和回滚切换到另一个版本文件后不会继承 capability，用户必须对新的实际
  文件重新授权；启动脚本必须检测并给出明确命令。
- 持有 capability 的 sing-box 会解释后端生成的配置，因此认证、配置校验、路径所有权
  和禁止任意参数仍属于特权边界的一部分。
- 普通系统代理模式不需要 capability，也不需要 Polkit 规则。

## 已拒绝方案

- 以 root 运行 Web/API 或整个 systemd 服务。
- 对 Go、Vite、开发脚本或目录中的所有二进制授予能力。
- 从默认开发命令静默调用 `sudo` 或 `pkexec`。
- 在当前权限需求下引入拥有通用命令接口的 root Helper。

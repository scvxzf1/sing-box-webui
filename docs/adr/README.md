# 架构决策索引

ADR 用于记录会改变安全边界、跨平台契约或长期维护成本的技术决定。普通实现细节不
需要 ADR。

## 状态

- `proposed`：已有候选方案，尚不能约束实现。
- `accepted`：已经评审，相关实现必须遵守。
- `superseded`：已由新的 ADR 替代，保留历史原因。
- `rejected`：明确不采用，避免未来重复讨论。

## 决策目录

| 编号 | 状态 | 决策 | 当前结论或剩余问题 |
| --- | --- | --- | --- |
| [ADR-0001](0001-linux-tun-privilege.md) | accepted | Linux TUN 最小特权边界 | 普通用户 Web/API；仅 sing-box 文件持有 `CAP_NET_ADMIN`；DNS 使用精确 Polkit 授权 |
| [ADR-0002](0002-local-web-authentication.md) | accepted | 本地 Web 鉴权与秘密轮换 | 首次生成文件 token；短期签名会话；修改 token 并重启完成轮换 |
| [ADR-0003](0003-sing-box-compatibility.md) | proposed | sing-box 版本兼容策略 | 核心托管已实现；最低版本、能力门控和升级兼容矩阵仍未决定 |
| [ADR-0004](0004-crash-restart-policy.md) | accepted | 崩溃重启及退出语义 | 同一 generation 有界重启 3 次；指数退避带抖动；耗尽后失败关闭并恢复系统代理 |

已进入实现的决策必须拥有独立 ADR，不能只在索引中保留一句候选描述。交叉开发形成
事实决策后，应在同一轮文档核对中更新状态；`proposed` 只表示剩余问题尚不能约束实现，
不再笼统表示整个功能尚未开发。

## ADR 模板

```markdown
# ADR-NNNN: 标题

- 状态：proposed
- 日期：YYYY-MM-DD
- 决策人：

## 背景

## 约束

## 候选方案

## 决策

## 安全与资源影响

## 验证方式

## 后果与迁移
```

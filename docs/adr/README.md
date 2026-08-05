# 架构决策索引

ADR 用于记录会改变安全边界、跨平台契约或长期维护成本的技术决定。普通实现细节不
需要 ADR。

## 状态

- `proposed`：已有候选方案，尚不能约束实现。
- `accepted`：已经评审，相关实现必须遵守。
- `superseded`：已由新的 ADR 替代，保留历史原因。
- `rejected`：明确不采用，避免未来重复讨论。

## 阻塞性决策队列

| 编号 | 状态 | 决策 | 阻塞内容 |
| --- | --- | --- | --- |
| ADR-0001 | proposed | Linux TUN 最小特权机制和 Core/Helper 生命周期 | TUN 实现 |
| ADR-0002 | proposed | 本地首次配对、浏览器会话和秘密轮换 | 危险修改 API |
| ADR-0003 | proposed | sing-box 支持版本和能力矩阵 | 稳定进程监督 |
| ADR-0004 | proposed | 崩溃重启上限及 Web/Core 退出语义 | 稳定进程监督 |

当某个阻塞项进入开发前，应创建对应的 `NNNN-short-title.md`，完成方案比较、安全
影响、资源影响、迁移方式和最终决定。不能只把讨论结论留在 issue 或聊天记录中。

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

# devmaster — 研发生产管理

面向软件研发的执行事实源：**工作项、冲刺、版本、工时**。参考 Jira 的任务/缺陷/冲刺/版本迭代
四件套，但只保留其中可被核算的部分，并把它们接进生态的项目与财务链路。

**当前状态：设计阶段（2026-10-27）。** D1～D4 与 O1 已确认，02/03/04/06 已展开，尚无代码。
评估与裁决见 [docs/01-方案评估与关键决策.md](docs/01-方案评估与关键决策.md)。

## 定位

devmaster 不拥有项目、不拥有账、不拥有部署、不拥有生产事件。它只回答四个问题：

1. 谁在改什么、为什么改、改到哪一步（工作项与状态迁移）；
2. 这一轮迭代承诺了什么、中途加了多少（冲刺与范围变更）；
3. 这版软件包含哪些改动、什么时候发的、谁批的（版本与发布记录）；
4. 这些改动花了多少人工（工时）。

它存在的理由不是"缺一块看板"，而是三条可追溯链：

```text
链 A 影响面   BUG → 影响版本 → it-solution 部署 → 客户
链 B 投入产出 工时 → PPM(LABOR 实际) → FI → 项目毛利
链 C 交付合规 版本 → approver 审批 → artifact 证据 → 部署/验收
```

三条链任一不通，本项目应停在 M3 或直接改用 Gitea Issues。

## 边界（一句话版）

| 事实 | 权威源 |
|---|---|
| 工作项 / 冲刺 / 软件版本 / 研发工时 | **devmaster** |
| 项目结构 / WBS / 预算 / EVM | PPM（devmaster 只做弱引用） |
| 部署 / 客户验收 / 订阅 | it-solution |
| 生产事件 / 问题 / 变更 / CMDB | it-swarm |
| 产品工程 Release（Rev A/B、BOM、ECO） | PLM（与软件 Version 不同义） |
| 凭证 / 总账 / 成本 | FI（不直连，经 PPM 结算） |
| 审批 / 文档证据 | approver / artifact |
| 代码 / 分支 / PR / tag | Gitea（:3123） |

## 技术栈

Next.js（App Router + BFF）· Go + ent + gRPC · Temporal · PostgreSQL 18 · NATS JetStream ·
Keycloak（realm `devmaster`）。本地开发全部使用 Homebrew 原生服务，不引入 Docker。

| 组件 | 端口 |
|---|---|
| gRPC | `50091` |
| HTTP 网关（healthz / metrics / Gitea webhook） | `8291` |
| web | `3031` |
| 数据库 | `devmaster_dev` / `devmaster_test` |
| NATS 流 | `DEVMASTER_EVENTS`（`dev.v1.>`） |
| Temporal namespace | `devmaster` |

## 文档

| 文档 | 内容 |
|---|---|
| [01 方案评估与关键决策](docs/01-方案评估与关键决策.md) | 定位、边界、ADR、领域模型草案、契约、分期、风险 |
| [02 需求范围与系统边界](docs/02-需求范围与系统边界.md) | 功能域、v1 范围、不做清单、边界矩阵、角色、术语、非功能 |
| [03 总体架构与技术落地](docs/03-总体架构与技术落地.md) | 进程、请求链路、事务、读模型、幂等、安全、运维、容量 |
| [04 领域模型与数据设计](docs/04-领域模型与数据设计.md) | 聚合、表结构、状态机、不变量、编号与排序、事件、测试 |
| [06 集成契约与事件设计](docs/06-集成契约与事件设计.md) | 事件规范、Gitea/PPM/it-solution/approver/artifact 契约、失败恢复 |
| 05 指标与报表口径 | 待写（当前定义见 02 §3.8） |
| [07 Temporal 工作流设计](docs/07-Temporal工作流设计.md) | 发布、冲刺时钟、缺陷计时、Gitea 对账、定时任务、ID 规则、测试 |
| 08 研发计划与里程碑 | 待写（当前见 01 §8） |
| 09 运维手册 | 待写 |

原始需求：[docs/drafts/draft.md](docs/drafts/draft.md) ·
参考：[docs/drafts/jira-like-system-draft.md](docs/drafts/jira-like-system-draft.md)

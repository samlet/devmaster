# devmaster — 研发生产管理

面向软件研发的执行事实源：**工作项、冲刺、版本、工时**。参考 Jira 的任务/缺陷/冲刺/版本迭代
四件套，但只保留其中可被核算的部分，并把它们接进生态的项目与财务链路。

**当前状态：M0 骨架已落地（2026-10-27）。** 产品垂直切片（gRPC + PG + outbox + relay +
Temporal worker）、只读控制台、迁移与门禁可运行；工作项/冲刺/版本/工时在 M1–M4 逐步落地。
评估与裁决见 [docs/01](docs/01-方案评估与关键决策.md)，分期见 [docs/01 §8](docs/01-方案评估与关键决策.md)。

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

三条链任一不通，本项目应停在 M3 或直接改用 Gitea Issues。链 B 的 PPM 侧已落地
（PPM `docs/06 §9`，`labor_rates` + `dev.v1.work.logged` 消费者）。

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

## 快速开始

前置：本地 PostgreSQL 18、NATS JetStream、Temporal 已启动（brew 服务，不引入 Docker）。

```bash
make db-create        # devmaster_owner / devmaster_app 与 devmaster_dev / devmaster_test
make migrate          # Atlas 版本化迁移 + 收紧运行时角色
make run              # gRPC :50091 / HTTP :8291
make web-install      # 首次
make web-dev          # 控制台 http://127.0.0.1:3031
```

`make dev` 同起 server + relay + worker；`relay` 负责把 outbox 投递到
`DEVMASTER_EVENTS`（`dev.>`）。**consumer 在 M4 接 Gitea webhook 时引入**，M0 没有入站源。

创建第一个产品（本地关闭鉴权，注入 demo 主体）：

```bash
grpcurl -plaintext -d '{"options":{"idempotency_key":"p-1"},"key":"hosp","name":"酒店 PMS"}' \
  127.0.0.1:50091 dev.v1.ProductService/CreateProduct
```

同一个 key 重放返回同一结果；同一个 key 换 payload 返回 `Aborted`。

## 进程与端口

| 进程 | 作用 | 监听 |
|---|---|---|
| `server` | gRPC + HTTP（healthz / metrics / 只读 `/api/*`） | `:50091` / `:8291` |
| `relay` | outbox → JetStream | 无 |
| `worker` | Temporal worker（M0 只有 PingWorkflow） | 无 |
| `web` | Next.js 只读控制台 | `:3031` |

```bash
curl -s localhost:8291/healthz   # postgres / nats / temporal，任一失败 503
curl -s localhost:8291/metrics | grep '^devmaster_'
```

## 门禁

```bash
make test              # buf lint + go test
make test-integration  # 真连 PG 的集成测试（-p 1）
make lint              # buf lint + go vet
make build             # 编译全部进程
make proto-check       # 生成物是否有未提交改动
make web-check         # 前端 tsc --noEmit
```

集成测试默认跳过（`DEVMASTER_RUN_INTEGRATION=1` 才跑），避免在没有库的机器上误红。

## 仓库结构

```text
backend/
  cmd/            server / relay / worker
  internal/
    platform/     config / database / auth / outbox / jetstream / idempotency / observability
    product/      domain / application / adapter/postgres（M0 垂直切片）
    transport/    grpc / http
    workflow/     Temporal 工作流与活动
  ent/schema/     ent schema（迁移才是 DDL 的唯一来源）
  migrations/     Atlas 版本化迁移
  configs/        local.example.yaml
proto/            buf 工作区（dev/v1/*.proto，生成到 backend/gen/go）
web/              Next.js 只读控制台
assets/           数据包规范与走查数据
docs/             01 评估 / 02 边界 / 03 架构 / 04 模型 / 06 契约 / 07 工作流
```

## 文档

| 文档 | 内容 |
|---|---|
| [01 方案评估与关键决策](docs/01-方案评估与关键决策.md) | 定位、边界、ADR、分期、风险 |
| [02 需求范围与系统边界](docs/02-需求范围与系统边界.md) | 功能域、v1 范围、边界矩阵、角色、术语 |
| [03 总体架构与技术落地](docs/03-总体架构与技术落地.md) | 进程、请求链路、事务、读模型、幂等、安全 |
| [04 领域模型与数据设计](docs/04-领域模型与数据设计.md) | 聚合、表结构、状态机、不变量、事件 |
| [06 集成契约与事件设计](docs/06-集成契约与事件设计.md) | 事件规范、Gitea/PPM/it-solution/approver 契约 |
| [07 Temporal 工作流设计](docs/07-Temporal工作流设计.md) | 发布、冲刺时钟、缺陷计时、对账 |
| 05 指标与报表口径 | 待写（当前定义见 02 §3.8） |
| 08 研发计划与里程碑 | 待写（当前见 01 §8） |
| 09 运维手册 | 待写 |

原始需求：[docs/drafts/draft.md](docs/drafts/draft.md) ·
参考：[docs/drafts/jira-like-system-draft.md](docs/drafts/jira-like-system-draft.md)

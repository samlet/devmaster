# AGENTS.md — 给 AI 协作代理的项目约定

这份文件写给在这个仓里干活的 AI 代理（也包括三个月后忘了细节的人）。
它只讲**这个项目特有的东西**：不写通用编程常识，也不重复 README 里的产品介绍。

产品定位、需求边界见 [README.md](README.md) 与 [docs/02](docs/02-需求范围与系统边界.md)。

## 1. 一句话理解这个项目

devmaster 是**研发执行的事实源**：工作项、冲刺、版本、工时。它不拥有项目、账、部署、生产事件。

三条可追溯链是全部价值所在，任何改动都应该问一句「它让哪条链更通还是更断」：

```text
链 A 影响面   BUG → 影响版本 → 部署 → 客户
链 B 投入产出 工时 → PPM(LABOR) → FI → 项目毛利
链 C 交付合规  版本 → approver 审批 → artifact 证据 → 部署
```

## 2. 技术栈与目录

```text
backend/          Go 1.27 · gRPC(buf) + net/http · ent · PostgreSQL 18(RLS) · Temporal · NATS JetStream
web/              Next.js 15 · React 19 · 无 UI 库（手写 CSS）
proto/dev/v1/     契约唯一来源；backend/gen/go 是生成物，不要手改
docs/01–10        设计与裁决。01 评估、04 模型、06 契约、08 里程碑、10 控制台
scripts/          运维脚本（dev-stack / keycloak-dev / bootstrap-db …）
deploy/keycloak/  本地 IdP 的 realm 定义
```

**四层结构**（后端）：`domain`（纯逻辑，不 import 传输层）→ `application`（用例）→ `adapter/postgres`（持久化）→ `transport/{grpc,http}`。

## 3. 常用命令

```bash
make status                 # 全栈状态（基础设施 + 应用层）
make up / make down         # 起停应用层（Keycloak/registry/server/web），不动基础设施
make check                  # buf lint + go vet + go test（提交前必跑）
make test-integration       # 真连 PG
make web-check              # tsc --noEmit
make web-env                # 首次生成 web/.env.local（session secret 随机）
```

登录控制台用 `./scripts/keycloak-dev.sh info` 查看测试账号。

## 4. 硬约定（违反会被测试或生产环境挡住）

### 4.1 领域错误必须翻译成人话

领域层用 `apperr.Kind*` 表达语义，传输层翻译成 HTTP/gRPC 码。**唯一索引冲突也要翻译**——
`sprints_one_active_idx` 曾经返回 `internal error`，用户对着它猜不出「你得先结束上一个冲刺」。

写新端点时先问：**这个操作有哪些唯一索引/前置条件会拦？**

### 4.2 写操作的身份一律取自会话

控制台的写端点（`/api/*` 的 POST/PATCH/DELETE）走 BFF HMAC 签名，`actor` 与角色**放在 body 里**
（签名覆盖 body，改不动；header 不参与签名，放身份等于留一个可篡改的口子）。

客户端传什么都覆盖不了 `submitted_by` / `author_id`。

### 4.3 append-only 的表不能删

`work_item_events`、`work_logs` 有触发器拒绝 `UPDATE/DELETE`。
**造演示数据前先想清楚怎么收场**——这两张表一写就收不回来。

可删的：`comments`、`work_item_links`、`release_records`、`versions`、`sprints`。

### 4.4 前端无 JS 优先

除看板拖拽（`app/board/draggable-board.tsx`）外，所有页面都是 server component + 原生 `<form>` + 303 回跳。
写操作走 `app/api/**/route.ts` 的 BFF 通道，签名在服务端完成。

加新页面时默认不要 `"use client"`。

### 4.5 状态机与校验由后端给

前端不复制状态机：工作项的可流转目标由 `GET /api/work-items` 的 `next_statuses` 返回，
来自领域 `AllowedTransitions`。硬编码在前端，状态机一改界面就骗人。

### 4.6 契约变更走 proto

改 `proto/dev/v1/*.proto` → `make proto` → 生成物入库。不要手改 `gen/go`。

## 5. 已知的坑（踩过的）

| 坑 | 表现 | 处理 |
|---|---|---|
| 本机沙箱注入 `NODE_OPTIONS` | `next dev` 启动即退出，报 `safe-delete` / `.next` 删除失败 | `env -u NODE_OPTIONS make web-dev`（`make up` 已内置） |
| JSX 兄弟元素 | 往 `{cond ? (...) : (...)}` 的 else 分支插新元素，报「must have one parent element」 | 把 `: (` 改成 `: null}`，让元素平级 |
| Keycloak 内置 scope | realm import 时 `profile`/`email`/`roles` 尚未创建，引用被**静默忽略**，登录报 `Invalid scopes` | 在 realm JSON 的 `clientScopes` 里显式定义 |
| `go run` / `pnpm` 派生子进程 | 杀父进程后端口仍被占 | 先 `pkill -P <pid>` 再杀父（`dev-stack.sh` 已处理） |
| 造数据用错产品 | HOSP 已有 ACTIVE 冲刺，用它验证「开始新冲刺」必然撞唯一约束 | 验证脚本要选没有 active 冲刺的产品 |

## 6. 提交约定

- Conventional Commits 前缀 + 中文正文；正文说清**为什么**，不只是改了什么。
- 提交前跑 `make check`（后端）+ `make web-check`（前端）。
- 一次提交一个主题。跨仓改动（devmaster + artifact + subject-registry）各自提交，
  在提交信息里互相引用 hash。

## 7. 跨仓依赖

`backend/go.mod` 有两个本地 replace：

```text
github.com/xiaofeiwu/artifact          => ../../artifact
github.com/xiaofeiwu/subject-registry  => ../../../portals/subject-registry
```

**克隆时需把 artifact 放在 devmaster 同级、subject-registry 放在 `~/portals/` 下**，
否则 `go build` 直接失败。这是有意的：两个仓的契约就是编译期依赖，不该靠复制粘贴同步。

## 8. 验收口径

「测试绿」不等于「能用」。这个项目的历次 bug 都在**真栈**里才暴露：

- 只跑单测会漏掉：唯一约束、RLS、CAS 冲突、JSON 字段名不匹配、Keycloak 配置。
- 涉及写链路的改动，至少跑一次 `make up` + 浏览器操作 + 查库核对。
- 报表类改动用 `make perf-baseline` 看 P95，别只看功能对不对。

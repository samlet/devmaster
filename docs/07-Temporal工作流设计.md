# 07 Temporal 工作流设计

> 上游：[03 §4 事务与一致性](03-总体架构与技术落地.md)、[06 §7 发布契约](06-集成契约与事件设计.md)。
> 原则：**领域事实在 PostgreSQL，Workflow 只负责"推进"和"等待"**；Activity 做外部副作用且必须幂等；
> Workflow 代码必须确定性（不读时钟、不发网络、不随机）。

## 1. 边界

| 放在 Temporal | 不放在 Temporal |
|---|---|
| 发布审批等待、打 tag、存证、发布 | 状态迁移本身（同步 gRPC，毫秒级） |
| 冲刺开始/结束提醒、每日快照 | 看板查询、搜索 |
| 缺陷修复时限计时与升级 | 工时写入 |
| Gitea 每日对账 | webhook 接收（HTTP 同步） |
| 幂等键清理、事件折叠对账 | 报表聚合（M6 走 SQL） |

判断标准：**需要"等"（等人、等时间、等外部系统）的进 Temporal；毫秒级同步完成的留在 gRPC 里。**
把同步写也塞进工作流，会让每次点击都多一次 Temporal 往返，且错误处理变复杂。

## 2. 运行配置

| 项 | 值 |
|---|---|
| namespace | `devmaster` |
| task queue | `devmaster-release`（发布）/ `devmaster-maintenance`（时钟、对账、清理） |
| worker | 单进程，两个 queue 都注册（`cmd/worker`） |
| 并发 | `MaxConcurrentActivityTaskPollers=4`，`MaxConcurrentWorkflowTaskPollers=4` |
| 本地启动 | `temporal server start-dev --db-filename ~/.temporal/devmaster.db`（**必须带持久化**） |

两个 queue 的理由：发布工作流可能长时间等待审批（24h），不能占满维护类任务的槽位。

## 3. ReleaseWorkflow

### 3.1 触发与 ID

```text
Workflow ID: release-{version_id}-{release_record_id}
触发: SubmitRelease 写库 + outbox → relay → dev.v1.release.submitted（内部）→ consumer 起流程
      （不在 SubmitRelease 的请求里同步起流程，理由见 06 §7.2）
```

确定性 ID + `WorkflowIDReusePolicy=REJECT_DUPLICATE`：重复投递不会开出第二个发布流程。

### 3.2 状态推进

```text
[Start]
  │
  ├─ Activity CheckSubmittable(version_id, record_id)
  │     版本不是 PENDING_APPROVAL 或 record 不是 PENDING_APPROVAL → 返回 {applied:false}，工作流正常结束
  │
  ├─ Activity StartApproval(record_id)          → approver 实例 ID 写回 release_records
  │
  ├─ Loop: Activity PollApproval(instance_id)   ← 每 10s，最长 24h
  │     ├─ APPROVED → 继续
  │     ├─ REJECTED → Activity MarkFailed(reason) → 通知 → 结束
  │     └─ 超时     → Activity MarkFailed("approval timeout") → 通知 → 结束
  │
  ├─ Activity CheckFourEyes(submitted_by, approver_id)
  │     相同 → MarkFailed("self approval")（approver 侧已拦，这里是双保险）
  │
  ├─ Activity MarkApproved(record_id)
  │     release_records=APPROVED，写 approver_id（审批证据）
  │
  ├─ Activity CreateTag(product, version, commit_to)
  │     tag 已存在且指向同一 commit → 视为成功（幂等）
  │
  ├─ Activity SealEvidence(record_id)
  │     发布说明 + 提交范围 → artifact 固定版本 → evidence_ref 写回
  │
  └─ Activity Publish(record_id)
        release_records=PUBLISHED, versions=RELEASED（同事务）+ outbox E6
```

### 3.3 超时与重试

| Activity | StartToClose | 重试 | 说明 |
|---|---|---|---|
| `CheckSubmittable` | 5s | 3 次，指数退避 | 纯读 |
| `StartApproval` | 10s | 5 次 | approver 幂等（businessKey=version_id） |
| `PollApproval` | 10s | 不重试（循环内处理） | 网络错误视为一次"未完成"，继续轮询 |
| `CheckFourEyes` | 5s | 3 次 | 纯读 |
| `MarkApproved` | 10s | 5 次 | 状态迁移，CAS 幂等 |
| `CreateTag` | 15s | 5 次 | tag 已存在视为成功 |
| `SealEvidence` | 30s | 5 次 | artifact 幂等键带载荷指纹 |
| `Publish` | 10s | 5 次 | 同事务写版本与 outbox，天然幂等 |

**整体超时**：`WorkflowExecutionTimeout=48h`（审批 24h + 后续步骤余量）。

### 3.4 取消与补偿

- 审批中被取消：`MarkFailed("cancelled")`，**不打 tag、不存证**；
- 打 tag 后失败：tag 保留（Gitea 的事实），`ReleaseRecord=FAILED` 并记录 `failure_reason`；
  重试时新建 ReleaseRecord，不复用失败的记录（保留失败证据）。
- **不做自动回滚**：回滚是部署域的事（it-solution），devmaster 只记录失败。

### 3.5 Query

工作流暴露 `Query("state")` 返回 `{stage, approval_instance_id, approver_id, error}`，
供 UI 展示"发布卡在哪一步"。Query 只读工作流内存，不查库。

## 4. SprintClockWorkflow

每个 ACTIVE sprint 一条工作流，`Workflow ID: sprint-{sprint_id}`。

```text
[Start]
  ├─ 等待 SprintClosed 信号（或 end_at + 7 天超时）
  │
  ├─ 每日 09:00（组织时区）：Activity SnapshotSprint(sprint_id, date)
  │     幂等 upsert sprint_daily_snapshot
  │
  ├─ 结束前 1 天：Activity NotifySprintEnding(lead, sprint)
  │
  └─ 收到 SprintClosed → Activity FinalSnapshot → 结束
```

- **不自动关闭 sprint**：关闭是业务决定（要选择未完成项的去向），工作流只提醒。
- 每日快照用 `workflow.Sleep` 到下一个 09:00，不用 cron——cron 会在多实例下重复触发。
- 信号处理必须幂等：重复 `SprintClosed` 只执行一次 `FinalSnapshot`（用状态标记）。

## 5. DefectSlaWorkflow

每个 `severity in (BLOCKER, CRITICAL)` 且未关闭的 BUG 一条，`Workflow ID: sla-{work_item_id}`。

```text
[Start]
  ├─ 计时：BLOCKER 24h / CRITICAL 72h（可配）
  ├─ 到 50%：Activity NotifyAssignee(reminder)
  ├─ 到 100%：Activity NotifyEscalation(assignee, lead) + 打标 overdue=true
  ├─ 等待 BugClosed / BugResolved 信号 → 结束
  └─ 等待 BugReopened → 重新计时（最多 3 轮，之后只通知不重开计时器）
```

**不阻断任何操作**：超时只通知，不锁卡片、不改状态。SLA 是节奏工具，不是闸门。

## 6. GiteaReconcileWorkflow

`Schedule: 每日 02:00`，`Workflow ID: gitea-reconcile-{date}`（日期做 ID，天然去重）。

```text
[Start]
  ├─ Activity ListRepos(org)                       → repo 列表
  ├─ 对每个 repo（并行，最多 4 个）：
  │     Activity SyncRepo(repo, cursor)
  │       分页拉 commits → 解析 key → upsert work_item_commits → 推进 repo_sync_cursors
  └─ Activity ComputeLinkRate() → metric devmaster_gitea_link_rate
```

- 单个 repo 失败不影响其他 repo（`ContinueAsNew` 分片或 `errgroup` + 错误收集）；
- 失败保留水位，第二天重跑即可补齐；
- 不删除已有关联（只增），因为 commit 是历史事实。

## 7. 其他定时任务

| 任务 | 方式 | 频率 | 幂等 |
|---|---|---|---|
| 幂等键清理 | Schedule | 每日 03:00 | `DELETE WHERE expires_at < now()` |
| outbox 清理 | Schedule | 每日 03:10 | `DELETE WHERE sent_at < now() - 30d` |
| 事件折叠对账 | Schedule | 每日 04:00 | 只读比对，不一致写 metric |
| `user_profiles` 刷新 | Schedule | 每日 05:00 | 按 sub upsert |
| `audit_field_changes` 清理 | Schedule | 每日 03:20 | 180 天前删除 |
| rank rebalance | Schedule | 每周日 04:00 | 只处理长度 > 24 的列，单事务 |

**统一用 Temporal Schedule，不用 cron / goroutine 定时器**。cron 在多实例下重复触发、
无重试、无历史；goroutine 定时器随进程重启丢失。这是 03 §7.4 的纪律。

## 8. Workflow ID 规则

| 工作流 | ID | 冲突策略 |
|---|---|---|
| Release | `release-{version_id}-{record_id}` | REJECT_DUPLICATE |
| SprintClock | `sprint-{sprint_id}` | USE_EXISTING（重复启动复用） |
| DefectSla | `sla-{work_item_id}` | USE_EXISTING |
| GiteaReconcile | `gitea-reconcile-{yyyy-mm-dd}` | REJECT_DUPLICATE |
| 定时维护 | `maint-{task}-{yyyy-mm-dd}` | REJECT_DUPLICATE |

**ID 里必须带业务主键**：这样"重复触发"变成"幂等"，而不是"开出两条流程"。

## 9. 版本化与升级

- Workflow 代码改动遵循 Temporal 的兼容规则：优先 `workflow.GetVersion` 做补丁；
- 破坏性改动（如 ReleaseWorkflow 增加必填步骤）走**新 Workflow 类型 + 新 ID 前缀**，
  老工作流自然跑完，不做批量迁移；
- 已完成的发布记录不重放：历史事实在 PG，工作流历史只是执行痕迹（保留 30 天）。

## 10. 可观测性

```text
devmaster_workflow_started_total{workflow_type}
devmaster_workflow_failed_total{workflow_type,reason}
devmaster_workflow_duration_seconds{workflow_type}
devmaster_activity_failed_total{activity}
devmaster_release_waiting_approval            （gauge：当前等待审批的发布数）
devmaster_sla_overdue{severity}               （gauge：超时未修的缺陷数）
```

告警：

- `release_waiting_approval > 5` 持续 2 小时 → 审批人可能不在；
- `sla_overdue{severity="BLOCKER"} > 0` → 立即通知 lead；
- `workflow_failed_total` 任意增长 → 看 `failure_reason`。

## 11. 测试

| 层 | 方式 |
|---|---|
| Workflow 单测 | `testsuite.WorkflowTestSuite` + mock Activity，覆盖审批通过/拒绝/超时/自审批 |
| Activity 单测 | 纯函数化（入参出参明确），外部依赖用接口注入 |
| 集成 | 真实 Temporal（本地 7233）+ 真实 PG，跑一次完整发布（approver 用 stub 或真实实例） |
| 重放测试 | 用生产历史做 `ReplayWorkflowHistory`，确保代码改动不破坏确定性 |

**重放测试是硬要求**：Temporal 的确定性错误（读了 `time.Now()`、随机数、map 遍历顺序）
只在重放时暴露，而那时已经在生产里卡住了。

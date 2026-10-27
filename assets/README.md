# 数据包规范

走查数据与正式导入共用一套格式：一个目录 = 一个可重复导入的数据包。
目标不是"能导进去"，而是**同一份数据每次导入都得到同一组事实**，从而让断言可重放。

## 目录结构

```text
assets/datasets/<name>/
  manifest.json        数据包声明（组织、文件清单、版本）
  expectations.json    终局断言（导入后各实体的数量与关键字段）
  00-master/           主数据（产品、组件、成员）
  01-work/             工作项、链接、评论（M1）
  02-sprint/           冲刺与范围事件（M2）
  03-version/          版本与发布（M3）
  04-worklog/          工时（M4）
```

## manifest.json

```json
{
  "name": "m0-smoke",
  "version": 1,
  "organization_id": "00000000-0000-0000-0000-000000000001",
  "files": [
    { "path": "00-master/products.csv", "kind": "products", "required": true }
  ],
  "expectations": "expectations.json"
}
```

规则：

- `organization_id` 必填：数据包不跨组织；
- `kind` 是导入器识别的实体名，不是文件名；
- `required=false` 的文件缺失时跳过并记录，不失败；
- 数据包内所有 CSV 使用 UTF-8、逗号分隔、首行表头。

## expectations.json

断言是数据包的一部分，不是测试脚本的一部分。导入结束后逐条核对：

```json
{
  "product_count": 1,
  "products": [
    { "key": "HOSP", "status": "ACTIVE" }
  ]
}
```

**数量断言与字段断言都要有**：只断数量会让"字段写错但行数对"通过；
只断字段会让"多导了一条重复数据"通过。

## 幂等

导入器按业务键查后建（产品按 `key`，工作项按 `key`，工时按 `work_log_id`）。
重跑同一数据包不产生第二份事实；`--reset` 才整包重建。

## 与 PPM / it-solution 的关系

格式刻意与 PPM 的 `assets/datasets/` 保持一致（manifest + 分目录 CSV + expectations），
这样跨仓走查时可以用同一套工具与同一套读法。差异只在实体名。

## 实体：products

```csv
key,name,description
HOSP,酒店 PMS,面向酒店的管理系统
```

按 `key` 幂等：已存在则跳过，不覆盖已有名称与描述（导入器不替人做改名决定）。

## 实体：work_items

```csv
product_key,type,title,description,priority,severity,status,assignee_id,external_ref
HOSP,TASK,接入 Keycloak 登录,用 OIDC 替换本地登录,P1,,DONE,user-1,m1-1
HOSP,BUG,登录页崩溃,复现步骤见描述,P0,CRITICAL,RESOLVED,user-1,m1-2
```

| 列 | 必填 | 说明 |
|---|---|---|
| `product_key` | ✅ | 必须是已导入的产品 key |
| `type` | ✅ | EPIC / STORY / TASK / BUG / SUBTASK |
| `title` | ✅ | 非空 |
| `external_ref` | ✅ | 导入幂等键；同一组织内唯一 |
| `status` | ❌ | 目标状态。导入器按状态机找合法路径逐级迁移；不填则停在初始态 |
| `priority` | ❌ | P0–P3，默认 P2 |
| `severity` | ❌ | 仅 BUG：BLOCKER/CRITICAL/MAJOR/MINOR/TRIVIAL |
| `assignee_id` | ❌ | 进入 IN_PROGRESS 前必须有值，否则迁移被守卫拒绝 |

**状态必须可达**：例如 TASK 不能直接到 CLOSED（状态机里没有这条边）。
导入器会报"无法从 X 迁移到 Y"，而不是硬改状态 —— 硬改会让事件日志与当前态分叉。

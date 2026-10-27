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

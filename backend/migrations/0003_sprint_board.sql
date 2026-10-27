-- 0003_sprint_board.sql
-- M2：看板、冲刺、范围事件、每日燃尽快照。
--
-- 三个关键约束：
--   1. 一个看板同时只能有一个 ACTIVE 冲刺（部分唯一索引）；
--   2. 范围变更只追加，不改写：ADDED/REMOVED 是事实，不是"调整一下成员"；
--   3. 燃尽快照按天唯一，重跑当天快照是覆盖而不是新增。

CREATE TABLE boards (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    product_id      uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    name            text NOT NULL,
    -- {"IN_PROGRESS": 5}，只提示不阻断（WIP 超限是管理信号，不是数据库错误）
    wip_limits      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT boards_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT boards_product_name_unique UNIQUE (product_id, name)
);

CREATE TABLE sprints (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL,
    board_id         uuid NOT NULL REFERENCES boards (id) ON DELETE RESTRICT,
    name             text NOT NULL,
    goal             text NOT NULL DEFAULT '',
    state            text NOT NULL DEFAULT 'PLANNED',
    start_at         timestamptz,
    end_at           timestamptz,
    capacity_points  smallint,
    committed_points smallint NOT NULL DEFAULT 0,
    version          integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    closed_at        timestamptz,

    CONSTRAINT sprints_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT sprints_state_check CHECK (state IN ('PLANNED', 'ACTIVE', 'CLOSED')),
    CONSTRAINT sprints_window_check CHECK (end_at IS NULL OR start_at IS NULL OR end_at > start_at),
    CONSTRAINT sprints_capacity_check CHECK (capacity_points IS NULL OR capacity_points >= 0),
    CONSTRAINT sprints_board_name_unique UNIQUE (board_id, name)
);

-- 一个看板同时最多一个 ACTIVE 冲刺。没有这条，燃尽图会出现两个"当前冲刺"。
CREATE UNIQUE INDEX sprints_one_active_idx ON sprints (board_id) WHERE state = 'ACTIVE';

ALTER TABLE work_items
    ADD COLUMN sprint_id uuid REFERENCES sprints (id) ON DELETE RESTRICT,
    ADD COLUMN story_points smallint,
    ADD COLUMN estimate_minutes integer;

ALTER TABLE work_items
    ADD CONSTRAINT work_items_story_points_check CHECK (story_points IS NULL OR story_points BETWEEN 0 AND 100),
    ADD CONSTRAINT work_items_estimate_minutes_check CHECK (estimate_minutes IS NULL OR estimate_minutes >= 0);

CREATE INDEX work_items_sprint_rank_idx ON work_items (sprint_id, rank) WHERE sprint_id IS NOT NULL;
CREATE INDEX work_items_board_idx ON work_items (product_id, status, rank);

CREATE TABLE sprint_scope_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL,
    sprint_id       uuid NOT NULL REFERENCES sprints (id) ON DELETE RESTRICT,
    work_item_id    uuid NOT NULL REFERENCES work_items (id) ON DELETE RESTRICT,
    action          text NOT NULL,
    points_at_event smallint,
    actor_id        text,
    note            text,
    occurred_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT sprint_scope_events_action_check CHECK (action IN (
        'COMMITTED', 'ADDED', 'REMOVED', 'COMPLETED', 'REOPENED'))
);

CREATE INDEX sprint_scope_events_sprint_idx ON sprint_scope_events (sprint_id, occurred_at);
-- 承诺与完成各只能记一次：重复记录会让范围蔓延率凭空变大。
CREATE UNIQUE INDEX sprint_scope_events_committed_idx
    ON sprint_scope_events (sprint_id, work_item_id) WHERE action = 'COMMITTED';
CREATE UNIQUE INDEX sprint_scope_events_completed_idx
    ON sprint_scope_events (sprint_id, work_item_id) WHERE action = 'COMPLETED';

CREATE TABLE sprint_snapshots (
    sprint_id        uuid NOT NULL REFERENCES sprints (id) ON DELETE CASCADE,
    -- 冗余组织列：RLS 策略需要一个本表可判定的谓词，join sprints 在策略里代价太高。
    organization_id  uuid NOT NULL,
    snapshot_date    date NOT NULL,
    scope_points     integer NOT NULL,
    completed_points integer NOT NULL,
    remaining_points integer NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (sprint_id, snapshot_date)
);

CREATE INDEX sprint_snapshots_org_idx ON sprint_snapshots (organization_id, snapshot_date);

-- 看板移动是一种状态迁移，事件类型补 moved。
ALTER TABLE work_item_events DROP CONSTRAINT work_item_events_type_check;
ALTER TABLE work_item_events ADD CONSTRAINT work_item_events_type_check CHECK (event_type IN (
    'created', 'transitioned', 'assigned', 'updated', 'linked', 'unlinked', 'comment.added', 'moved'));

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'boards', 'sprints', 'sprint_scope_events', 'sprint_snapshots'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($policy$
            CREATE POLICY %I ON %I
                USING (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid)
                WITH CHECK (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid)
        $policy$, t || '_org_isolation', t);
    END LOOP;
END;
$$;

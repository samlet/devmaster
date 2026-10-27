-- 0002_workitem.sql
-- M1：工作项、状态迁移事件、评论、链接、编号计数器。
--
-- 四条纪律：
--   1. 工作项编号在组织内唯一且单调（product_counters 行锁）；
--   2. 状态迁移只能走迁移表，每次迁移写一条 work_item_events（append-only 触发器）；
--   3. 组织隔离：所有业务表开 RLS；
--   4. 搜索用 pg_trgm（中文按三元组切分），不引外部搜索引擎。

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE product_counters (
    product_id uuid PRIMARY KEY REFERENCES products (id) ON DELETE CASCADE,
    next_seq   bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE work_items (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL,
    key                 text NOT NULL,
    product_id          uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    type                text NOT NULL,
    status              text NOT NULL,
    resolution          text,
    title               text NOT NULL,
    description_md      text NOT NULL DEFAULT '',
    acceptance_criteria text,
    priority            smallint NOT NULL DEFAULT 3,
    severity            text,
    environment         text,
    -- 人员标识用 text：Keycloak sub 通常是 UUID，但协议上不保证；
    -- 存 text 避免把"本地 demo 主体"这类非 UUID 身份挡在门外。
    reporter_id         text NOT NULL,
    assignee_id         text,
    parent_id           uuid REFERENCES work_items (id) ON DELETE RESTRICT,
    rank                text NOT NULL,
    project_code        text,
    wbs_code            text,
    external_ref        text,
    custom_fields       jsonb NOT NULL DEFAULT '{}'::jsonb,
    blocked_reason      text,
    reopened_count      smallint NOT NULL DEFAULT 0,
    version             integer NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    resolved_at         timestamptz,
    closed_at           timestamptz,

    CONSTRAINT work_items_type_check CHECK (type IN ('EPIC', 'STORY', 'TASK', 'BUG', 'SUBTASK')),
    CONSTRAINT work_items_status_check CHECK (status IN (
        'BACKLOG', 'TODO', 'OPEN', 'IN_PROGRESS', 'IN_REVIEW', 'RESOLVED', 'VERIFIED',
        'DONE', 'CLOSED', 'REOPENED', 'WONTFIX', 'CANCELLED')),
    CONSTRAINT work_items_resolution_check CHECK (resolution IS NULL OR resolution IN (
        'FIXED', 'WONTFIX', 'DUPLICATE', 'CANNOT_REPRODUCE', 'BY_DESIGN', 'OBSOLETE')),
    CONSTRAINT work_items_priority_check CHECK (priority BETWEEN 0 AND 3),
    CONSTRAINT work_items_severity_check CHECK (severity IS NULL OR severity IN (
        'BLOCKER', 'CRITICAL', 'MAJOR', 'MINOR', 'TRIVIAL')),
    CONSTRAINT work_items_severity_bug_only CHECK (severity IS NULL OR type = 'BUG'),
    CONSTRAINT work_items_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT work_items_org_key_unique UNIQUE (organization_id, key),
    -- 导入器按 external_ref 幂等：同一组织内同一个外部引用只能对应一条工作项。
    CONSTRAINT work_items_org_external_unique UNIQUE (organization_id, external_ref)
);

CREATE INDEX work_items_product_rank_idx ON work_items (product_id, rank);
CREATE INDEX work_items_assignee_status_idx ON work_items (assignee_id, status)
    WHERE assignee_id IS NOT NULL;
CREATE INDEX work_items_status_idx ON work_items (organization_id, status, updated_at DESC);
-- 中文搜索：trigram 对 CJK 也有效（按字符三元组切分），比 tsvector 的 simple 配置实用。
CREATE INDEX work_items_title_trgm_idx ON work_items USING gin (title gin_trgm_ops);
CREATE INDEX work_items_description_trgm_idx ON work_items USING gin (description_md gin_trgm_ops);

CREATE TABLE work_item_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL,
    work_item_id    uuid NOT NULL REFERENCES work_items (id) ON DELETE RESTRICT,
    event_type      text NOT NULL,
    from_status     text,
    to_status       text,
    actor_id        text,
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT work_item_events_type_check CHECK (event_type IN (
        'created', 'transitioned', 'assigned', 'updated', 'linked', 'unlinked', 'comment.added'))
);

CREATE INDEX work_item_events_item_idx ON work_item_events (work_item_id, id);
CREATE INDEX work_item_events_org_time_idx ON work_item_events (organization_id, occurred_at DESC);

-- 事件是审计依据：不允许改写，也不允许删除。
CREATE OR REPLACE FUNCTION work_item_events_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'work_item_events is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER work_item_events_no_update
    BEFORE UPDATE OR DELETE ON work_item_events
    FOR EACH ROW EXECUTE FUNCTION work_item_events_append_only();

CREATE TABLE work_item_links (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    from_id         uuid NOT NULL REFERENCES work_items (id) ON DELETE RESTRICT,
    to_id           uuid NOT NULL REFERENCES work_items (id) ON DELETE RESTRICT,
    link_type       text NOT NULL,
    created_by      text,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT work_item_links_type_check CHECK (link_type IN (
        'BLOCKS', 'BLOCKED_BY', 'RELATES', 'DUPLICATES', 'CAUSES')),
    CONSTRAINT work_item_links_no_self CHECK (from_id <> to_id),
    CONSTRAINT work_item_links_unique UNIQUE (from_id, to_id, link_type)
);

CREATE INDEX work_item_links_to_idx ON work_item_links (to_id);

CREATE TABLE comments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    work_item_id    uuid NOT NULL REFERENCES work_items (id) ON DELETE RESTRICT,
    author_id       text NOT NULL,
    body_md         text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    edited_at       timestamptz,

    CONSTRAINT comments_body_not_blank CHECK (btrim(body_md) <> '')
);

CREATE INDEX comments_item_idx ON comments (work_item_id, created_at);

-- ---------------------------------------------------------------------------
-- RLS：所有业务表按组织隔离。work_item_events 也开，对账与读侧都走 InOrgTx。
-- ---------------------------------------------------------------------------

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'work_items', 'work_item_events', 'work_item_links', 'comments'
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

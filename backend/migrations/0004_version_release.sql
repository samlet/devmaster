-- 0004_version_release.sql
-- M3：版本、发布记录、BUG 字段补全。
--
-- 三条纪律：
--   1. 一个版本同时只能有一次进行中的发布尝试（部分唯一索引）；
--   2. 提交发布即冻结范围（versions.scope_frozen_at），之后不能改版本成员；
--   3. 发布记录不删除：失败也是一次事实，重试是新建记录。

CREATE TABLE versions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  uuid NOT NULL,
    product_id       uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    name             text NOT NULL,
    status           text NOT NULL DEFAULT 'PLANNED',
    milestone_label  text,
    planned_date     date,
    released_at      timestamptz,
    notes_ref        text,
    scope_frozen_at  timestamptz,
    version          integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT versions_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT versions_status_check CHECK (status IN ('PLANNED', 'IN_PROGRESS', 'RELEASED', 'ARCHIVED', 'CANCELLED')),
    CONSTRAINT versions_product_name_unique UNIQUE (product_id, name)
);

CREATE INDEX versions_product_status_idx ON versions (product_id, status, planned_date);

ALTER TABLE work_items
    ADD COLUMN platform text,
    ADD COLUMN build_number text,
    ADD COLUMN repro_steps text,
    ADD COLUMN expected_behavior text,
    ADD COLUMN actual_behavior text,
    ADD COLUMN version_id uuid REFERENCES versions (id) ON DELETE RESTRICT,
    ADD COLUMN affects_version_id uuid REFERENCES versions (id) ON DELETE RESTRICT;

ALTER TABLE work_items
    ADD CONSTRAINT work_items_platform_check CHECK (platform IS NULL OR platform IN ('WIN', 'MAC', 'IOS', 'ANDROID', 'SERVER')),
    ADD CONSTRAINT work_items_affects_bug_only CHECK (affects_version_id IS NULL OR type = 'BUG');

CREATE INDEX work_items_version_idx ON work_items (version_id) WHERE version_id IS NOT NULL;
CREATE INDEX work_items_affects_version_idx ON work_items (affects_version_id) WHERE affects_version_id IS NOT NULL;

CREATE TABLE release_records (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL,
    version_id           uuid NOT NULL REFERENCES versions (id) ON DELETE RESTRICT,
    status               text NOT NULL DEFAULT 'PENDING_APPROVAL',
    tag                  text,
    commit_from          text,
    commit_to            text,
    submitted_by         text NOT NULL,
    approval_instance_id text,
    approver_id          text,
    evidence_ref         text,
    failure_reason       text,
    released_by          text,
    released_at          timestamptz,
    version              integer NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT release_records_status_check CHECK (status IN (
        'PENDING_APPROVAL', 'APPROVED', 'TAGGED', 'PUBLISHED', 'FAILED')),
    CONSTRAINT release_records_submitted_by_not_blank CHECK (btrim(submitted_by) <> '')
);

-- 同一版本同时只有一个进行中的发布尝试。没有这条，两次点击会起两个审批。
CREATE UNIQUE INDEX release_records_inflight_idx ON release_records (version_id)
    WHERE status IN ('PENDING_APPROVAL', 'APPROVED', 'TAGGED');

CREATE INDEX release_records_version_idx ON release_records (version_id, created_at DESC);

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['versions', 'release_records'] LOOP
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

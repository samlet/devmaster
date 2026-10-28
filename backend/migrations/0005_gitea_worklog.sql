-- 0005_gitea_worklog.sql
-- M4：Gitea 提交关联、每日对账水位、工时（只增不改）。
--
-- 三条纪律：
--   1. 提交关联幂等：unique (work_item_id, repo, commit_sha)；
--   2. 工时只增不改：UPDATE/DELETE 由触发器拒绝，更正用负向记录；
--   3. 一条工时只能更正一次：reversal_of 唯一（部分索引）。

-- 供工时表做复合外键（组织 + 工作项）。
ALTER TABLE work_items ADD CONSTRAINT work_items_org_id_unique UNIQUE (organization_id, id);

CREATE TABLE work_item_commits (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    work_item_id    uuid NOT NULL,
    repo            text NOT NULL,
    branch          text,
    commit_sha      text NOT NULL,
    pr_no           integer,
    pr_state        text,
    committed_at    timestamptz,
    author_id       text,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT work_item_commits_pr_state_check CHECK (pr_state IS NULL OR pr_state IN ('OPEN', 'MERGED', 'CLOSED')),
    CONSTRAINT work_item_commits_unique UNIQUE (work_item_id, repo, commit_sha),
    CONSTRAINT work_item_commits_item_fk FOREIGN KEY (organization_id, work_item_id)
        REFERENCES work_items (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX work_item_commits_repo_sha_idx ON work_item_commits (repo, commit_sha);
CREATE INDEX work_item_commits_item_idx ON work_item_commits (work_item_id);

CREATE TABLE work_logs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    work_item_id    uuid NOT NULL,
    author_id       text NOT NULL,
    minutes         integer NOT NULL,
    work_date       date NOT NULL,
    note            text,
    project_code    text,
    wbs_code        text,
    grade_code      text,
    source          text NOT NULL DEFAULT 'MANUAL',
    reversal_of     uuid REFERENCES work_logs (id) ON DELETE RESTRICT,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT work_logs_minutes_check CHECK (minutes <> 0),
    CONSTRAINT work_logs_reversal_check CHECK (reversal_of IS NULL OR minutes < 0),
    CONSTRAINT work_logs_source_check CHECK (source IN ('MANUAL', 'IMPORT')),
    CONSTRAINT work_logs_item_fk FOREIGN KEY (organization_id, work_item_id)
        REFERENCES work_items (organization_id, id) ON DELETE RESTRICT
);

CREATE INDEX work_logs_item_idx ON work_logs (work_item_id, work_date);
CREATE INDEX work_logs_org_date_idx ON work_logs (organization_id, work_date DESC);
-- 一条工时只能更正一次；否则净额会被冲成负数。
CREATE UNIQUE INDEX work_logs_reversal_unique ON work_logs (reversal_of) WHERE reversal_of IS NOT NULL;

CREATE OR REPLACE FUNCTION work_logs_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'work_logs is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER work_logs_no_update
    BEFORE UPDATE OR DELETE ON work_logs
    FOR EACH ROW EXECUTE FUNCTION work_logs_append_only();

CREATE TABLE repo_sync_cursors (
    organization_id uuid NOT NULL,
    repo            text NOT NULL,
    last_commit_at  timestamptz,
    last_sha        text,
    updated_at      timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (organization_id, repo)
);

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['work_item_commits', 'work_logs', 'repo_sync_cursors'] LOOP
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

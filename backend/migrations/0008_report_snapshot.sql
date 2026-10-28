-- 0008_report_snapshot.sql
-- 报表日快照：CFD 与吞吐不再每次请求折叠事件，改为读每天一行。
CREATE TABLE report_daily_snapshots (
    organization_id  uuid NOT NULL,
    product_id       uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    snapshot_date    date NOT NULL,
    todo             integer NOT NULL DEFAULT 0,
    in_progress      integer NOT NULL DEFAULT 0,
    in_review        integer NOT NULL DEFAULT 0,
    done             integer NOT NULL DEFAULT 0,
    -- 当天首次完成数，按类型：{"TASK": 3, "BUG": 1}
    completed_by_type jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (organization_id, product_id, snapshot_date)
);

ALTER TABLE report_daily_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_daily_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY report_daily_snapshots_org_isolation ON report_daily_snapshots
    USING (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid)
    WITH CHECK (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid);

CREATE INDEX report_daily_snapshots_product_date_idx
    ON report_daily_snapshots (organization_id, product_id, snapshot_date);

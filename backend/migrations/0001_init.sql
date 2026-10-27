-- 0001_init.sql
-- M0 骨架：产品、事务性发件箱、入站幂等、写接口幂等。
--
-- 这里只有四张表，但四条纪律都在：
--   1. 组织隔离：products / idempotency_keys 开 RLS，谓词依赖 app.organization_id；
--   2. 事件只经 outbox：业务写入与事件写入同事务；
--   3. 幂等靠唯一约束，不靠"先查再写"；
--   4. 时间统一 timestamptz（UTC）。

CREATE TABLE products (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    -- 工作项编号的前缀（HOSP-1234）。创建后不可变：改了它等于改了历史编号。
    key             text NOT NULL,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'ACTIVE',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT products_key_shape CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$'),
    CONSTRAINT products_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT products_status_check CHECK (status IN ('ACTIVE', 'ARCHIVED')),
    CONSTRAINT products_org_key_unique UNIQUE (organization_id, key)
);

CREATE INDEX products_org_status_idx ON products (organization_id, status, key);

-- 事务性发件箱。
--
-- event_id 是确定性派生的 UUIDv5：同一个业务事实重发时不变，消费方可按它去重。
-- next_attempt_at 同时承担"租约"与"退避"两个职责：relay 取走时推后一个租约，
-- 投递失败时按指数退避推得更远。
CREATE TABLE outbox_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL,
    event_id        uuid NOT NULL,
    subject         text NOT NULL,
    payload         jsonb NOT NULL,
    headers         jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT outbox_events_event_id_unique UNIQUE (event_id),
    CONSTRAINT outbox_events_subject_not_blank CHECK (btrim(subject) <> '')
);

CREATE INDEX outbox_events_pending_idx
    ON outbox_events (next_attempt_at, id) WHERE sent_at IS NULL;

-- 入站幂等。M0 没有入站源，但表先建好：Gitea webhook 在 M4 接入时不再改迁移。
CREATE TABLE inbox_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL,
    source_system   text NOT NULL,
    event_id        text NOT NULL,
    event_type      text NOT NULL,
    event_version   integer NOT NULL DEFAULT 1,
    subject         text NOT NULL DEFAULT '',
    stream_sequence bigint NOT NULL DEFAULT 0,
    payload         jsonb NOT NULL,
    payload_sha256  bytea NOT NULL,
    status          text NOT NULL DEFAULT 'RECEIVED',
    attempts        integer NOT NULL DEFAULT 0,
    last_error      text,
    received_at     timestamptz NOT NULL DEFAULT now(),
    processed_at    timestamptz,

    CONSTRAINT inbox_events_source_event_unique UNIQUE (source_system, event_id),
    CONSTRAINT inbox_events_status_check CHECK (status IN ('RECEIVED', 'DEFERRED', 'PROCESSED', 'FAILED', 'DEAD'))
);

-- 写接口幂等：同 key 同请求返回缓存响应，异请求返回 ABORTED。
CREATE TABLE idempotency_keys (
    key             text PRIMARY KEY,
    organization_id uuid NOT NULL,
    scope           text NOT NULL,
    request_hash    bytea NOT NULL,
    response_blob   bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,

    CONSTRAINT idempotency_keys_sha_len CHECK (octet_length(request_hash) = 32)
);

CREATE INDEX idempotency_keys_expires_idx ON idempotency_keys (expires_at);

-- ---------------------------------------------------------------------------
-- RLS：products 与 idempotency_keys 是业务表，必须按组织隔离。
-- outbox/inbox 是运维表，由 relay/consumer 跨组织扫描，不开 RLS（M0 决策）。
-- ---------------------------------------------------------------------------

ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;
CREATE POLICY products_org_isolation ON products
    USING (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid)
    WITH CHECK (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid);

ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY idempotency_keys_org_isolation ON idempotency_keys
    USING (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid)
    WITH CHECK (organization_id = nullif(current_setting('app.organization_id', true), '')::uuid);

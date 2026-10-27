-- grant-runtime.sql
-- 以 owner 身份执行：把运行时角色 devmaster_app 的权限收到最小集合。
--
-- 不可变表（事件、入站）只给必要的 DML；outbox 需要 UPDATE（标记已发送），
-- inbox 需要 UPDATE（状态推进），所以这两张表保留 UPDATE，但都收回 DELETE：
-- 删掉一条投递/接收记录等于抹掉"曾经发生过"的证据。

GRANT USAGE ON SCHEMA public TO devmaster_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO devmaster_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO devmaster_app;

REVOKE DELETE ON outbox_events FROM devmaster_app;
REVOKE DELETE ON inbox_events FROM devmaster_app;

-- 将来由迁移新建的表默认继承同样的授权。
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO devmaster_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO devmaster_app;

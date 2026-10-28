-- 0009_partial_reversal.sql
-- 允许一条工时被分次冲正（例如 90 分钟先冲 30、再冲 60）。
-- 原来的 reversal_of 唯一索引会挡住第二次；改成普通索引，
-- 由仓储在事务里锁原记录、累计冲正额，保证不超过原工时。
DROP INDEX IF EXISTS work_logs_reversal_unique;

CREATE INDEX IF NOT EXISTS work_logs_reversal_idx
    ON work_logs (reversal_of) WHERE reversal_of IS NOT NULL;

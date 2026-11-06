-- Migration: 000016
-- Description: Track degraded QA answers.
-- 降级答案只是兜底文案，不能和正常回答一样被 request_id 固化成终态；
-- 记录该标记后，重试可以重新生成并用条件更新覆盖它。

DO $$ BEGIN RAISE NOTICE '[Migration 000016] Adding qa_records.degraded...'; END $$;

ALTER TABLE qa_records
    ADD COLUMN IF NOT EXISTS degraded BOOLEAN NOT NULL DEFAULT FALSE;

-- 部分索引只覆盖仍处于降级的行：线上排查「还有多少请求没拿到真正回答」
-- 时结果集很小，也不会给正常写入增加太多索引维护成本。
CREATE INDEX IF NOT EXISTS idx_qa_records_degraded
    ON qa_records(degraded) WHERE degraded = TRUE;

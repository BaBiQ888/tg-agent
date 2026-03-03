-- Migration: 从 FastGPT 迁移到自建 AI 服务
-- 日期: 2026-03-03
-- 说明: 移除 FastGPT 专属字段，新增自建服务所需字段

-- ============================================
-- kb_datasets 表变更
-- ============================================

-- 1. 新增字段
ALTER TABLE kb_datasets ADD COLUMN IF NOT EXISTS embedding_model VARCHAR(64) DEFAULT 'text-embedding-3-small';
ALTER TABLE kb_datasets ADD COLUMN IF NOT EXISTS chunk_count INTEGER DEFAULT 0;
ALTER TABLE kb_datasets ADD COLUMN IF NOT EXISTS status VARCHAR(16) DEFAULT 'active';

-- 2. 将 ai_dataset_id 的值拷贝到新逻辑（现在 id 直接作为 Milvus 的 dataset_id）
-- 注意：新创建的数据集会直接用 PG id 作为 dataset_id，无需 ai_dataset_id 映射
-- 对于旧数据，ai_dataset_id 不再使用，保留字段但标记为弃用

-- 3. 移除 FastGPT 专属字段（如果存在）
-- 先检查列是否存在再删除，避免重复执行报错
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'kb_datasets' AND column_name = 'vector_model') THEN
        ALTER TABLE kb_datasets DROP COLUMN vector_model;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'kb_datasets' AND column_name = 'agent_model') THEN
        ALTER TABLE kb_datasets DROP COLUMN agent_model;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'kb_datasets' AND column_name = 'ai_dataset_id') THEN
        ALTER TABLE kb_datasets DROP COLUMN ai_dataset_id;
    END IF;
END $$;

-- ============================================
-- kb_collections 表变更
-- ============================================

ALTER TABLE kb_collections ADD COLUMN IF NOT EXISTS chunk_count INTEGER DEFAULT 0;
ALTER TABLE kb_collections ADD COLUMN IF NOT EXISTS status VARCHAR(16) DEFAULT 'pending';
ALTER TABLE kb_collections ADD COLUMN IF NOT EXISTS error_message TEXT;

-- ============================================
-- 验证
-- ============================================
-- SELECT column_name, data_type, column_default
-- FROM information_schema.columns
-- WHERE table_name = 'kb_datasets'
-- ORDER BY ordinal_position;
--
-- SELECT column_name, data_type, column_default
-- FROM information_schema.columns
-- WHERE table_name = 'kb_collections'
-- ORDER BY ordinal_position;

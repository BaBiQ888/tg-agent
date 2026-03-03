-- 创建知识库数据集表
CREATE TABLE kb_datasets (
    id              UUID            NOT NULL DEFAULT gen_random_uuid(),
    name            VARCHAR(255)    NOT NULL,
    description     TEXT            NULL,
    embedding_model VARCHAR(64)     NOT NULL DEFAULT 'text-embedding-3-small',
    chunk_count     INTEGER         NOT NULL DEFAULT 0,
    status          VARCHAR(16)     NOT NULL DEFAULT 'active',
    created_at      TIMESTAMP       WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP       WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMP       WITH TIME ZONE,
    CONSTRAINT kb_datasets_pkey PRIMARY KEY (id)
);

-- 创建索引
CREATE INDEX idx_kb_datasets_deleted_at ON kb_datasets(deleted_at);
CREATE INDEX idx_kb_datasets_status ON kb_datasets(status);

-- 创建更新时间触发器
CREATE TRIGGER update_kb_datasets_updated_at
    BEFORE UPDATE ON kb_datasets
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

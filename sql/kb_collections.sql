-- 创建知识库集合表
CREATE TABLE kb_collections (
    id              UUID            NOT NULL DEFAULT gen_random_uuid(),
    dataset_id      UUID            NOT NULL,
    collection_id   VARCHAR(255)    NOT NULL,
    name            VARCHAR(255)    NOT NULL,
    type            VARCHAR(50)     NOT NULL,  -- text, link, file
    content         TEXT            NOT NULL,
    source_name     VARCHAR(255)    NULL,
    chunk_count     INTEGER         NOT NULL DEFAULT 0,
    status          VARCHAR(16)     NOT NULL DEFAULT 'pending',  -- pending, ready, error
    error_message   TEXT            NULL,
    created_at      TIMESTAMP       WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP       WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMP       WITH TIME ZONE,
    CONSTRAINT kb_collections_pkey PRIMARY KEY (id),
    CONSTRAINT kb_collections_dataset_id_fkey FOREIGN KEY (dataset_id) REFERENCES kb_datasets(id)
);

-- 创建索引
CREATE INDEX idx_kb_collections_dataset_id ON kb_collections(dataset_id);
CREATE INDEX idx_kb_collections_collection_id ON kb_collections(collection_id);
CREATE INDEX idx_kb_collections_type ON kb_collections(type);
CREATE INDEX idx_kb_collections_status ON kb_collections(status);
CREATE INDEX idx_kb_collections_deleted_at ON kb_collections(deleted_at);

-- 创建更新时间触发器
CREATE TRIGGER update_kb_collections_updated_at
    BEFORE UPDATE ON kb_collections
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

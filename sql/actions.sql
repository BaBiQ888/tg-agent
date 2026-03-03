-- 创建 actions 表
CREATE TABLE actions (
    id              UUID          NOT NULL DEFAULT gen_random_uuid(),
    path            TEXT          NOT NULL,
    created_at      TIMESTAMP     WITH TIME ZONE NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP     WITH TIME ZONE NULL DEFAULT CURRENT_TIMESTAMP,
    platform        VARCHAR(50)   NOT NULL DEFAULT 'FastGPT',
    api_key         VARCHAR(255)  NULL,
    input_param     JSONB         NOT NULL DEFAULT '{}',
    output_param    JSONB         NOT NULL DEFAULT '{}',
    action_tip      TEXT          NULL,
    return_result   BOOLEAN       NOT NULL DEFAULT true,
    need_confirm    BOOLEAN       NOT NULL DEFAULT false,
    CONSTRAINT action_types_pkey PRIMARY KEY (id)
) TABLESPACE pg_default;

-- 创建更新时间触发器
CREATE TRIGGER update_action_types_updated_at 
    BEFORE UPDATE ON actions 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();
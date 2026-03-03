-- 创建 agents 表
CREATE TABLE agents (
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  bot_id uuid NOT NULL,
  description text NULL,
  role_id uuid NOT NULL,
  created_at timestamp with time zone NOT NULL DEFAULT now(),
  updated_at timestamp without time zone NULL DEFAULT now(),
  deleted_at timestamp with time zone NULL,
  status smallint NULL DEFAULT 0,
  name character varying NULL,
  is_delete boolean NULL DEFAULT false,
  test jsonb NULL,
  knowledges jsonb NULL,
  language text NULL,
  owner_id bigint NOT NULL,
  CONSTRAINT agents_pkey PRIMARY KEY (id),
  CONSTRAINT agents_bot_id_fkey FOREIGN KEY (bot_id) REFERENCES bots(id),
  CONSTRAINT agents_character_id_fkey FOREIGN KEY (role_id) REFERENCES agent_roles(id)
) TABLESPACE pg_default;
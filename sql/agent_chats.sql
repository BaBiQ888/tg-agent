-- 创建 agent_chats 表
CREATE TABLE agent_chats (
  id              UUID          NOT NULL DEFAULT gen_random_uuid(),
  created_at      TIMESTAMP     WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at      TIMESTAMP     WITHOUT TIME ZONE NULL DEFAULT now(),
  deleted_at      TIMESTAMP     WITHOUT TIME ZONE NULL,
  chat_id         BIGINT        NOT NULL DEFAULT '0'::bigint,
  bot_id          UUID          NOT NULL,
  agent_id        UUID          NOT NULL,
  CONSTRAINT agent_chat_pkey PRIMARY KEY (id),
  CONSTRAINT agent_chat_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id),
  CONSTRAINT agent_chat_bot_id_fkey FOREIGN KEY (bot_id) REFERENCES bots(id)
) TABLESPACE pg_default;
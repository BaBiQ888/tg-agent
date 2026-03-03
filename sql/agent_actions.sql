CREATE TABLE agent_actions (
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  created_at timestamp without time zone NULL DEFAULT now(),
  updated_at timestamp without time zone NULL DEFAULT now(),
  trigger_action text NULL DEFAULT '@',
  trigger_chat_id bigint NULL DEFAULT 0,
  deleted_at timestamp without time zone NULL,
  trigger_agent_id uuid NULL,
  action_id uuid NULL,
  action_agents jsonb NULL,
  trigger_bot_id uuid NULL DEFAULT gen_random_uuid(),
  CONSTRAINT agent_actions_pkey PRIMARY KEY (id),
  CONSTRAINT agent_actions_cmd_action_id_fkey FOREIGN KEY (action_id) REFERENCES cmd_actions(id),
  CONSTRAINT agent_actions_trigger_agent_id_fkey FOREIGN KEY (trigger_agent_id) REFERENCES agents(id),
  CONSTRAINT agent_actions_trigger_bot_id_fkey FOREIGN KEY (trigger_bot_id) REFERENCES bots(id)
) TABLESPACE pg_default;
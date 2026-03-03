create table public.cmd_actions (
  id uuid not null default gen_random_uuid (),
  created_at timestamp with time zone not null default now(),
  updated_at timestamp without time zone null default now(),
  deleted_at timestamp without time zone null,
  cmd_id uuid null,
  action_id uuid null,
  constraint cmd_actions_pkey primary key (id),
  constraint cmd_actions_action_id_fkey foreign KEY (action_id) references actions (id),
  constraint cmd_actions_cmd_id_fkey foreign KEY (cmd_id) references bot_cmds (id)
) TABLESPACE pg_default;
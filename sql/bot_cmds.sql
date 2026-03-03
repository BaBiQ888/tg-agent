create table public.bot_cmds (
  id uuid not null default extensions.uuid_generate_v4 (),
  command character varying(50) not null,
  description text null,
  reply_tip text not null,
  created_at timestamp with time zone null default CURRENT_TIMESTAMP,
  updated_at timestamp with time zone null default CURRENT_TIMESTAMP,
  constraint commands_pkey primary key (id)
) TABLESPACE pg_default;

create index IF not exists idx_commands_command on public.bot_cmds using btree (command) TABLESPACE pg_default;

create trigger update_commands_updated_at BEFORE
update on bot_cmds for EACH row
execute FUNCTION update_updated_at_column ();
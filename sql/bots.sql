create table public.bots (
  id uuid not null default extensions.uuid_generate_v4 (),
  bot_name character varying(50) not null,
  token character varying(100) not null,
  created_at timestamp with time zone null default CURRENT_TIMESTAMP,
  updated_at timestamp with time zone null default CURRENT_TIMESTAMP,
  constraint bots_pkey primary key (id),
  constraint bots_bot_id_key unique (bot_name)
) TABLESPACE pg_default;

create index IF not exists idx_bots_bot_id on public.bots using btree (bot_name) TABLESPACE pg_default;

create trigger update_bots_updated_at BEFORE
update on bots for EACH row
execute FUNCTION update_updated_at_column ();
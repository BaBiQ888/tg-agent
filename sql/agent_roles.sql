create table public.agent_roles (
  name character varying null,
  description text null,
  personality text null,
  skills text null,
  constraints text null,
  created_at timestamp with time zone not null default now(),
  updated_at timestamp with time zone null default now(),
  output_format text null,
  native_language character varying null,
  target character varying null,
  id uuid not null default gen_random_uuid (),
  gender text null,
  deleted_at timestamp with time zone null,
  is_public smallint null default '1'::smallint,
  owner_id bigint null default '0'::bigint,
  constraint character_pkey primary key (id)
) TABLESPACE pg_default;
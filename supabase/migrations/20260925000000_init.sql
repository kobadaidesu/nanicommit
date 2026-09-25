begin;

create table public.repositories (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references auth.users(id) on delete cascade,
    name text not null,
    learning_base_sha text,
    created_at timestamptz not null default now(),
    unique (id, user_id),
    check (
        learning_base_sha is null
        or learning_base_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'
    )
);

create table public.commits (
    id uuid primary key default gen_random_uuid(),
    user_id uuid not null references auth.users(id) on delete cascade,
    repository_id uuid not null,
    commit_sha text not null
        check (commit_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    branch text,
    message text not null,
    files jsonb not null check (jsonb_typeof(files) = 'array'),
    diff text not null,
    status text not null default 'ready'
        check (status in ('ready', 'passed')),
    passed_at timestamptz,
    created_at timestamptz not null default now(),
    foreign key (repository_id, user_id)
        references public.repositories(id, user_id) on delete cascade,
    unique (user_id, repository_id, commit_sha),
    check (
        (status = 'ready' and passed_at is null)
        or (status = 'passed' and passed_at is not null)
    )
);

create table public.questions (
    id uuid primary key default gen_random_uuid(),
    commit_id uuid not null references public.commits(id) on delete cascade,
    position smallint not null check (position between 1 and 3),
    question text not null,
    choices jsonb not null
        check (jsonb_typeof(choices) = 'array' and jsonb_array_length(choices) = 4),
    correct_index smallint not null check (correct_index between 0 and 3),
    hint text not null,
    explanation text not null,
    unique (commit_id, position)
);

create table public.answers (
    question_id uuid primary key
        references public.questions(id) on delete cascade,
    selected_index smallint not null check (selected_index between 0 and 3),
    is_correct boolean not null,
    answered_at timestamptz not null default now()
);

alter table public.repositories enable row level security;
alter table public.commits enable row level security;
alter table public.questions enable row level security;
alter table public.answers enable row level security;

revoke all on table
    public.repositories, public.commits, public.questions, public.answers
from anon, authenticated;

commit;

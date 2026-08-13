-- Core schema.
--
-- Note there is no `kind` column anywhere, and there never will be: the
-- ordering of content is groups and projects, nothing else (PROMPT section 12).

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username      text NOT NULL,
    password_hash text NOT NULL,
    display_name  text NOT NULL DEFAULT '',
    totp_secret   text,
    totp_enabled  boolean NOT NULL DEFAULT false,
    is_owner      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_key ON users (lower(username));

CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fgp_hash     text NOT NULL,
    refresh_hash text NOT NULL,
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    stepup_at    timestamptz,
    revoked_at   timestamptz
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Throttling for logins, project passwords and git basic auth alike.
CREATE TABLE auth_attempts (
    id         bigserial PRIMARY KEY,
    scope      text NOT NULL,
    subject    text NOT NULL,
    ok         boolean NOT NULL,
    ip         text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_attempts_lookup_idx ON auth_attempts (scope, subject, created_at DESC);

CREATE TABLE audit_log (
    id         bigserial PRIMARY KEY,
    user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    action     text NOT NULL,
    subject    text NOT NULL DEFAULT '',
    detail     jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip         text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);

CREATE TABLE groups (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug            text NOT NULL,
    title           text NOT NULL,
    description     text NOT NULL DEFAULT '',
    visibility      text NOT NULL DEFAULT 'private'
                    CHECK (visibility IN ('private','public','password')),
    password_hash   text,
    read_only       boolean NOT NULL DEFAULT false,
    color           text NOT NULL DEFAULT 'mauve',
    icon            text NOT NULL DEFAULT 'folder',
    site_project_id uuid,
    pinned          boolean NOT NULL DEFAULT false,
    archived        boolean NOT NULL DEFAULT false,
    position        integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX groups_slug_key ON groups (slug);

CREATE TABLE projects (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id      uuid REFERENCES groups(id) ON DELETE SET NULL,
    slug          text NOT NULL,
    title         text NOT NULL,
    description   text NOT NULL DEFAULT '',
    -- capabilities is a plain set of names; the core never switches on them.
    capabilities  text[] NOT NULL DEFAULT '{}',
    -- preset drives icon and default tab. Nothing else. Ever.
    preset        text NOT NULL DEFAULT 'data',
    default_tab   text NOT NULL DEFAULT 'files',
    git_tracked   boolean NOT NULL DEFAULT false,
    site_root     text,
    visibility    text NOT NULL DEFAULT 'private'
                  CHECK (visibility IN ('private','public','password')),
    password_hash text,
    read_only     boolean NOT NULL DEFAULT false,
    anon_write    boolean NOT NULL DEFAULT false,
    color         text NOT NULL DEFAULT '',
    icon          text NOT NULL DEFAULT '',
    archived      boolean NOT NULL DEFAULT false,
    position      integer NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- A project slug is the branch name inside its group's repo, so it has to be
-- unique per group. Ungrouped projects (group_id IS NULL) share one namespace.
CREATE UNIQUE INDEX projects_group_slug_key ON projects (group_id, slug) NULLS NOT DISTINCT;
CREATE INDEX projects_group_idx ON projects (group_id);
CREATE INDEX projects_slug_idx ON projects (slug);

ALTER TABLE groups
    ADD CONSTRAINT groups_site_project_fk
    FOREIGN KEY (site_project_id) REFERENCES projects(id) ON DELETE SET NULL;

-- Links: content of one project shown inside another. No copy, no second
-- membership. Deleting a link never touches the source.
CREATE TABLE folder_links (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_project uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_path    text NOT NULL,
    target_project uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    target_path    text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX folder_links_target_key ON folder_links (target_project, target_path);

CREATE TABLE file_links (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_project uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_path    text NOT NULL,
    target_project uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    target_path    text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX file_links_target_key ON file_links (target_project, target_path);

-- Tokens for machines: ICS subscriptions, webhooks, git over HTTPS, the app.
CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   text NOT NULL,
    project_id   uuid REFERENCES projects(id) ON DELETE CASCADE,
    group_id     uuid REFERENCES groups(id) ON DELETE CASCADE,
    scope        text NOT NULL DEFAULT 'read'
                 CHECK (scope IN ('read','write','ics','git','webhook')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz,
    revoked_at   timestamptz
);
CREATE UNIQUE INDEX api_tokens_hash_key ON api_tokens (token_hash);

-- Accounts hold every credential in the system. Projects never do.
CREATE TABLE accounts (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind         text NOT NULL,
    title        text NOT NULL,
    config       jsonb NOT NULL DEFAULT '{}'::jsonb,
    secret_enc   bytea,
    -- has_secret stays true after the secret was consumed so the UI can say
    -- "enter password again" instead of "never had one".
    needs_secret boolean NOT NULL DEFAULT true,
    -- The single-use rule (PROMPT section 5) lives in these three columns.
    attempt_in_flight boolean NOT NULL DEFAULT false,
    attempt_started_at timestamptz,
    consumed_at  timestamptz,
    state        text NOT NULL DEFAULT 'new'
                 CHECK (state IN ('new','ok','needs_password','error')),
    last_ok_at   timestamptz,
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE schedulers (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id   uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    account_id   uuid REFERENCES accounts(id) ON DELETE SET NULL,
    title        text NOT NULL DEFAULT '',
    kind         text NOT NULL,
    schedule     text NOT NULL DEFAULT 'manual',
    target_path  text NOT NULL DEFAULT '',
    options      jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled      boolean NOT NULL DEFAULT true,
    paused_reason text NOT NULL DEFAULT '',
    last_run_at  timestamptz,
    last_status  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedulers_project_idx ON schedulers (project_id);
CREATE INDEX schedulers_account_idx ON schedulers (account_id);

CREATE TABLE scheduler_runs (
    id            bigserial PRIMARY KEY,
    scheduler_id  uuid NOT NULL REFERENCES schedulers(id) ON DELETE CASCADE,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    status        text NOT NULL DEFAULT 'running',
    message       text NOT NULL DEFAULT '',
    files_changed integer NOT NULL DEFAULT 0,
    trigger       text NOT NULL DEFAULT 'schedule',
    log           text NOT NULL DEFAULT ''
);
CREATE INDEX scheduler_runs_lookup_idx ON scheduler_runs (scheduler_id, started_at DESC);

-- Variables: the one route from a project to the dashboard.
CREATE TABLE variables (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        text NOT NULL,
    type        text NOT NULL DEFAULT 'text'
                CHECK (type IN ('number','text','bool','date','list','table')),
    value       jsonb NOT NULL DEFAULT 'null'::jsonb,
    unit        text NOT NULL DEFAULT '',
    source      text NOT NULL DEFAULT '',
    error       text NOT NULL DEFAULT '',
    ttl_seconds integer NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX variables_project_name_key ON variables (project_id, name);

CREATE TABLE variable_history (
    id         bigserial PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       text NOT NULL,
    value      jsonb NOT NULL,
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX variable_history_lookup_idx ON variable_history (project_id, name, at DESC);

-- A group can define variables of its own, computed over the ones its
-- projects export.
CREATE TABLE group_variables (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id   uuid NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    name       text NOT NULL,
    op         text NOT NULL CHECK (op IN ('sum','count','avg','min','max','any','all','expr')),
    inputs     jsonb NOT NULL DEFAULT '[]'::jsonb,
    expr       text NOT NULL DEFAULT '',
    unit       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX group_variables_name_key ON group_variables (group_id, name);

CREATE TABLE dashboard_tiles (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id   uuid NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    variable   text NOT NULL DEFAULT '',
    title      text NOT NULL DEFAULT '',
    kind       text NOT NULL DEFAULT 'number'
               CHECK (kind IN ('number','text','status','list','table','history','button')),
    options    jsonb NOT NULL DEFAULT '{}'::jsonb,
    x          integer NOT NULL DEFAULT 0,
    y          integer NOT NULL DEFAULT 0,
    w          integer NOT NULL DEFAULT 1,
    h          integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX dashboard_tiles_owner_idx ON dashboard_tiles (owner_id);

-- User-facing settings that are not tied to a single object.
CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

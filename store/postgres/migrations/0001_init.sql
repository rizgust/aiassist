-- aiassist core schema. All tables are prefixed ai_ so they can live in a
-- project's existing database without colliding.

CREATE TABLE ai_model (
    id                          TEXT PRIMARY KEY,           -- registry id, e.g. 'sonnet-5-5'
    provider                    TEXT NOT NULL,              -- 'anthropic'
    model_id                    TEXT NOT NULL,              -- provider's id, e.g. 'claude-sonnet-5-5'
    display_name                TEXT NOT NULL DEFAULT '',
    price_in_per_mtok           NUMERIC(12,6) NOT NULL DEFAULT 0,
    price_out_per_mtok          NUMERIC(12,6) NOT NULL DEFAULT 0,
    price_cache_read_per_mtok   NUMERIC(12,6) NOT NULL DEFAULT 0,
    price_cache_write_per_mtok  NUMERIC(12,6) NOT NULL DEFAULT 0,
    enabled                     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE ai_task_route (
    id          BIGSERIAL PRIMARY KEY,
    task        TEXT NOT NULL,
    model_id    TEXT NOT NULL REFERENCES ai_model(id),
    weight      INT NOT NULL DEFAULT 1 CHECK (weight >= 0),
    priority    INT NOT NULL DEFAULT 0,             -- lower = tried first; higher tiers are fallbacks
    max_tokens  BIGINT NOT NULL DEFAULT 4096,
    effort      TEXT NOT NULL DEFAULT '',           -- '' = model default; low|medium|high|xhigh|max
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ai_task_route_task ON ai_task_route (task) WHERE enabled;

-- Monthly USD limits. scope='project' → scope_key is the project name;
-- scope='task' → scope_key is the task name.
CREATE TABLE ai_budget (
    id          BIGSERIAL PRIMARY KEY,
    scope       TEXT NOT NULL CHECK (scope IN ('project', 'task')),
    scope_key   TEXT NOT NULL,
    limit_usd   NUMERIC(12,2) NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('warn', 'downgrade', 'block')),
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (scope, scope_key)
);

-- One row per LLM call; cost priced at call time.
CREATE TABLE ai_usage (
    id                  BIGSERIAL PRIMARY KEY,
    at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    project             TEXT NOT NULL,
    channel             TEXT NOT NULL DEFAULT '',
    caller_id           TEXT NOT NULL DEFAULT '',
    task                TEXT NOT NULL,
    model_id            TEXT NOT NULL,
    provider            TEXT NOT NULL,
    provider_model      TEXT NOT NULL,
    request_id          TEXT NOT NULL,
    tokens_in           BIGINT NOT NULL DEFAULT 0,
    tokens_out          BIGINT NOT NULL DEFAULT 0,
    tokens_cache_read   BIGINT NOT NULL DEFAULT 0,
    tokens_cache_write  BIGINT NOT NULL DEFAULT 0,
    cost_usd            NUMERIC(14,6) NOT NULL DEFAULT 0,
    latency_ms          BIGINT NOT NULL DEFAULT 0,
    status              TEXT NOT NULL CHECK (status IN ('ok', 'error')),
    error               TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_ai_usage_project_at ON ai_usage (project, at);
CREATE INDEX idx_ai_usage_task_at ON ai_usage (project, task, at);
CREATE INDEX idx_ai_usage_request ON ai_usage (request_id);

CREATE TABLE ai_conversation (
    id          BIGSERIAL PRIMARY KEY,
    caller_id   TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content     TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ai_conversation_caller ON ai_conversation (caller_id, id DESC);

CREATE TABLE ai_global_memory (
    id          BIGSERIAL PRIMARY KEY,
    content     TEXT NOT NULL,
    saved_by    TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE ai_user_memory (
    caller_id   TEXT PRIMARY KEY,
    content     TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Cached context signals (weather, events, holidays, calendar rules).
CREATE TABLE ai_signal (
    id          BIGSERIAL PRIMARY KEY,
    date        DATE NOT NULL,
    scope       TEXT NOT NULL,              -- 'area' or a project venue/department id
    kind        TEXT NOT NULL,
    code        TEXT NOT NULL,
    severity    NUMERIC(8,3) NOT NULL DEFAULT 0,
    title       TEXT NOT NULL DEFAULT '',
    payload     JSONB,
    source      TEXT NOT NULL,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (kind, source, scope, date, code, title)
);
CREATE INDEX idx_ai_signal_date ON ai_signal (date, kind);

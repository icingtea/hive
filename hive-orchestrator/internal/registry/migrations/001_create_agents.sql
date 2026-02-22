CREATE TABLE IF NOT EXISTS agents (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    repo_url    TEXT NOT NULL,
    branch      TEXT NOT NULL DEFAULT 'main',
    env_vars    TEXT NOT NULL DEFAULT '{}',
    webhook_id  INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agents_repo_url ON agents(repo_url);

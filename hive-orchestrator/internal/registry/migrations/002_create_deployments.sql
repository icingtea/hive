CREATE TABLE IF NOT EXISTS deployments (
    id            TEXT PRIMARY KEY,
    agent_id      TEXT NOT NULL REFERENCES agents(id),
    commit_sha    TEXT NOT NULL DEFAULT '',
    image_ref     TEXT NOT NULL DEFAULT '',
    pod_name      TEXT NOT NULL DEFAULT '',
    namespace     TEXT NOT NULL DEFAULT 'hive-agents',
    status        TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT NOT NULL DEFAULT '',
    started_at    DATETIME,
    finished_at   DATETIME,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_deployments_agent_id ON deployments(agent_id);
CREATE INDEX IF NOT EXISTS idx_deployments_status   ON deployments(status);

package registry

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"hive-mind/internal/domain"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(string(data)); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

// ── Agents ───────────────────────────────────────────────────────────────────

func (s *SQLiteStore) CreateAgent(ctx context.Context, a *domain.Agent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, name, repo_url, branch, env_vars, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.RepoURL, a.Branch, a.EnvVars, a.Status, a.CreatedAt, a.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetAgent(ctx context.Context, id string) (*domain.Agent, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, repo_url, branch, env_vars, status, created_at, updated_at FROM agents WHERE id = ?`, id)
	return scanAgent(row)
}

func (s *SQLiteStore) ListAgents(ctx context.Context) ([]*domain.Agent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, repo_url, branch, env_vars, status, created_at, updated_at FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateAgent(ctx context.Context, a *domain.Agent) error {
	a.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE agents SET name=?, repo_url=?, branch=?, env_vars=?, status=?, updated_at=? WHERE id=?`,
		a.Name, a.RepoURL, a.Branch, a.EnvVars, a.Status, a.UpdatedAt, a.ID,
	)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeleteAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAgent(row scanner) (*domain.Agent, error) {
	var a domain.Agent
	err := row.Scan(&a.ID, &a.Name, &a.RepoURL, &a.Branch, &a.EnvVars, &a.Status, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("agent not found: %w", sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("scan agent: %w", err)
	}
	return &a, nil
}

// ── Deployments ───────────────────────────────────────────────────────────────

func (s *SQLiteStore) CreateDeployment(ctx context.Context, d *domain.Deployment) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deployments (id, agent_id, commit_sha, image_ref, pod_name, namespace, status, error_message, started_at, finished_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.AgentID, d.CommitSHA, d.ImageRef, d.PodName, d.Namespace,
		d.Status, d.ErrorMessage, d.StartedAt, d.FinishedAt, d.CreatedAt, d.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetDeployment(ctx context.Context, id string) (*domain.Deployment, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, agent_id, commit_sha, image_ref, pod_name, namespace, status, error_message, started_at, finished_at, created_at, updated_at
		 FROM deployments WHERE id = ?`, id)
	return scanDeployment(row)
}

func (s *SQLiteStore) ListDeployments(ctx context.Context, agentID string) ([]*domain.Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, commit_sha, image_ref, pod_name, namespace, status, error_message, started_at, finished_at, created_at, updated_at
		 FROM deployments WHERE agent_id = ? ORDER BY created_at DESC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListAllDeployments(ctx context.Context) ([]*domain.Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_id, commit_sha, image_ref, pod_name, namespace, status, error_message, started_at, finished_at, created_at, updated_at
		 FROM deployments ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) UpdateDeployment(ctx context.Context, d *domain.Deployment) error {
	d.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET agent_id=?, commit_sha=?, image_ref=?, pod_name=?, namespace=?, status=?, error_message=?, started_at=?, finished_at=?, updated_at=? WHERE id=?`,
		d.AgentID, d.CommitSHA, d.ImageRef, d.PodName, d.Namespace,
		d.Status, d.ErrorMessage, d.StartedAt, d.FinishedAt, d.UpdatedAt, d.ID,
	)
	if err != nil {
		return fmt.Errorf("update deployment: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeleteDeployment(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deployments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete deployment: %w", err)
	}
	return nil
}

func scanDeployment(row scanner) (*domain.Deployment, error) {
	var d domain.Deployment
	err := row.Scan(&d.ID, &d.AgentID, &d.CommitSHA, &d.ImageRef, &d.PodName, &d.Namespace,
		&d.Status, &d.ErrorMessage, &d.StartedAt, &d.FinishedAt, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment not found: %w", sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("scan deployment: %w", err)
	}
	return &d, nil
}

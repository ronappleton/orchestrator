package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PGStore struct {
	db *sql.DB
}

func NewPGStore(dsn string) (*PGStore, error) {
	if dsn == "" {
		return nil, fmt.Errorf("dsn is empty")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &PGStore{db: db}
	if err := s.migrate(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *PGStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
create table if not exists orchestrator_workflows (
  id text primary key,
  payload jsonb not null,
  created_at timestamptz not null
);
create table if not exists orchestrator_runs (
  id text primary key,
  workflow_id text not null,
  status text not null,
  current_step int not null,
  payload jsonb not null,
  created_at timestamptz not null,
  updated_at timestamptz not null
);
create table if not exists orchestrator_run_logs (
  id bigserial primary key,
  run_id text not null,
  message text not null,
  created_at timestamptz not null
);
`)
	return err
}

func (s *PGStore) CreateWorkflow(w Workflow) Workflow {
	b, _ := json.Marshal(w)
	_, _ = s.db.Exec(`insert into orchestrator_workflows (id, payload, created_at) values ($1, $2, $3)
on conflict (id) do update set payload = excluded.payload`, w.ID, b, w.CreatedAt)
	return w
}

func (s *PGStore) ListWorkflows() []Workflow {
	rows, err := s.db.Query(`select payload from orchestrator_workflows order by created_at desc`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var w Workflow
		if err := json.Unmarshal(raw, &w); err != nil {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (s *PGStore) GetWorkflow(id string) (Workflow, error) {
	var raw []byte
	err := s.db.QueryRow(`select payload from orchestrator_workflows where id=$1`, id).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return Workflow{}, ErrNotFound
		}
		return Workflow{}, err
	}
	var w Workflow
	if err := json.Unmarshal(raw, &w); err != nil {
		return Workflow{}, err
	}
	return w, nil
}

func (s *PGStore) CreateRun(r Run) Run {
	b, _ := json.Marshal(r)
	_, _ = s.db.Exec(`insert into orchestrator_runs (id, workflow_id, status, current_step, payload, created_at, updated_at)
values ($1,$2,$3,$4,$5,$6,$7)
on conflict (id) do update set payload = excluded.payload, status = excluded.status, current_step = excluded.current_step, updated_at = excluded.updated_at`,
		r.ID, r.WorkflowID, r.Status, r.CurrentStep, b, r.CreatedAt, r.UpdatedAt)
	return r
}

func (s *PGStore) UpdateRun(r Run) {
	r.UpdatedAt = time.Now().UTC()
	b, _ := json.Marshal(r)
	_, _ = s.db.Exec(`update orchestrator_runs set status=$2, current_step=$3, payload=$4, updated_at=$5 where id=$1`,
		r.ID, r.Status, r.CurrentStep, b, r.UpdatedAt)
}

func (s *PGStore) GetRun(id string) (Run, error) {
	var raw []byte
	err := s.db.QueryRow(`select payload from orchestrator_runs where id=$1`, id).Scan(&raw)
	if err != nil {
		if err == sql.ErrNoRows {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	var r Run
	if err := json.Unmarshal(raw, &r); err != nil {
		return Run{}, err
	}
	return r, nil
}

func (s *PGStore) AppendLog(runID string, msg string) {
	_, _ = s.db.Exec(`insert into orchestrator_run_logs (run_id, message, created_at) values ($1,$2,$3)`,
		runID, msg, time.Now().UTC())
}

func (s *PGStore) ListLogs(runID string) []string {
	rows, err := s.db.Query(`select message from orchestrator_run_logs where run_id=$1 order by id asc`, runID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			continue
		}
		out = append(out, msg)
	}
	return out
}

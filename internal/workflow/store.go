package workflow

import (
	"errors"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	CreateWorkflow(w Workflow) Workflow
	ListWorkflows() []Workflow
	GetWorkflow(id string) (Workflow, error)
	CreateRun(r Run) Run
	UpdateRun(r Run)
	GetRun(id string) (Run, error)
	AppendLog(runID string, msg string)
	ListLogs(runID string) []string
	SaveVersion(w Workflow) WorkflowVersion
	ListVersions(workflowID string) []WorkflowVersion
	GetVersion(workflowID string, version int) (Workflow, error)
}

type MemoryStore struct {
	mu        sync.RWMutex
	workflows map[string]Workflow
	runs      map[string]Run
	logs      map[string][]string
	versions  map[string][]WorkflowVersion
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workflows: map[string]Workflow{},
		runs:      map[string]Run{},
		logs:      map[string][]string{},
		versions:  map[string][]WorkflowVersion{},
	}
}

func (s *MemoryStore) CreateWorkflow(w Workflow) Workflow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflows[w.ID] = w
	return w
}

func (s *MemoryStore) ListWorkflows() []Workflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Workflow, 0, len(s.workflows))
	for _, w := range s.workflows {
		out = append(out, w)
	}
	return out
}

func (s *MemoryStore) GetWorkflow(id string) (Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.workflows[id]
	if !ok {
		return Workflow{}, ErrNotFound
	}
	return w, nil
}

func (s *MemoryStore) CreateRun(r Run) Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[r.ID] = r
	return r
}

func (s *MemoryStore) UpdateRun(r Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.UpdatedAt = time.Now().UTC()
	s.runs[r.ID] = r
}

func (s *MemoryStore) GetRun(id string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return r, nil
}

func (s *MemoryStore) AppendLog(runID string, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs[runID] = append(s.logs[runID], msg)
}

func (s *MemoryStore) ListLogs(runID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.logs[runID]...)
}

func (s *MemoryStore) SaveVersion(w Workflow) WorkflowVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := WorkflowVersion{
		ID:         newID("wfver"),
		WorkflowID: w.ID,
		Version:    len(s.versions[w.ID]) + 1,
		Payload:    w,
		CreatedAt:  time.Now().UTC(),
	}
	s.versions[w.ID] = append(s.versions[w.ID], v)
	return v
}

func (s *MemoryStore) ListVersions(workflowID string) []WorkflowVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]WorkflowVersion(nil), s.versions[workflowID]...)
}

func (s *MemoryStore) GetVersion(workflowID string, version int) (Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.versions[workflowID] {
		if v.Version == version {
			return v.Payload, nil
		}
	}
	return Workflow{}, ErrNotFound
}

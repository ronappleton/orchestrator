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
}

type MemoryStore struct {
	mu        sync.RWMutex
	workflows map[string]Workflow
	runs      map[string]Run
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		workflows: map[string]Workflow{},
		runs:      map[string]Run{},
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

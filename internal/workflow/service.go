package workflow

import (
	"context"
	"time"
)

type Service struct {
	store  *Store
	engine *Engine
}

func NewService(store *Store, engine *Engine) *Service {
	return &Service{store: store, engine: engine}
}

func (s *Service) CreateWorkflow(w Workflow) Workflow {
	if w.ID == "" {
		w.ID = newID("wf")
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	for i := range w.Steps {
		if w.Steps[i].ID == "" {
			w.Steps[i].ID = newID("step")
		}
	}
	return s.store.CreateWorkflow(w)
}

func (s *Service) ListWorkflows() []Workflow {
	return s.store.ListWorkflows()
}

func (s *Service) GetWorkflow(id string) (Workflow, error) {
	return s.store.GetWorkflow(id)
}

func (s *Service) StartRun(ctx context.Context, workflowID string, context map[string]interface{}) (Run, error) {
	wf, err := s.store.GetWorkflow(workflowID)
	if err != nil {
		return Run{}, err
	}
	run := Run{
		ID:          newID("run"),
		WorkflowID:  wf.ID,
		Status:      StatusPending,
		CurrentStep: 0,
		Context:     context,
		Steps:       []StepRun{},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	run = s.store.CreateRun(run)
	go s.engine.Execute(ctx, run.ID)
	return run, nil
}

func (s *Service) GetRun(id string) (Run, error) {
	return s.store.GetRun(id)
}

func (s *Service) ApproveRun(ctx context.Context, id string) (Run, error) {
	run, err := s.store.GetRun(id)
	if err != nil {
		return Run{}, err
	}
	if run.Status != StatusWaitingApproval {
		return run, nil
	}
	run.Status = StatusRunning
	s.store.UpdateRun(run)
	go s.engine.Execute(ctx, run.ID)
	return run, nil
}

func (s *Service) CancelRun(id string) (Run, error) {
	run, err := s.store.GetRun(id)
	if err != nil {
		return Run{}, err
	}
	run.Status = StatusCanceled
	s.store.UpdateRun(run)
	return run, nil
}

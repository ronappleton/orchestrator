package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Engine struct {
	store  Store
	client *http.Client
	notify *Notifier
}

func NewEngine(store Store, notify *Notifier) *Engine {
	return &Engine{
		store:  store,
		client: &http.Client{Timeout: 30 * time.Second},
		notify: notify,
	}
}

func (e *Engine) Execute(ctx context.Context, runID string) {
	run, err := e.store.GetRun(runID)
	if err != nil {
		return
	}

	if run.Status == StatusCanceled || run.Status == StatusSucceeded || run.Status == StatusFailed {
		return
	}

	run.Status = StatusRunning
	e.store.UpdateRun(run)
	e.notify.RunEvent(run, "run.started", "")

	workflow, err := e.store.GetWorkflow(run.WorkflowID)
	if err != nil {
		run.Status = StatusFailed
		e.store.UpdateRun(run)
		return
	}

	if run.Context == nil {
		run.Context = map[string]interface{}{}
	}

	for i := run.CurrentStep; i < len(workflow.Steps); i++ {
		step := workflow.Steps[i]
		stepRun := StepRun{
			StepID: step.ID,
			Name:   step.Name,
			Action: step.Action,
			Status: StatusRunning,
		}

		if step.RequiresApproval {
			run.Status = StatusWaitingApproval
			run.CurrentStep = i
			run.Steps = append(run.Steps, StepRun{
				StepID: step.ID,
				Name:   step.Name,
				Action: step.Action,
				Status: StatusWaitingApproval,
			})
			e.store.UpdateRun(run)
			e.notify.RunEvent(run, "run.waiting_approval", step.Name)
			return
		}

		out, code, err := e.executeStep(ctx, step)
		if err != nil {
			stepRun.Status = StatusFailed
			stepRun.Error = err.Error()
			stepRun.HTTPCode = code
			run.Steps = append(run.Steps, stepRun)
			run.Status = StatusFailed
			run.CurrentStep = i
			e.store.UpdateRun(run)
			e.notify.StepEvent(run, stepRun, "step.failed")
			return
		}
		stepRun.Status = StatusSucceeded
		stepRun.Output = out
		stepRun.HTTPCode = code
		run.Steps = append(run.Steps, stepRun)
		run.CurrentStep = i + 1
		e.store.UpdateRun(run)
		e.notify.StepEvent(run, stepRun, "step.succeeded")
	}

	run.Status = StatusSucceeded
	e.store.UpdateRun(run)
	e.notify.RunEvent(run, "run.succeeded", "")
}

func (e *Engine) executeStep(ctx context.Context, step Step) (string, int, error) {
	switch strings.ToLower(step.Action) {
	case "http", "ai_router.chat", "web.search", "web.extract", "memarch.store_fact", "memarch.search", "scm.call":
		return e.executeHTTP(ctx, step)
	default:
		return "", 0, fmt.Errorf("unsupported action: %s", step.Action)
	}
}

type httpInput struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

func (e *Engine) executeHTTP(ctx context.Context, step Step) (string, int, error) {
	var in httpInput
	raw, _ := json.Marshal(step.Input)
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", 0, fmt.Errorf("invalid http input")
	}
	if strings.TrimSpace(in.Method) == "" {
		in.Method = "POST"
	}
	if strings.TrimSpace(in.URL) == "" {
		return "", 0, fmt.Errorf("missing url")
	}

	var body io.Reader
	if len(in.Body) > 0 {
		body = bytes.NewReader(in.Body)
	}

	req, err := http.NewRequestWithContext(ctx, in.Method, in.URL, body)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request")
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return string(b), resp.StatusCode, fmt.Errorf("http status %d", resp.StatusCode)
	}
	return string(b), resp.StatusCode, nil
}

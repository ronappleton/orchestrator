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
	store                  Store
	client                 *http.Client
	notify                 *Notifier
	policyRequiresApproval bool
}

func NewEngine(store Store, notify *Notifier) *Engine {
	return &Engine{
		store:  store,
		client: &http.Client{Timeout: 30 * time.Second},
		notify: notify,
	}
}

func (e *Engine) SetPolicyRequiresApproval(v bool) {
	e.policyRequiresApproval = v
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

		if (step.RequiresApproval || e.policyRequiresApproval) && !e.isApproved(run, step) {
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

		out, code, stop, err := e.executeStepWithRetry(ctx, run, step)
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
		if stop {
			run.Status = StatusSucceeded
			e.store.UpdateRun(run)
			e.notify.RunEvent(run, "run.succeeded", "stopped by condition")
			return
		}
	}

	run.Status = StatusSucceeded
	e.store.UpdateRun(run)
	e.notify.RunEvent(run, "run.succeeded", "")
}

func (e *Engine) executeStepWithRetry(ctx context.Context, run Run, step Step) (string, int, bool, error) {
	attempts := 0
	max := step.Retry.Max
	if max < 0 {
		max = 0
	}
	for {
		out, code, stop, err := e.executeStep(ctx, run, step)
		if err == nil {
			return out, code, stop, nil
		}
		if attempts >= max {
			return out, code, stop, err
		}
		backoff := time.Duration(step.Retry.BackoffMs) * time.Millisecond
		if backoff <= 0 {
			backoff = 250 * time.Millisecond
		}
		time.Sleep(backoff)
		attempts++
	}
}

func (e *Engine) executeStep(ctx context.Context, run Run, step Step) (string, int, bool, error) {
	switch strings.ToLower(step.Action) {
	case "http", "ai_router.chat", "web.search", "web.extract", "memarch.store_fact", "memarch.search", "scm.call":
		out, code, err := e.executeHTTP(ctx, step)
		return out, code, false, err
	case "transform":
		return e.executeTransform(run, step)
	case "condition":
		return e.executeCondition(run, step)
	default:
		return "", 0, false, fmt.Errorf("unsupported action: %s", step.Action)
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
	if err := validateHTTPInput(in); err != nil {
		return "", 0, err
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

func validateHTTPInput(in httpInput) error {
	if !strings.HasPrefix(strings.ToLower(in.URL), "http") {
		return fmt.Errorf("url must be http or https")
	}
	return nil
}

func (e *Engine) executeTransform(run Run, step Step) (string, int, bool, error) {
	input := map[string]any{}
	raw, _ := json.Marshal(step.Input)
	_ = json.Unmarshal(raw, &input)
	setMap, _ := input["set"].(map[string]any)
	if run.Context == nil {
		run.Context = map[string]any{}
	}
	for k, v := range setMap {
		run.Context[k] = v
	}
	return "ok", http.StatusOK, false, nil
}

func (e *Engine) executeCondition(run Run, step Step) (string, int, bool, error) {
	input := map[string]any{}
	raw, _ := json.Marshal(step.Input)
	_ = json.Unmarshal(raw, &input)
	key, _ := input["key"].(string)
	onFalse, _ := input["on_false"].(string)
	equals := input["equals"]
	notEquals := input["not_equals"]

	val, ok := run.Context[key]
	pass := false
	if ok {
		if equals != nil {
			pass = val == equals
		} else if notEquals != nil {
			pass = val != notEquals
		}
	}
	if pass {
		return "condition true", http.StatusOK, false, nil
	}
	switch strings.ToLower(onFalse) {
	case "stop":
		return "condition false; stopping", http.StatusOK, true, nil
	case "fail":
		return "condition false; failing", http.StatusBadRequest, false, fmt.Errorf("condition failed")
	default:
		return "condition false; continue", http.StatusOK, false, nil
	}
}

func (e *Engine) isApproved(run Run, step Step) bool {
	if run.Context == nil {
		return false
	}
	if v, ok := run.Context["approved_step_id"]; ok {
		if s, ok := v.(string); ok && s == step.ID {
			// Clear approval so it is one-time.
			run.Context["approved_step_id"] = ""
			return true
		}
	}
	return false
}

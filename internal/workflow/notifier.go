package workflow

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

type Notifier struct {
	memarch  *endpoint
	auditLog *endpoint
	eventBus *endpoint
	client   *http.Client
}

type endpoint struct {
	baseURL string
	timeout time.Duration
}

func NewNotifier(memarchURL, memarchTimeout, auditURL, auditTimeout, eventURL, eventTimeout string) *Notifier {
	return &Notifier{
		memarch:  parseEndpoint(memarchURL, memarchTimeout),
		auditLog: parseEndpoint(auditURL, auditTimeout),
		eventBus: parseEndpoint(eventURL, eventTimeout),
		client:   &http.Client{Timeout: 5 * time.Second},
	}
}

func (n *Notifier) RunEvent(run Run, event, note string) {
	if n == nil {
		return
	}
	payload := map[string]any{
		"event":        event,
		"run_id":       run.ID,
		"workflow_id":  run.WorkflowID,
		"status":       run.Status,
		"current_step": run.CurrentStep,
		"note":         note,
		"ts":           time.Now().UTC().Format(time.RFC3339),
	}
	n.postMemarch(run, payload)
	n.postAudit(payload)
	n.postEventBus(payload)
}

func (n *Notifier) StepEvent(run Run, step StepRun, event string) {
	if n == nil {
		return
	}
	payload := map[string]any{
		"event":       event,
		"run_id":      run.ID,
		"workflow_id": run.WorkflowID,
		"step_id":     step.StepID,
		"step_name":   step.Name,
		"step_action": step.Action,
		"step_status": step.Status,
		"http_code":   step.HTTPCode,
		"error":       step.Error,
		"ts":          time.Now().UTC().Format(time.RFC3339),
	}
	n.postMemarch(run, payload)
	n.postAudit(payload)
	n.postEventBus(payload)
}

func (n *Notifier) postMemarch(run Run, payload map[string]any) {
	if n.memarch == nil || n.memarch.baseURL == "" {
		return
	}
	body := map[string]any{
		"project_id": run.WorkflowID,
		"type":       "orchestrator",
		"payload":    payload,
	}
	n.postJSON(n.memarch.baseURL+"/v1/events", body)
}

func (n *Notifier) postAudit(payload map[string]any) {
	if n.auditLog == nil || n.auditLog.baseURL == "" {
		return
	}
	n.postJSON(n.auditLog.baseURL+"/v1/events", payload)
}

func (n *Notifier) postEventBus(payload map[string]any) {
	if n.eventBus == nil || n.eventBus.baseURL == "" {
		return
	}
	body := map[string]any{
		"topic":   payload["event"],
		"payload": payload,
	}
	n.postJSON(n.eventBus.baseURL+"/v1/events", body)
}

func (n *Notifier) postJSON(url string, payload map[string]any) {
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	_, _ = n.client.Do(req)
}

func parseEndpoint(url, timeout string) *endpoint {
	if url == "" {
		return nil
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil {
		dur = 5 * time.Second
	}
	return &endpoint{baseURL: url, timeout: dur}
}

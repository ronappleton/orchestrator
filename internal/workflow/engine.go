package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	notifypb "github.com/ronappleton/notification-service/pkg/pb"
	policypb "github.com/ronappleton/policy-service/pkg/pb"
	workspacepb "github.com/ronappleton/workspace-service/pkg/pb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Engine struct {
	store                  Store
	client                 *http.Client
	notify                 *Notifier
	notificationBaseURL    string
	workspaceBaseURL       string
	policyBaseURL          string
	notificationGRPCAddr   string
	workspaceGRPCAddr      string
	policyGRPCAddr         string
	notificationTimeout    time.Duration
	workspaceTimeout       time.Duration
	policyTimeout          time.Duration
	policyRequiresApproval bool

	notificationConn   *grpc.ClientConn
	notificationClient notifypb.NotificationServiceClient
	workspaceConn      *grpc.ClientConn
	workspaceClient    workspacepb.WorkspaceServiceClient
	policyConn         *grpc.ClientConn
	policyClient       policypb.PolicyServiceClient
}

func NewEngine(store Store, notify *Notifier, notificationBaseURL, notificationGRPC, notificationTimeout, workspaceBaseURL, workspaceGRPC, workspaceTimeout, policyBaseURL, policyGRPC, policyTimeout string) *Engine {
	return &Engine{
		store:                store,
		client:               &http.Client{Timeout: 30 * time.Second},
		notify:               notify,
		notificationBaseURL:  strings.TrimRight(notificationBaseURL, "/"),
		workspaceBaseURL:     strings.TrimRight(workspaceBaseURL, "/"),
		policyBaseURL:        strings.TrimRight(policyBaseURL, "/"),
		notificationGRPCAddr: notificationGRPC,
		workspaceGRPCAddr:    workspaceGRPC,
		policyGRPCAddr:       policyGRPC,
		notificationTimeout:  parseTimeout(notificationTimeout),
		workspaceTimeout:     parseTimeout(workspaceTimeout),
		policyTimeout:        parseTimeout(policyTimeout),
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
	e.store.AppendLog(run.ID, "run started")
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
			e.store.AppendLog(run.ID, "waiting approval for step: "+step.Name)
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
			e.store.AppendLog(run.ID, "step failed: "+step.Name+" err="+err.Error())
			e.notify.StepEvent(run, stepRun, "step.failed")
			return
		}
		stepRun.Status = StatusSucceeded
		stepRun.Output = out
		stepRun.HTTPCode = code
		run.Steps = append(run.Steps, stepRun)
		run.CurrentStep = i + 1
		e.store.UpdateRun(run)
		e.store.AppendLog(run.ID, "step succeeded: "+step.Name)
		e.notify.StepEvent(run, stepRun, "step.succeeded")
		if stop {
			run.Status = StatusSucceeded
			e.store.UpdateRun(run)
			e.store.AppendLog(run.ID, "run stopped by condition")
			e.notify.RunEvent(run, "run.succeeded", "stopped by condition")
			return
		}
	}

	run.Status = StatusSucceeded
	e.store.UpdateRun(run)
	e.store.AppendLog(run.ID, "run succeeded")
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
	case "notify":
		out, code, err := e.executeNotify(ctx, run, step)
		return out, code, false, err
	case "workspace.check":
		out, code, err := e.executeWorkspaceCheck(ctx, run, step)
		return out, code, false, err
	case "policy.check":
		out, code, err := e.executePolicyCheck(ctx, step)
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

type notifyInput struct {
	Channel  string            `json:"channel"`
	To       string            `json:"to"`
	Subject  string            `json:"subject"`
	Body     string            `json:"body"`
	Metadata map[string]string `json:"metadata"`
}

func (e *Engine) executeNotify(ctx context.Context, run Run, step Step) (string, int, error) {
	var in notifyInput
	raw, _ := json.Marshal(step.Input)
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", 0, fmt.Errorf("invalid notify input")
	}
	if strings.TrimSpace(in.Channel) == "" || strings.TrimSpace(in.To) == "" {
		return "", 0, fmt.Errorf("notify requires channel and to")
	}
	if err := e.ensurePolicyAllows(ctx, "notify", in.Channel, in.To, run, step); err != nil {
		return "", 0, err
	}
	if e.notificationGRPCAddr != "" {
		resp, err := e.sendNotifyGRPC(ctx, in)
		if err == nil {
			return resp, http.StatusOK, nil
		}
		if e.notificationBaseURL == "" {
			return "", 0, err
		}
	}
	if e.notificationBaseURL == "" {
		return "", 0, fmt.Errorf("notification base_url not configured")
	}
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.notificationBaseURL+"/v1/notify", bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
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

func (e *Engine) executeWorkspaceCheck(ctx context.Context, run Run, step Step) (string, int, error) {
	input := map[string]any{}
	raw, _ := json.Marshal(step.Input)
	_ = json.Unmarshal(raw, &input)
	id, _ := input["workspace_id"].(string)
	if id == "" {
		id, _ = input["id"].(string)
	}
	if strings.TrimSpace(id) == "" {
		return "", 0, fmt.Errorf("workspace_id required")
	}
	if err := e.ensurePolicyAllows(ctx, "workspace.check", "", "", run, step); err != nil {
		return "", 0, err
	}
	if e.workspaceGRPCAddr != "" {
		resp, err := e.fetchWorkspaceGRPC(ctx, id)
		if err == nil {
			return resp, http.StatusOK, nil
		}
		if e.workspaceBaseURL == "" {
			return "", 0, err
		}
	}
	if e.workspaceBaseURL == "" {
		return "", 0, fmt.Errorf("workspace base_url not configured")
	}
	url := e.workspaceBaseURL + "/v1/workspaces/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request")
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

func (e *Engine) ensurePolicyAllows(ctx context.Context, action, channel, to string, run Run, step Step) error {
	payload := map[string]any{
		"action": action,
	}
	if channel != "" {
		payload["channel"] = channel
	}
	if to != "" {
		payload["to"] = to
	}
	if run.Context != nil {
		if ws, ok := run.Context["workspace_id"].(string); ok && ws != "" {
			payload["workspace_id"] = ws
		}
	}
	if step.Input != nil {
		raw, _ := json.Marshal(step.Input)
		var input map[string]any
		_ = json.Unmarshal(raw, &input)
		if ws, ok := input["workspace_id"].(string); ok && ws != "" {
			payload["workspace_id"] = ws
		}
	}
	if e.policyGRPCAddr != "" {
		allowed, reason, err := e.checkPolicyGRPC(ctx, payload)
		if err != nil {
			return err
		}
		if !allowed {
			if reason == "" {
				reason = "denied by policy"
			}
			return errors.New(reason)
		}
		return nil
	}
	if e.policyBaseURL == "" {
		return nil
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.policyBaseURL+"/v1/check", bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("policy request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("policy status %d", resp.StatusCode)
	}
	var res struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	if !res.Allowed {
		if res.Reason == "" {
			res.Reason = "denied by policy"
		}
		return errors.New(res.Reason)
	}
	return nil
}

func parseTimeout(raw string) time.Duration {
	if raw == "" {
		return 5 * time.Second
	}
	dur, err := time.ParseDuration(raw)
	if err != nil {
		return 5 * time.Second
	}
	return dur
}

func (e *Engine) ensureNotificationClient(ctx context.Context) error {
	if e.notificationClient != nil {
		return nil
	}
	conn, err := grpc.DialContext(
		ctx,
		e.notificationGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		return err
	}
	e.notificationConn = conn
	e.notificationClient = notifypb.NewNotificationServiceClient(conn)
	return nil
}

func (e *Engine) ensureWorkspaceClient(ctx context.Context) error {
	if e.workspaceClient != nil {
		return nil
	}
	conn, err := grpc.DialContext(
		ctx,
		e.workspaceGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		return err
	}
	e.workspaceConn = conn
	e.workspaceClient = workspacepb.NewWorkspaceServiceClient(conn)
	return nil
}

func (e *Engine) ensurePolicyClient(ctx context.Context) error {
	if e.policyClient != nil {
		return nil
	}
	conn, err := grpc.DialContext(
		ctx,
		e.policyGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		return err
	}
	e.policyConn = conn
	e.policyClient = policypb.NewPolicyServiceClient(conn)
	return nil
}

func (e *Engine) sendNotifyGRPC(ctx context.Context, in notifyInput) (string, error) {
	if e.notificationGRPCAddr == "" {
		return "", nil
	}
	if err := e.ensureNotificationClient(ctx); err != nil {
		return "", err
	}
	rpcCtx, cancel := context.WithTimeout(ctx, e.notificationTimeout)
	defer cancel()
	resp, err := e.notificationClient.Notify(rpcCtx, &notifypb.NotifyRequest{
		Channel:  in.Channel,
		To:       in.To,
		Subject:  in.Subject,
		Body:     in.Body,
		Metadata: in.Metadata,
	})
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(map[string]any{
		"id":     resp.GetId(),
		"status": resp.GetStatus(),
	})
	return string(raw), nil
}

func (e *Engine) fetchWorkspaceGRPC(ctx context.Context, id string) (string, error) {
	if e.workspaceGRPCAddr == "" {
		return "", nil
	}
	if err := e.ensureWorkspaceClient(ctx); err != nil {
		return "", err
	}
	rpcCtx, cancel := context.WithTimeout(ctx, e.workspaceTimeout)
	defer cancel()
	resp, err := e.workspaceClient.GetWorkspace(rpcCtx, &workspacepb.GetWorkspaceRequest{Id: id})
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(resp.GetWorkspace())
	return string(raw), nil
}

func (e *Engine) checkPolicyGRPC(ctx context.Context, payload map[string]any) (bool, string, error) {
	if e.policyGRPCAddr == "" {
		return true, "", nil
	}
	if err := e.ensurePolicyClient(ctx); err != nil {
		return false, "", err
	}
	action, _ := payload["action"].(string)
	if strings.TrimSpace(action) == "" {
		return false, "", fmt.Errorf("policy.check requires action")
	}
	req := &policypb.CheckRequest{
		Subject: &policypb.Subject{
			Service:   firstNonEmpty(stringPayload(payload, "service"), "orchestrator"),
			ActorType: policypb.ActorType_SYSTEM,
			ActorId:   stringPayload(payload, "actor_id"),
		},
		Action: action,
	}
	if res := buildResource(payload); res != nil {
		req.Resource = res
	}
	if ctx := buildContext(payload); ctx != nil {
		req.Context = ctx
	}
	rpcCtx, cancel := context.WithTimeout(ctx, e.policyTimeout)
	defer cancel()
	resp, err := e.policyClient.Check(rpcCtx, req)
	if err != nil {
		return false, "", err
	}
	return resp.GetDecision() == policypb.Decision_ALLOW, resp.GetReason(), nil
}

func buildResource(payload map[string]any) *policypb.Resource {
	resource := &policypb.Resource{}
	if v := stringPayload(payload, "resource_url"); v != "" {
		resource.Url = v
	}
	if v := stringPayload(payload, "channel"); v != "" {
		resource.Tool = v
	}
	if v := stringPayload(payload, "workflow_id"); v != "" {
		resource.WorkflowId = v
	}
	if paths := stringSlice(payload["paths"]); len(paths) > 0 {
		resource.Paths = paths
	}
	if resource.Url == "" && resource.Tool == "" && resource.WorkflowId == "" && len(resource.Paths) == 0 {
		return nil
	}
	return resource
}

func buildContext(payload map[string]any) *policypb.Context {
	ctx := &policypb.Context{}
	if v := stringPayload(payload, "workspace_id"); v != "" {
		ctx.WorkspaceId = v
	}
	if v := stringPayload(payload, "project_id"); v != "" {
		ctx.ProjectId = v
	}
	if v := stringPayload(payload, "correlation_id"); v != "" {
		ctx.CorrelationId = v
	}
	if ctx.WorkspaceId == "" && ctx.ProjectId == "" && ctx.CorrelationId == "" {
		return nil
	}
	return ctx
}

func stringPayload(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if v, ok := payload[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func stringSlice(value any) []string {
	out := []string{}
	switch typed := value.(type) {
	case []string:
		out = append(out, typed...)
	case []any:
		for _, item := range typed {
			if s := fmt.Sprintf("%v", item); s != "" {
				out = append(out, s)
			}
		}
	case string:
		if typed != "" {
			out = append(out, typed)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (e *Engine) executePolicyCheck(ctx context.Context, step Step) (string, int, error) {
	input := map[string]any{}
	raw, _ := json.Marshal(step.Input)
	_ = json.Unmarshal(raw, &input)

	bodyRaw, _ := input["body"]
	if body, ok := bodyRaw.(map[string]any); ok {
		input = body
	}

	if e.policyGRPCAddr != "" {
		allowed, reason, err := e.checkPolicyGRPC(ctx, input)
		if err != nil {
			return "", 0, err
		}
		resp := map[string]any{
			"allowed": allowed,
			"reason":  reason,
		}
		encoded, _ := json.Marshal(resp)
		return string(encoded), http.StatusOK, nil
	}

	url, _ := input["url"].(string)
	method, _ := input["method"].(string)
	if method == "" {
		method = http.MethodPost
	}
	if strings.TrimSpace(url) == "" {
		return "", 0, fmt.Errorf("policy.check requires url or policy grpc address")
	}
	payload := map[string]any{
		"method": method,
		"url":    url,
		"body":   bodyRaw,
	}
	rawPayload, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawPayload))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
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

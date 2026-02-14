package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	auditpb "github.com/ronappleton/audit-log/pkg/pb"
	"github.com/ronappleton/event-bus/pkg/natsbus"
	memarchpb "github.com/ronappleton/memarch/pkg/pb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"
)

type Notifier struct {
	memarch  *endpoint
	auditLog *endpoint
	eventBus *endpoint
	client   *http.Client

	memarchConn   *grpc.ClientConn
	memarchClient memarchpb.MemArchServiceClient
	auditConn     *grpc.ClientConn
	auditClient   auditpb.AuditLogClient
	eventNC       *natsbus.Conn
	eventJS       natsbus.JetStream
}

type endpoint struct {
	baseURL     string
	grpcAddress string
	timeout     time.Duration
}

func NewNotifier(memarchURL, memarchGRPC, memarchTimeout, auditURL, auditGRPC, auditTimeout, eventURL, eventGRPC, eventTimeout string) *Notifier {
	return &Notifier{
		memarch:  parseEndpoint(memarchURL, memarchGRPC, memarchTimeout),
		auditLog: parseEndpoint(auditURL, auditGRPC, auditTimeout),
		eventBus: parseEndpoint(eventURL, eventGRPC, eventTimeout),
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
	if n.memarch == nil {
		return
	}
	if n.memarch.grpcAddress != "" {
		if err := n.postMemarchGRPC(run, payload); err == nil {
			return
		}
		if n.memarch.baseURL == "" {
			return
		}
	}
	if n.memarch.baseURL == "" {
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
	if n.auditLog == nil {
		return
	}
	if n.auditLog.grpcAddress != "" {
		if err := n.postAuditGRPC(payload); err == nil {
			return
		}
		if n.auditLog.baseURL == "" {
			return
		}
	}
	if n.auditLog.baseURL == "" {
		return
	}
	n.postJSON(n.auditLog.baseURL+"/v1/events", payload)
}

func (n *Notifier) postEventBus(payload map[string]any) {
	if n.eventBus == nil {
		return
	}
	_ = n.postEventBusNATS(payload)
}

func (n *Notifier) postJSON(url string, payload map[string]any) {
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	_, _ = n.client.Do(req)
}

func parseEndpoint(url, grpcAddr, timeout string) *endpoint {
	if url == "" && grpcAddr == "" {
		return nil
	}
	dur, err := time.ParseDuration(timeout)
	if err != nil {
		dur = 5 * time.Second
	}
	return &endpoint{baseURL: url, grpcAddress: grpcAddr, timeout: dur}
}

func (n *Notifier) postMemarchGRPC(run Run, payload map[string]any) error {
	if n.memarch == nil || n.memarch.grpcAddress == "" {
		return nil
	}
	if err := n.ensureMemarchClient(context.Background()); err != nil {
		return err
	}
	raw, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), n.memarch.timeout)
	defer cancel()
	_, err := n.memarchClient.PostEvent(ctx, &memarchpb.PostEventRequest{
		ProjectId: run.WorkflowID,
		Source:    "orchestrator",
		Payload:   string(raw),
	})
	return err
}

func (n *Notifier) postAuditGRPC(payload map[string]any) error {
	if n.auditLog == nil || n.auditLog.grpcAddress == "" {
		return nil
	}
	if err := n.ensureAuditClient(context.Background()); err != nil {
		return err
	}
	var eventType string
	if v, ok := payload["event"].(string); ok {
		eventType = v
	}
	st, _ := structpb.NewStruct(payload)
	ctx, cancel := context.WithTimeout(context.Background(), n.auditLog.timeout)
	defer cancel()
	_, err := n.auditClient.Append(ctx, &auditpb.AppendRequest{
		Type:    eventType,
		Source:  "orchestrator",
		Payload: st,
	})
	return err
}

func (n *Notifier) postEventBusNATS(payload map[string]any) error {
	if n.eventBus == nil {
		return nil
	}
	if err := n.ensureEventClient(context.Background()); err != nil {
		return err
	}
	eventName, _ := payload["event"].(string)
	subject := "events.orchestrator.event"
	if strings.TrimSpace(eventName) != "" {
		subject = "events.orchestrator." + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(eventName, ".", "_"), " ", "_"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), n.eventBus.timeout)
	defer cancel()
	_, err := natsbus.PublishEvent(ctx, n.eventJS, subject, natsbus.EventEnvelope{
		Type:    "OrchestratorEvent.v1",
		Payload: payload,
	})
	return err
}

func (n *Notifier) ensureMemarchClient(ctx context.Context) error {
	if n.memarchClient != nil {
		return nil
	}
	if n.memarch == nil || n.memarch.grpcAddress == "" {
		return fmt.Errorf("memarch grpc address not configured")
	}
	dialCtx, cancel := context.WithTimeout(ctx, n.memarch.timeout)
	defer cancel()
	conn, err := grpc.DialContext(
		dialCtx,
		n.memarch.grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		return err
	}
	n.memarchConn = conn
	n.memarchClient = memarchpb.NewMemArchServiceClient(conn)
	return nil
}

func (n *Notifier) ensureAuditClient(ctx context.Context) error {
	if n.auditClient != nil {
		return nil
	}
	if n.auditLog == nil || n.auditLog.grpcAddress == "" {
		return fmt.Errorf("audit grpc address not configured")
	}
	dialCtx, cancel := context.WithTimeout(ctx, n.auditLog.timeout)
	defer cancel()
	conn, err := grpc.DialContext(
		dialCtx,
		n.auditLog.grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		return err
	}
	n.auditConn = conn
	n.auditClient = auditpb.NewAuditLogClient(conn)
	return nil
}

func (n *Notifier) ensureEventClient(ctx context.Context) error {
	if n.eventJS != nil {
		return nil
	}
	if n.eventBus == nil {
		return fmt.Errorf("event bus endpoint not configured")
	}
	natsURL := strings.TrimSpace(n.eventBus.grpcAddress)
	if natsURL == "" {
		natsURL = strings.TrimSpace(os.Getenv("NATS_URL"))
	}
	if natsURL == "" {
		natsURL = natsbus.DefaultNATSURL
	}
	dialCtx, cancel := context.WithTimeout(ctx, n.eventBus.timeout)
	defer cancel()
	nc, js, err := natsbus.Connect(dialCtx, natsbus.Config{
		URL:  natsURL,
		Name: "orchestrator-notifier",
	})
	if err != nil {
		return err
	}
	if err := natsbus.EnsureStreams(dialCtx, js, natsbus.StreamSpec{
		Name:     natsbus.DefaultEventStream,
		Subjects: []string{natsbus.DefaultEventSubject},
	}); err != nil {
		nc.Close()
		return err
	}
	n.eventNC = nc
	n.eventJS = js
	return nil
}

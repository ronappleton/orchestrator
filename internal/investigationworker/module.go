package investigationworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	eventpb "github.com/ronappleton/event-bus/pkg/pb"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	investigationTopic       = "observability.investigation"
	investigationRequestedV1 = "InvestigationRequested.v1"
)

func Module() fx.Option {
	return fx.Invoke(register)
}

func register(lc fx.Lifecycle, logger *zap.Logger) {
	address := envOr("EVENT_BUS_GRPC_ADDRESS", "event-bus:9111")
	managementURL := strings.TrimRight(envOr("MANAGEMENT_SERVICE_URL", "http://management-service:8125"), "/")
	if address == "" || managementURL == "" {
		logger.Info("investigation worker disabled: missing configuration")
		return
	}
	worker := &worker{
		logger:        logger,
		eventBusAddr:  address,
		managementURL: managementURL,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}
	var cancel context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			go worker.run(runCtx)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			return nil
		},
	})
}

type worker struct {
	logger        *zap.Logger
	eventBusAddr  string
	managementURL string
	httpClient    *http.Client
}

func (w *worker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := w.consume(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			w.logger.Warn("investigation worker stream failed; retrying", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

func (w *worker) consume(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, w.eventBusAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := eventpb.NewEventBusClient(conn)
	stream, err := client.Stream(ctx, &eventpb.StreamRequest{Topic: investigationTopic})
	if err != nil {
		return err
	}
	w.logger.Info("investigation worker connected", zap.String("topic", investigationTopic))

	for {
		evt, err := stream.Recv()
		if err != nil {
			return err
		}
		envelope, err := decodeEnvelope(evt)
		if err != nil {
			w.logger.Warn("investigation worker dropped invalid event", zap.Error(err))
			continue
		}
		if envelope.Type != investigationRequestedV1 {
			continue
		}
		if err := w.handleInvestigationRequested(ctx, envelope.Payload); err != nil {
			w.logger.Warn("investigation worker failed to post note", zap.Error(err))
		}
	}
}

func (w *worker) handleInvestigationRequested(ctx context.Context, payload map[string]any) error {
	investigationID, _ := payload["investigation_id"].(string)
	if strings.TrimSpace(investigationID) == "" {
		return fmt.Errorf("missing investigation_id")
	}
	w.logger.Info("investigation requested received", zap.String("investigation_id", investigationID))

	body, _ := json.Marshal(map[string]string{"note": "AI worker queued. Playbook: none (MVP)."})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/investigations/%s/notes", w.managementURL, investigationID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", "orchestrator-ai-worker")

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("management-service returned status %d", resp.StatusCode)
	}
	return nil
}

type envelope struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

func decodeEnvelope(evt *eventpb.Event) (envelope, error) {
	if evt == nil || evt.Payload == nil {
		return envelope{}, fmt.Errorf("empty payload")
	}
	fields := evt.Payload.AsMap()
	value := envelope{}
	if kind, ok := fields["type"].(string); ok {
		value.Type = kind
	}
	if payload, ok := fields["payload"].(map[string]any); ok {
		value.Payload = payload
	} else {
		value.Payload = fields
	}
	if value.Type == "" {
		if hint, ok := fields["event_type"].(string); ok {
			value.Type = hint
		}
	}
	if value.Type == "" {
		return envelope{}, fmt.Errorf("missing event type")
	}
	return value, nil
}

func envOr(key string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

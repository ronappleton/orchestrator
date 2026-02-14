package investigationworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ronappleton/event-bus/pkg/natsbus"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	investigationSubject     = "events.observability.investigation"
	investigationRequestedV1 = "InvestigationRequested.v1"
)

func Module() fx.Option {
	return fx.Invoke(register)
}

func register(lc fx.Lifecycle, logger *zap.Logger) {
	natsURL := envOr("NATS_URL", natsbus.DefaultNATSURL)
	managementURL := strings.TrimRight(envOr("MANAGEMENT_SERVICE_URL", "http://management-service:8125"), "/")
	if natsURL == "" || managementURL == "" {
		logger.Info("investigation worker disabled: missing configuration")
		return
	}
	worker := &worker{
		logger:        logger,
		natsURL:       natsURL,
		managementURL: managementURL,
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}
	var cancel context.CancelFunc
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			go worker.run(runCtx)
			return nil
		},
		OnStop: func(context.Context) error {
			if cancel != nil {
				cancel()
			}
			return nil
		},
	})
}

type worker struct {
	logger        *zap.Logger
	natsURL       string
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
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	nc, js, err := natsbus.Connect(connectCtx, natsbus.Config{
		URL:  w.natsURL,
		Name: "orchestrator-investigation-worker",
	})
	if err != nil {
		return err
	}
	defer nc.Close()

	if err := natsbus.EnsureStreams(connectCtx, js, natsbus.StreamSpec{
		Name:     natsbus.DefaultEventStream,
		Subjects: []string{natsbus.DefaultEventSubject},
	}); err != nil {
		return err
	}

	stop, err := natsbus.SubscribeDurable(ctx, js, natsbus.DurableConsumerConfig{
		Stream:      natsbus.DefaultEventStream,
		Subject:     investigationSubject,
		DurableName: "orchestrator-investigation-worker",
		FetchBatch:  8,
		FetchWait:   500 * time.Millisecond,
		Retry: natsbus.RetryPolicy{
			AckWait:    30 * time.Second,
			MaxDeliver: 8,
			Backoff: []time.Duration{
				500 * time.Millisecond,
				2 * time.Second,
				5 * time.Second,
				15 * time.Second,
			},
		},
	}, func(handlerCtx context.Context, evt natsbus.ReceivedEvent) natsbus.HandlerResult {
		if evt.Envelope.Type != investigationRequestedV1 {
			return natsbus.Ack()
		}
		if err := w.handleInvestigationRequested(handlerCtx, evt.Envelope.Payload); err != nil {
			w.logger.Warn("investigation worker failed to post note", zap.Error(err))
			return natsbus.Nak(2 * time.Second)
		}
		return natsbus.Ack()
	})
	if err != nil {
		return err
	}
	defer stop()

	w.logger.Info("investigation worker connected",
		zap.String("subject", investigationSubject),
		zap.String("nats_url", w.natsURL),
	)
	<-ctx.Done()
	return nil
}

func (w *worker) handleInvestigationRequested(ctx context.Context, payload map[string]any) error {
	investigationID, _ := payload["investigation_id"].(string)
	if strings.TrimSpace(investigationID) == "" {
		return fmt.Errorf("missing investigation_id")
	}
	w.logger.Info("investigation requested received", zap.String("investigation_id", investigationID))

	body := []byte(`{"note":"AI worker queued. Playbook: none (MVP)."}`)
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

func envOr(key string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

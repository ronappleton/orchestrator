package discoveryannounce

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ronappleton/event-bus/pkg/natsbus"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	heartbeatInterval = 10 * time.Second
	publishTimeout    = 5 * time.Second
	defaultFabric     = "local"
	discoverySubject  = "events.discovery.service"
	discoveryType     = "ServiceDiscovery.v1"
)

type Params struct {
	fx.In

	Lifecycle fx.Lifecycle
	Logger    *zap.Logger
}

// Register attaches service announcements to the app lifecycle.
func Register(params Params, serviceName, host string, port int) {
	lc := params.Lifecycle
	logger := params.Logger
	if serviceName == "" {
		logger.Warn("discovery announcer disabled: missing service name")
		return
	}
	if port <= 0 || port > 65535 {
		logger.Warn("discovery announcer disabled: invalid port", zap.Int("port", port))
		return
	}
	if host == "" {
		name, err := os.Hostname()
		if err == nil && name != "" {
			host = name
		} else {
			host = "unknown"
		}
	}

	natsURL := strings.TrimSpace(os.Getenv("NATS_URL"))
	if natsURL == "" {
		natsURL = natsbus.DefaultNATSURL
	}

	instanceID := fmt.Sprintf("%s-%s-%d-%d", serviceName, host, os.Getpid(), rand.New(rand.NewSource(time.Now().UnixNano())).Int63())
	publisher := &publisher{
		logger:      logger,
		serviceName: serviceName,
		host:        host,
		port:        port,
		instanceID:  instanceID,
		natsURL:     natsURL,
		fabric:      defaultFabric,
	}

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return publisher.Start(ctx)
		},
		OnStop: func(ctx context.Context) error {
			return publisher.Stop(ctx)
		},
	})
}

type publisher struct {
	logger      *zap.Logger
	serviceName string
	host        string
	port        int
	instanceID  string
	natsURL     string
	fabric      string

	mu      sync.Mutex
	nc      *natsbus.Conn
	js      natsbus.JetStream
	ticker  *time.Ticker
	stopCh  chan struct{}
	running bool
}

func (p *publisher) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	if err := p.connect(ctx); err != nil {
		p.logger.Warn("discovery announcer initial connect failed; will retry", zap.Error(err))
	} else if err := p.publish(ctx, "up"); err != nil {
		p.logger.Warn("discovery up event publish failed", zap.Error(err))
	}

	p.mu.Lock()
	p.stopCh = make(chan struct{})
	p.ticker = time.NewTicker(heartbeatInterval)
	p.running = true
	p.mu.Unlock()
	go p.runHeartbeat()

	p.logger.Info("discovery announcer started",
		zap.String("service", p.serviceName),
		zap.String("instance_id", p.instanceID),
		zap.String("nats_url", p.natsURL),
	)
	return nil
}

func (p *publisher) Stop(ctx context.Context) error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil
	}
	stopCh := p.stopCh
	p.stopCh = nil
	if p.ticker != nil {
		p.ticker.Stop()
		p.ticker = nil
	}
	p.running = false
	p.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}

	_ = p.publish(ctx, "down")
	_ = p.closeConn()
	return nil
}

func (p *publisher) runHeartbeat() {
	for {
		p.mu.Lock()
		ticker := p.ticker
		stopCh := p.stopCh
		p.mu.Unlock()

		if ticker == nil || stopCh == nil {
			return
		}

		select {
		case <-stopCh:
			return
		case <-ticker.C:
			_ = p.publish(context.Background(), "heartbeat")
		}
	}
}

func (p *publisher) connect(ctx context.Context) error {
	connectCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	nc, js, err := natsbus.Connect(connectCtx, natsbus.Config{
		URL:  p.natsURL,
		Name: p.serviceName + "-discovery-announcer",
	})
	if err != nil {
		return err
	}
	if err := natsbus.EnsureStreams(connectCtx, js, natsbus.StreamSpec{
		Name:     natsbus.DefaultEventStream,
		Subjects: []string{natsbus.DefaultEventSubject},
	}); err != nil {
		nc.Close()
		return err
	}

	p.mu.Lock()
	p.nc = nc
	p.js = js
	p.mu.Unlock()
	return nil
}

func (p *publisher) publish(ctx context.Context, eventType string) error {
	p.mu.Lock()
	js := p.js
	p.mu.Unlock()
	if js == nil {
		if err := p.connect(ctx); err != nil {
			return fmt.Errorf("nats jetstream unavailable: %w", err)
		}
		p.mu.Lock()
		js = p.js
		p.mu.Unlock()
	}

	payload := map[string]any{
		"event_type":          eventType,
		"service_name":        p.serviceName,
		"instance_id":         p.instanceID,
		"host":                p.host,
		"port":                p.port,
		"fabric":              p.fabric,
		"observed_at_unix_ms": time.Now().UnixMilli(),
	}

	callCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	_, err := natsbus.PublishEvent(callCtx, js, discoverySubject, natsbus.EventEnvelope{
		Type:    discoveryType,
		Payload: payload,
	})
	if err != nil {
		p.logger.Warn("discovery event publish failed", zap.String("event_type", eventType), zap.Error(err))
	}
	return err
}

func (p *publisher) closeConn() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nc != nil {
		p.nc.Close()
		p.nc = nil
		p.js = nil
	}
	return nil
}

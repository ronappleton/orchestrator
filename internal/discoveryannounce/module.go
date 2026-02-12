package discoveryannounce

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"time"

	eventpb "github.com/ronappleton/event-bus/pkg/pb"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	heartbeatInterval      = 10 * time.Second
	publishTimeout         = 5 * time.Second
	defaultEventBusAddress = "event-bus:9111"
	defaultFabric          = "local"
	discoveryTopic         = "service.discovery"
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

	busAddress := os.Getenv("EVENT_BUS_GRPC_ADDRESS")
	if busAddress == "" {
		busAddress = defaultEventBusAddress
	}

	instanceID := fmt.Sprintf("%s-%s-%d-%d", serviceName, host, os.Getpid(), rand.New(rand.NewSource(time.Now().UnixNano())).Int63())
	publisher := &publisher{
		logger:      logger,
		serviceName: serviceName,
		host:        host,
		port:        port,
		instanceID:  instanceID,
		busAddress:  busAddress,
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
	busAddress  string
	fabric      string

	mu      sync.Mutex
	conn    *grpc.ClientConn
	client  eventpb.EventBusClient
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
		return err
	}
	if err := p.publish(ctx, "up"); err != nil {
		_ = p.closeConn()
		return err
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
		zap.String("event_bus", p.busAddress),
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
	dialCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	conn, err := grpc.DialContext(
		dialCtx,
		p.busAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.conn = conn
	p.client = eventpb.NewEventBusClient(conn)
	p.mu.Unlock()
	return nil
}

func (p *publisher) publish(ctx context.Context, eventType string) error {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()
	if client == nil {
		return fmt.Errorf("event-bus client unavailable")
	}

	payload, err := structpb.NewStruct(map[string]any{
		"event_type":          eventType,
		"service_name":        p.serviceName,
		"instance_id":         p.instanceID,
		"host":                p.host,
		"port":                p.port,
		"fabric":              p.fabric,
		"observed_at_unix_ms": time.Now().UnixMilli(),
	})
	if err != nil {
		return err
	}

	callCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	_, err = client.Publish(callCtx, &eventpb.PublishRequest{Topic: discoveryTopic, Payload: payload})
	if err != nil {
		p.logger.Warn("discovery event publish failed", zap.String("event_type", eventType), zap.Error(err))
	}
	return err
}

func (p *publisher) closeConn() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		err := p.conn.Close()
		p.conn = nil
		p.client = nil
		return err
	}
	p.client = nil
	return nil
}

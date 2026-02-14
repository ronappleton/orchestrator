package discoveryannounce

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ronappleton/ai-eco-system/pkg/natsbus"
	"github.com/ronappleton/ai-eco-system/pkg/servicediscovery"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

const (
	publishTimeout = 5 * time.Second
)

type Params struct {
	fx.In

	Lifecycle fx.Lifecycle
	Logger    *zap.Logger
}

// Register attaches KV-based service announcements to the app lifecycle.
func Register(params Params, serviceName, host string, port int) {
	if strings.TrimSpace(serviceName) == "" {
		params.Logger.Warn("service discovery announcer disabled: missing service name")
		return
	}
	if port <= 0 || port > 65535 {
		params.Logger.Warn("service discovery announcer disabled: invalid port", zap.Int("port", port))
		return
	}

	fabric := strings.TrimSpace(os.Getenv("SERVICE_FABRIC"))
	if fabric == "" {
		fabric = servicediscovery.DefaultFabric
	}

	explicitHost := strings.TrimSpace(os.Getenv("ADVERTISE_HOST"))
	if explicitHost == "" {
		host = strings.TrimSpace(host)
		if host != "" && host != "0.0.0.0" && host != "::" {
			explicitHost = host
		}
	}
	advertiseHost := servicediscovery.DefaultAdvertiseHost(explicitHost, serviceName)

	advertisePort := port
	if envPort := strings.TrimSpace(os.Getenv("ADVERTISE_PORT")); envPort != "" {
		if parsed, err := strconv.Atoi(envPort); err == nil && parsed > 0 && parsed <= 65535 {
			advertisePort = parsed
		}
	}

	natsURL := strings.TrimSpace(os.Getenv("NATS_URL"))
	if natsURL == "" {
		natsURL = natsbus.DefaultNATSURL
	}

	metadata := map[string]string{}
	if v := strings.TrimSpace(os.Getenv("SERVICE_VERSION")); v != "" {
		metadata["version"] = v
	}
	if v := strings.TrimSpace(os.Getenv("SERVICE_COMMIT")); v != "" {
		metadata["commit"] = v
	}

	var (
		client    *servicediscovery.Client
		announcer *servicediscovery.Announcer
	)

	params.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			callCtx, cancel := context.WithTimeout(ctx, publishTimeout)
			defer cancel()

			var err error
			client, err = servicediscovery.New(callCtx, servicediscovery.NewConfig{
				NATS: natsbus.Config{
					URL:  natsURL,
					Name: serviceName + "-service-discovery",
				},
				Bucket: servicediscovery.BucketConfig{
					Name:   servicediscovery.DefaultBucket,
					MaxAge: servicediscovery.DefaultBucketMaxAge,
				},
			})
			if err != nil {
				params.Logger.Warn("service discovery init failed", zap.Error(err))
				return nil
			}

			announcer, err = client.NewAnnouncer(servicediscovery.AnnouncerConfig{
				Service:           serviceName,
				Fabric:            fabric,
				Host:              advertiseHost,
				Port:              advertisePort,
				Scheme:            servicediscovery.DefaultScheme,
				Metadata:          metadata,
				HeartbeatInterval: servicediscovery.DefaultHeartbeatInterval,
			})
			if err != nil {
				params.Logger.Warn("service discovery announcer create failed", zap.Error(err))
				return nil
			}
			if err := announcer.Start(callCtx); err != nil {
				params.Logger.Warn("service discovery announcer start failed", zap.Error(err))
				return nil
			}
			params.Logger.Info("service discovery announcer started",
				zap.String("service", serviceName),
				zap.String("fabric", fabric),
				zap.String("host", advertiseHost),
				zap.Int("port", advertisePort),
				zap.String("bucket", servicediscovery.DefaultBucket),
			)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			callCtx, cancel := context.WithTimeout(ctx, publishTimeout)
			defer cancel()
			if announcer != nil {
				_ = announcer.Stop(callCtx)
			}
			if client != nil {
				client.Close()
			}
			return nil
		},
	})
}

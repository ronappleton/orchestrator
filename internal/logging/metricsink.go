package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type metricPayload struct {
	Source   string            `json:"source"`
	Level    string            `json:"level"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type metricSender struct {
	baseURL string
	apiKey  string
	source  string
	client  *http.Client
	ch      chan metricPayload
}

func newMetricSender(baseURL string, apiKey string, source string) *metricSender {
	return &metricSender{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		source:  source,
		client:  &http.Client{Timeout: 3 * time.Second},
		ch:      make(chan metricPayload, 200),
	}
}

func (s *metricSender) start() {
	go func() {
		for payload := range s.ch {
			body, _ := json.Marshal(payload)
			req, err := http.NewRequest(http.MethodPost, s.baseURL+"/v1/logs", bytes.NewReader(body))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			if s.apiKey != "" {
				req.Header.Set("Authorization", "Bearer "+s.apiKey)
			}
			_, _ = s.client.Do(req)
		}
	}()
}

func attachMetricSink(logger *zap.Logger) *zap.Logger {
	baseURL := os.Getenv("METRIC_SERVICE_BASE_URL")
	if baseURL == "" {
		return logger
	}
	apiKey := os.Getenv("METRIC_SERVICE_API_KEY")
	source := os.Getenv("METRIC_SERVICE_SOURCE")
	if source == "" {
		source = filepathBase(os.Args[0])
	}
	sender := newMetricSender(baseURL, apiKey, source)
	sender.start()
	sink := &metricCore{
		level:  zapcore.InfoLevel,
		sender: sender,
	}
	return logger.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(core, sink)
	}))
}

type metricCore struct {
	level  zapcore.LevelEnabler
	fields []zapcore.Field
	sender *metricSender
}

func (c *metricCore) Enabled(level zapcore.Level) bool {
	return c.level.Enabled(level)
}

func (c *metricCore) With(fields []zapcore.Field) zapcore.Core {
	clone := *c
	clone.fields = append(clone.fields, fields...)
	return &clone
}

func (c *metricCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}

func (c *metricCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	enc := zapcore.NewMapObjectEncoder()
	for _, f := range c.fields {
		f.AddTo(enc)
	}
	for _, f := range fields {
		f.AddTo(enc)
	}
	metadata := map[string]string{}
	for k, v := range enc.Fields {
		metadata[k] = fmt.Sprint(v)
	}
	payload := metricPayload{
		Source:   c.sender.source,
		Level:    entry.Level.String(),
		Message:  entry.Message,
		Metadata: metadata,
	}
	select {
	case c.sender.ch <- payload:
	default:
	}
	return nil
}

func (c *metricCore) Sync() error { return nil }

func filepathBase(input string) string {
	idx := strings.LastIndex(input, string(os.PathSeparator))
	if idx == -1 {
		return input
	}
	return input[idx+1:]
}

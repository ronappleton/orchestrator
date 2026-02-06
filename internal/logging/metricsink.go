package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
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

type metricPoint struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp string            `json:"timestamp,omitempty"`
}

type metricSender struct {
	baseURL string
	apiKey  string
	source  string
	env     string
	tags    string
	client  *http.Client
	ch      chan metricPayload
	mu      sync.Mutex
	counts  map[string]int
}

func newMetricSender(baseURL string, apiKey string, source string, env string, tags string) *metricSender {
	return &metricSender{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		source:  source,
		env:     env,
		tags:    tags,
		client:  &http.Client{Timeout: 3 * time.Second},
		ch:      make(chan metricPayload, 200),
		counts:  map[string]int{},
	}
}

func (s *metricSender) start() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case payload, ok := <-s.ch:
				if !ok {
					return
				}
				s.sendLog(payload)
				s.bump(payload.Level)
			case <-ticker.C:
				s.flushMetrics()
			}
		}
	}()
}

func (s *metricSender) sendLog(payload metricPayload) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/v1/logs", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	_, _ = s.client.Do(req)
}

func (s *metricSender) bump(level string) {
	s.mu.Lock()
	s.counts[level]++
	s.mu.Unlock()
}

func (s *metricSender) flushMetrics() {
	s.mu.Lock()
	counts := make(map[string]int, len(s.counts))
	for k, v := range s.counts {
		counts[k] = v
	}
	for k := range s.counts {
		delete(s.counts, k)
	}
	s.mu.Unlock()

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	baseLabels := map[string]string{}
	if s.env != "" {
		baseLabels["env"] = s.env
	}
	if s.tags != "" {
		baseLabels["tags"] = s.tags
	}

	points := []metricPoint{{
		Name:      "service_heartbeat",
		Value:     1,
		Labels:    mergeLabels(baseLabels, map[string]string{"component": "log_sink"}),
		Timestamp: ts,
	}}
	for level, count := range counts {
		if count == 0 {
			continue
		}
		points = append(points, metricPoint{
			Name:      "log_count_total",
			Value:     float64(count),
			Labels:    mergeLabels(baseLabels, map[string]string{"level": level}),
			Timestamp: ts,
		})
	}

	body, _ := json.Marshal(map[string]any{
		"source":  s.source,
		"metrics": points,
	})
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/v1/metrics", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	_, _ = s.client.Do(req)
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
	env := os.Getenv("METRIC_SERVICE_ENV")
	tags := normalizeTags(os.Getenv("METRIC_SERVICE_TAGS"))
	sender := newMetricSender(baseURL, apiKey, source, env, tags)
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
	if c.sender.env != "" {
		metadata["env"] = c.sender.env
	}
	if c.sender.tags != "" {
		metadata["tags"] = c.sender.tags
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

func normalizeTags(tags string) string {
	if tags == "" {
		return ""
	}
	parts := strings.Split(tags, ",")
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			cleaned = append(cleaned, t)
		}
	}
	return strings.Join(cleaned, ",")
}

func mergeLabels(base map[string]string, extra map[string]string) map[string]string {
	if len(base) == 0 {
		return extra
	}
	merged := map[string]string{}
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

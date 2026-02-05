package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ronappleton/orchestrator/internal/workflow"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("content-type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("# metrics placeholder\n"))
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Docs not yet available"))
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var wf workflow.Workflow
		if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(wf.Name) == "" || len(wf.Steps) == 0 {
			http.Error(w, "name and steps required", http.StatusBadRequest)
			return
		}
		if err := workflow.ValidateAgainstSchema(s.cfg.Policy.WorkflowSchema, wf); err != nil {
			http.Error(w, "schema validation failed", http.StatusBadRequest)
			return
		}
		wf = s.wf.CreateWorkflow(wf)
		writeJSON(w, wf)
	case http.MethodGet:
		items := s.wf.ListWorkflows()
		writeJSON(w, map[string]any{"items": items})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowVersionRoutes(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/versions") {
		s.handleWorkflowVersions(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/rollback") {
		s.handleWorkflowRollback(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) handleWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/workflows/")
	id = strings.TrimSuffix(id, "/versions")
	if id == "" {
		http.Error(w, "workflow id required", http.StatusBadRequest)
		return
	}
	versions := s.wf.ListVersions(id)
	writeJSON(w, map[string]any{"items": versions})
}

func (s *Server) handleWorkflowRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/workflows/")
	id = strings.TrimSuffix(id, "/rollback")
	var body struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	wf, err := s.wf.RollbackVersion(id, body.Version)
	if err != nil {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	}
	writeJSON(w, wf)
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"items": workflow.BuiltinTemplates})
	case http.MethodPost:
		var wf workflow.Workflow
		if err := json.NewDecoder(r.Body).Decode(&wf); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(wf.Name) == "" || len(wf.Steps) == 0 {
			http.Error(w, "name and steps required", http.StatusBadRequest)
			return
		}
		wf = s.wf.CreateWorkflow(wf)
		writeJSON(w, wf)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		WorkflowID string                 `json:"workflow_id"`
		Context    map[string]interface{} `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.WorkflowID) == "" {
		http.Error(w, "workflow_id required", http.StatusBadRequest)
		return
	}
	run, err := s.wf.StartRun(r.Context(), body.WorkflowID, body.Context)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, run)
}

func (s *Server) handleRunByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	if path == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(path, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		if action == "logs" && len(parts) > 1 && len(parts) == 2 {
			logs := s.wf.ListLogs(id)
			writeJSON(w, map[string]any{"items": logs})
			return
		}
		if action == "logs" && len(parts) > 2 && parts[2] == "stream" {
			s.handleLogStream(w, r, id)
			return
		}
		run, err := s.wf.GetRun(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, run)
	case http.MethodPost:
		switch action {
		case "approve":
			run, err := s.wf.ApproveRun(r.Context(), id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, run)
		case "cancel":
			run, err := s.wf.CancelRun(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, run)
		case "logs":
			body := readBody(r)
			if len(body) == 0 {
				http.Error(w, "message required", http.StatusBadRequest)
				return
			}
			s.wf.AppendLog(id, string(body))
			writeJSON(w, map[string]any{"ok": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("content-type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request, runID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	lastIdx := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			logs := s.wf.ListLogs(runID)
			for lastIdx < len(logs) {
				msg := logs[lastIdx]
				_, _ = w.Write([]byte("data: " + msg + "\n\n"))
				flusher.Flush()
				lastIdx++
			}
		}
	}
}

func readBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return b
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

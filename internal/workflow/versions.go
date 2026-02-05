package workflow

import (
	"encoding/json"
	"time"
)

type WorkflowVersion struct {
	ID          string    `json:"id"`
	WorkflowID  string    `json:"workflow_id"`
	Version     int       `json:"version"`
	Payload     Workflow  `json:"payload"`
	CreatedAt   time.Time `json:"created_at"`
	Description string    `json:"description,omitempty"`
}

// In-memory version tracking (PG store can extend later).
var versions = map[string][]WorkflowVersion{}

func saveVersion(w Workflow) WorkflowVersion {
	v := WorkflowVersion{
		ID:         newID("wfver"),
		WorkflowID: w.ID,
		Version:    len(versions[w.ID]) + 1,
		Payload:    w,
		CreatedAt:  time.Now().UTC(),
	}
	versions[w.ID] = append(versions[w.ID], v)
	return v
}

func ListVersions(id string) []WorkflowVersion {
	return versions[id]
}

func RollbackVersion(id string, version int) (Workflow, error) {
	vlist := versions[id]
	for _, v := range vlist {
		if v.Version == version {
			// deep copy via JSON to avoid shared refs
			raw, _ := json.Marshal(v.Payload)
			var w Workflow
			_ = json.Unmarshal(raw, &w)
			return w, nil
		}
	}
	return Workflow{}, ErrNotFound
}

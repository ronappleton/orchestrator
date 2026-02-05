package workflow

import "time"

type WorkflowVersion struct {
	ID          string    `json:"id"`
	WorkflowID  string    `json:"workflow_id"`
	Version     int       `json:"version"`
	Payload     Workflow  `json:"payload"`
	CreatedAt   time.Time `json:"created_at"`
	Description string    `json:"description,omitempty"`
}

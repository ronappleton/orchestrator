package workflow

import "time"

type Workflow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Steps       []Step    `json:"steps"`
	CreatedAt   time.Time `json:"created_at"`
}

type Step struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Action           string      `json:"action"`
	Input            interface{} `json:"input,omitempty"`
	Retry            RetryPolicy `json:"retry,omitempty"`
	RequiresApproval bool        `json:"requires_approval,omitempty"`
}

type RetryPolicy struct {
	Max       int `json:"max,omitempty"`
	BackoffMs int `json:"backoff_ms,omitempty"`
}

type Run struct {
	ID          string                 `json:"id"`
	WorkflowID  string                 `json:"workflow_id"`
	Status      string                 `json:"status"`
	CurrentStep int                    `json:"current_step"`
	Context     map[string]interface{} `json:"context,omitempty"`
	Steps       []StepRun              `json:"steps,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

type StepRun struct {
	StepID   string `json:"step_id"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Output   string `json:"output,omitempty"`
	HTTPCode int    `json:"http_code,omitempty"`
}

const (
	StatusPending         = "PENDING"
	StatusRunning         = "RUNNING"
	StatusWaitingApproval = "WAITING_APPROVAL"
	StatusSucceeded       = "SUCCEEDED"
	StatusFailed          = "FAILED"
	StatusCanceled        = "CANCELED"
)

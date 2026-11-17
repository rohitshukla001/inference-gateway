// Package jobs persists async inference jobs and the state machine the
// gateway and worker share:
//
//	pending --claim--> running --success--> completed
//	                           --failure--> failed
//	                           --sweep----> failed (stuck)
//	pending --enqueue failure or sweep----> failed
package jobs

import (
	"encoding/json"
	"errors"
	"time"
)

// Status is a job's lifecycle state.
type Status string

// Status values.
const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// ErrNotFound is returned when the job is missing or belongs to another
// tenant; the cases are merged so job IDs don't leak across tenants.
var ErrNotFound = errors.New("job not found")

// Job is a single async inference job row.
type Job struct {
	JobID            string
	TenantID         string
	Status           Status
	RequestPayload   json.RawMessage
	ResponsePayload  json.RawMessage
	ErrorMessage     string
	CreatedAt        time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
}

// IsTerminal reports whether the job has finished and won't transition again.
func (j *Job) IsTerminal() bool {
	return j.Status == StatusCompleted || j.Status == StatusFailed
}

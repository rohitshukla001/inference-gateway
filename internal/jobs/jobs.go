// Package jobs persists async inference jobs and exposes the state machine
// the gateway and worker share. The state graph is:
//
//	pending --claim--> running --success--> completed
//	                          --failure--> failed
//	                          --sweep---->  failed (stuck timeout)
package jobs

import (
	"encoding/json"
	"errors"
	"time"
)

// Status is one of the four states a job can be in.
type Status string

// Status values.
const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// ErrNotFound is returned when the job does not exist or does not belong to
// the calling tenant. We deliberately collapse those two cases so the API
// does not leak which job_ids are valid for other tenants.
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

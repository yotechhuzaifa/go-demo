package dto

// CreateTaskRequest is the JSON body accepted by POST /api/v1/tasks.
//
// Pointer fields are not needed for create: omitted and empty both mean "use
// the default". Validator tags document the transport-level rules; the domain
// performs the authoritative validation as well.
type CreateTaskRequest struct {
	Title       string `json:"title" validate:"required,max=200" example:"Write the API documentation"`
	Description string `json:"description" validate:"max=2000" example:"Generate Swagger and verify the contract"`
	Priority    string `json:"priority" validate:"omitempty,oneof=low medium high" example:"high"`

	// DueDate accepts an ISO date, RFC3339 timestamp, or a flexible format
	// understood by araddon/dateparse. Date-only values are interpreted in
	// Timezone (default UTC).
	DueDate  string `json:"due_date" validate:"omitempty" example:"2026-12-31"`
	Timezone string `json:"timezone" validate:"omitempty" example:"Asia/Karachi"`
}

// UpdateTaskRequest is the JSON body accepted by PATCH /api/v1/tasks/{id}.
//
// Pointers distinguish "field omitted" (nil) from "field provided". DueDate is a
// pointer to a string so an explicit null can clear the deadline, while an
// omitted field leaves it unchanged. JSON null and omission both decode to nil
// in this simple version; ClearDueDate exists for clients that want clarity.
type UpdateTaskRequest struct {
	Title        *string `json:"title" validate:"omitempty,min=1,max=200"`
	Description  *string `json:"description" validate:"omitempty,max=2000"`
	Status       *string `json:"status" validate:"omitempty,oneof=todo in_progress done"`
	Priority     *string `json:"priority" validate:"omitempty,oneof=low medium high"`
	DueDate      *string `json:"due_date" validate:"omitempty"`
	ClearDueDate bool    `json:"clear_due_date"`
	Timezone     string  `json:"timezone" validate:"omitempty"`
}

// Task is the response representation of a task.
//
// It contains the stored fields plus date values computed at request time. The
// computed fields (due_date_human, is_overdue, days_until_due, due_week) are
// exactly the kind of presentation concern that belongs in a DTO, not in the
// database or the domain entity.
type Task struct {
	ID          string  `json:"id" example:"4f4c0f4e-6b1a-4e2f-9c3d-2a1b0c9d8e7f"`
	Title       string  `json:"title" example:"Write the API documentation"`
	Description string  `json:"description" example:"Generate Swagger and verify the contract"`
	Status      string  `json:"status" example:"todo"`
	Priority    string  `json:"priority" example:"high"`
	DueDate     *string `json:"due_date,omitempty" example:"2026-12-31T00:00:00Z"`

	// Computed date descriptions. They are not stored and change as time passes.
	DueDateHuman string `json:"due_date_human,omitempty" example:"in 2 months"`
	DueWeek      string `json:"due_week,omitempty" example:"2026-W53"`
	IsOverdue    bool   `json:"is_overdue" example:"false"`
	DaysUntilDue int    `json:"days_until_due,omitempty" example:"73"`

	CreatedAt string `json:"created_at" example:"2026-10-19T12:00:00Z"`
	UpdatedAt string `json:"updated_at" example:"2026-10-19T12:00:00Z"`
}

// TaskResponse wraps a single task in the standard `data` envelope.
type TaskResponse struct {
	Data Task `json:"data"`
}

// TaskListMeta describes the page that was actually returned.
type TaskListMeta struct {
	Limit  int `json:"limit" example:"20"`
	Offset int `json:"offset" example:"0"`
	Count  int `json:"count" example:"3"`
}

// TaskListResponse is the list shape: data plus pagination metadata.
type TaskListResponse struct {
	Data []Task       `json:"data"`
	Meta TaskListMeta `json:"meta"`
}

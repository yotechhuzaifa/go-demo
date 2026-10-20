package dto

// CreateNoteRequest is the JSON body accepted by POST /api/v1/notes.
//
// TaskID deliberately carries no `uuid` validation tag. The domain owns that
// rule (note.normalizeOptionalTaskID), and the domain also accepts an empty
// string to mean "this note is not attached to a task". A tag on this field
// would have to choose between those two cases and get one of them wrong.
type CreateNoteRequest struct {
	TaskID string   `json:"task_id" example:"4f4c0f4e-6b1a-4e2f-9c3d-2a1b0c9d8e7f"`
	Author string   `json:"author" validate:"omitempty,max=100" example:"yotech"`
	Body   string   `json:"body" validate:"required,max=5000" example:"Follow up with the client on Monday."`
	Tags   []string `json:"tags" validate:"omitempty,max=10,dive,max=40" example:"follow-up,client"`
}

// UpdateNoteRequest is the JSON body accepted by PATCH /api/v1/notes/{id}.
//
// TaskID is a pointer so "omitted" and "set to an empty string" stay
// distinguishable: an empty string detaches the note from its task. Note that
// `validate:"omitempty,uuid"` cannot express that on a *string — omitempty
// skips a nil pointer but not a pointer to "", so the uuid tag would reject
// the one value the API is supposed to accept. The domain validates the
// non-empty case instead.
type UpdateNoteRequest struct {
	TaskID *string   `json:"task_id"`
	Author *string   `json:"author" validate:"omitempty,max=100"`
	Body   *string   `json:"body" validate:"omitempty,min=1,max=5000"`
	Tags   *[]string `json:"tags" validate:"omitempty,max=10,dive,max=40"`
}

// Note is the response representation of a MongoDB note.
type Note struct {
	ID        string   `json:"id" example:"0b0a4f9e-3d3e-4a90-8b5c-6d7e8f9a0b1c"`
	TaskID    string   `json:"task_id,omitempty" example:"4f4c0f4e-6b1a-4e2f-9c3d-2a1b0c9d8e7f"`
	Author    string   `json:"author" example:"yotech"`
	Body      string   `json:"body" example:"Follow up with the client on Monday."`
	Tags      []string `json:"tags" example:"follow-up,client"`
	CreatedAt string   `json:"created_at" example:"2026-10-19T12:00:00Z"`
	UpdatedAt string   `json:"updated_at" example:"2026-10-19T12:00:00Z"`
}

// NoteResponse wraps a single note.
type NoteResponse struct {
	Data Note `json:"data"`
}

// NoteListMeta describes the returned page.
type NoteListMeta struct {
	Limit  int `json:"limit" example:"20"`
	Offset int `json:"offset" example:"0"`
	Count  int `json:"count" example:"3"`
}

// NoteListResponse is the list shape for notes.
type NoteListResponse struct {
	Data []Note       `json:"data"`
	Meta NoteListMeta `json:"meta"`
}

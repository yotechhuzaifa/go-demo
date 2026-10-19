package httpapi

import (
	"time"

	"example.com/taskflow/internal/domain/note"
	"example.com/taskflow/internal/domain/task"
	"example.com/taskflow/internal/platform/dates"
	"example.com/taskflow/internal/transport/httpapi/dto"
)

// taskToDTO converts a domain task into its wire representation.
//
// This is the "date manipulation" boundary: the database stores one UTC
// instant, while clients receive an RFC3339 string plus human-friendly fields.
// Keeping the conversion here means the domain never computes presentation
// values and the values can never become stale in storage.
func taskToDTO(value task.Task, now time.Time) dto.Task {
	result := dto.Task{
		ID:          value.ID,
		Title:       value.Title,
		Description: value.Description,
		Status:      string(value.Status),
		Priority:    string(value.Priority),
		CreatedAt:   value.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   value.UpdatedAt.UTC().Format(time.RFC3339),
	}

	if value.DueDate != nil {
		due := value.DueDate.UTC()
		formatted := due.Format(time.RFC3339)
		result.DueDate = &formatted
		result.DueWeek = dates.WeekLabel(due)

		description := dates.DescribeDue(&due, now)
		result.DueDateHuman = description.Human
		result.IsOverdue = description.IsOverdue
		result.DaysUntilDue = description.DaysUntil
	}

	return result
}

// noteToDTO converts a domain note into its wire representation.
func noteToDTO(value note.Note) dto.Note {
	tags := value.Tags
	if tags == nil {
		tags = []string{}
	}
	return dto.Note{
		ID:        value.ID,
		TaskID:    value.TaskID,
		Author:    value.Author,
		Body:      value.Body,
		Tags:      tags,
		CreatedAt: value.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: value.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// tasksToDTO converts a slice and always returns a non-nil slice so JSON shows
// [] instead of null.
func tasksToDTO(values []task.Task, now time.Time) []dto.Task {
	result := make([]dto.Task, 0, len(values))
	for _, value := range values {
		result = append(result, taskToDTO(value, now))
	}
	return result
}

// notesToDTO converts a slice and always returns a non-nil slice.
func notesToDTO(values []note.Note) []dto.Note {
	result := make([]dto.Note, 0, len(values))
	for _, value := range values {
		result = append(result, noteToDTO(value))
	}
	return result
}

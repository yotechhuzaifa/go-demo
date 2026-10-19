package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"example.com/taskflow/internal/domain/note"
	"example.com/taskflow/internal/domain/task"
	"example.com/taskflow/internal/platform/dates"
)

// seedDemoData inserts a small, realistic data set the first time the service
// runs against an empty database.
//
// The seed is idempotent: it checks for at least one task before doing anything.
// It goes through the domain services (not raw SQL) so demo data exercises the
// same validation, caching, and audit paths as real traffic. That makes the
// first startup an end-to-end test of all three stores.
func seedDemoData(
	ctx context.Context,
	tasks *task.Service,
	notes *note.Service,
	logger *slog.Logger,
) error {
	existing, err := tasks.List(ctx, task.Filter{Limit: 1})
	if err != nil {
		return fmt.Errorf("check existing tasks: %w", err)
	}
	if len(existing) > 0 {
		logger.Info("seed: task table is not empty; skipping demo tasks",
			"existing_task_id", existing[0].ID)
		return nil
	}

	now := time.Now().UTC()

	// Due dates use business-day math so the demo looks like real work. One
	// task is intentionally overdue to exercise the is_overdue / human fields.
	samples := []struct {
		title       string
		description string
		priority    task.Priority
		status      task.Status
		dueOffset   int // Business days from now; negative means calendar days ago.
	}{
		{
			title:       "Ship the cross-database overview",
			description: "Read the task from PostgreSQL, notes from MongoDB, and the counter from Redis in one request.",
			priority:    task.PriorityHigh,
			status:      task.StatusInProgress,
			dueOffset:   3,
		},
		{
			title:       "Document the API contract",
			description: "Regenerate Swagger with `make docs` and verify the DTO schemas.",
			priority:    task.PriorityMedium,
			status:      task.StatusTodo,
			dueOffset:   7,
		},
		{
			title:       "Review Redis disconnect handling",
			description: "Kill the connection, watch the retry logs, then confirm the fallback still serves reads.",
			priority:    task.PriorityLow,
			status:      task.StatusDone,
			dueOffset:   -2,
		},
	}

	createdTasks := make([]task.Task, 0, len(samples))
	for index, sample := range samples {
		var due time.Time
		if sample.dueOffset < 0 {
			due = now.AddDate(0, 0, sample.dueOffset)
		} else {
			due = dates.AddBusinessDays(now, sample.dueOffset)
		}

		created, err := tasks.Create(ctx, task.CreateInput{
			Title:       sample.title,
			Description: sample.description,
			Priority:    sample.priority,
			DueDate:     &due,
		})
		if err != nil {
			return fmt.Errorf("create demo task %d: %w", index+1, err)
		}

		// Create always starts in `todo`; move the two non-todo samples to
		// their intended state through the normal update path.
		if sample.status != task.StatusTodo {
			status := sample.status
			created, err = tasks.Update(ctx, created.ID, task.UpdateInput{Status: &status})
			if err != nil {
				return fmt.Errorf("set status on demo task %d: %w", index+1, err)
			}
		}

		createdTasks = append(createdTasks, created)
		logger.Info("seed: demo task created",
			"task_id", created.ID,
			"title", created.Title,
			"status", created.Status,
			"due_date", created.DueDate,
		)
	}

	if len(createdTasks) == 0 {
		return nil
	}

	// Attach notes to the first task so the overview endpoint has MongoDB data.
	primaryTaskID := createdTasks[0].ID
	existingNotes, err := notes.CountByTask(ctx, primaryTaskID)
	if err != nil {
		return fmt.Errorf("check existing notes: %w", err)
	}
	if existingNotes > 0 {
		logger.Info("seed: notes already exist; skipping demo notes",
			"task_id", primaryTaskID, "count", existingNotes)
		return nil
	}

	noteSamples := []note.CreateInput{
		{
			TaskID: primaryTaskID,
			Author: "yotech",
			Body:   "The overview response should make the source of each field obvious to API consumers.",
			Tags:   []string{"architecture", "api"},
		},
		{
			TaskID: primaryTaskID,
			Author: "yotech",
			Body:   "Check that a Redis outage degrades to PostgreSQL reads instead of returning 500.",
			Tags:   []string{"redis", "resilience"},
		},
		{
			TaskID: primaryTaskID,
			Author: "taskflow-bot",
			Body:   "Swagger is generated from handler annotations by `make docs`.",
			Tags:   []string{"documentation"},
		},
	}

	for index, sample := range noteSamples {
		created, err := notes.Create(ctx, sample)
		if err != nil {
			return fmt.Errorf("create demo note %d: %w", index+1, err)
		}
		logger.Info("seed: demo note created",
			"note_id", created.ID,
			"task_id", created.TaskID,
			"author", created.Author,
		)
	}

	return nil
}

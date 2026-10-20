package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"example.com/taskflow/internal/domain/task"
)

// TaskRepository implements task.Repository with explicit SQL.
type TaskRepository struct {
	db     *DB
	table  string
	logger *slog.Logger
}

// Compile-time proof that the adapter satisfies the domain contract.
var _ task.Repository = (*TaskRepository)(nil)

// taskColumns is shared by SELECT statements so a schema change cannot make
// the scan order drift between GetByID and List.
const taskColumns = `id, title, description, status, priority, due_date, created_at, updated_at`

// taskRow is the PostgreSQL row shape.
//
// The db tags are what pgx matches against the returned column names, so the
// mapping happens by name and not by position. A query that returns the same
// columns in a different order still scans into the right fields.
//
// The db tags and taskColumns must list the same columns. pgx fails loudly
// when they disagree ("struct doesn't have corresponding row field x" or
// "cannot find field x in returned row"), so a mismatch surfaces on the first
// query instead of silently storing wrong values.
//
// status and priority are plain strings here. A named string type would have
// to implement sql.Scanner to be scanned directly, and that would put a
// database concern inside the domain package.
type taskRow struct {
	ID          string     `db:"id"`
	Title       string     `db:"title"`
	Description string     `db:"description"`
	Status      string     `db:"status"`
	Priority    string     `db:"priority"`
	DueDate     *time.Time `db:"due_date"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
}

// toTask converts a database row into the domain entity. Timestamps are
// forced to UTC so callers never have to reason about the session's timezone.
func (r taskRow) toTask() task.Task {
	value := task.Task{
		ID:          r.ID,
		Title:       r.Title,
		Description: r.Description,
		Status:      task.Status(r.Status),
		Priority:    task.Priority(r.Priority),
		CreatedAt:   r.CreatedAt.UTC(),
		UpdatedAt:   r.UpdatedAt.UTC(),
	}
	if r.DueDate != nil {
		due := r.DueDate.UTC()
		value.DueDate = &due
	}
	return value
}

// NewTaskRepository builds the adapter.
func NewTaskRepository(db *DB, logger *slog.Logger) *TaskRepository {
	if db == nil || db.Pool == nil {
		panic("postgres.NewTaskRepository: db must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Sanitize quotes the identifier safely, so the schema and table names
	// cannot break out of SQL syntax.
	table := pgx.Identifier{Schema, "tasks"}.Sanitize()
	return &TaskRepository{db: db, table: table, logger: logger}
}

// Create inserts a task.
func (r *TaskRepository) Create(ctx context.Context, value *task.Task) error {
	start := time.Now()
	query := fmt.Sprintf(`
		INSERT INTO %s
			(id, title, description, status, priority, due_date, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, r.table)

	r.logger.DebugContext(ctx, "postgres: executing INSERT",
		"table", r.table, "task_id", value.ID)

	_, err := r.db.Pool.Exec(ctx, query,
		value.ID,
		value.Title,
		value.Description,
		string(value.Status),
		string(value.Priority),
		value.DueDate,
		value.CreatedAt,
		value.UpdatedAt,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "postgres: INSERT failed",
			"table", r.table, "task_id", value.ID,
			"duration_ms", durationMS(start), "error", err)
		return translateError("insert task", err)
	}

	r.logger.InfoContext(ctx, "postgres: INSERT committed",
		"table", r.table, "task_id", value.ID, "duration_ms", durationMS(start))
	return nil
}

// GetByID returns one task or task.ErrNotFound.
func (r *TaskRepository) GetByID(ctx context.Context, id string) (task.Task, error) {
	start := time.Now()
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE id = $1`, taskColumns, r.table)

	r.logger.DebugContext(ctx, "postgres: executing SELECT by id",
		"table", r.table, "task_id", id)

	// Previous version, kept for comparison. The row was scanned by hand
	// into a fixed list of destinations:
	//
	//	value, err := scanTask(r.db.Pool.QueryRow(ctx, query, id))
	//	if err != nil {
	//		if errors.Is(err, pgx.ErrNoRows) {
	//			return task.Task{}, task.ErrNotFound
	//		}
	//	}
	//
	// The column order and the scan order were two separate lists that had
	// to be kept in sync by hand. The version below lets pgx do the mapping
	// by column name, so there is only one list.
	rows, err := r.db.Pool.Query(ctx, query, id)
	if err != nil {
		r.logger.ErrorContext(ctx, "postgres: SELECT failed",
			"table", r.table, "task_id", id,
			"duration_ms", durationMS(start), "error", err)
		return task.Task{}, fmt.Errorf("select task by id: %w", err)
	}

	// CollectOneRow closes the rows itself and reports pgx.ErrNoRows when
	// the query matched nothing.
	row, err := pgx.CollectOneRow(rows, pgx.RowToStructByName[taskRow])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.DebugContext(ctx, "postgres: SELECT found no row",
				"table", r.table, "task_id", id, "duration_ms", durationMS(start))
			return task.Task{}, task.ErrNotFound
		}
		r.logger.ErrorContext(ctx, "postgres: SELECT failed",
			"table", r.table, "task_id", id,
			"duration_ms", durationMS(start), "error", err)
		return task.Task{}, fmt.Errorf("select task by id: %w", err)
	}

	r.logger.InfoContext(ctx, "postgres: SELECT completed",
		"table", r.table, "task_id", id, "duration_ms", durationMS(start))
	return row.toTask(), nil
}

// List returns filtered, sorted, paginated tasks.
func (r *TaskRepository) List(ctx context.Context, filter task.Filter) ([]task.Task, error) {
	start := time.Now()

	query := fmt.Sprintf(`SELECT %s FROM %s WHERE 1 = 1`, taskColumns, r.table)
	args := make([]any, 0, 4)

	// Parameters are appended in order; $n is derived from len(args) so the
	// query stays correct when filters are combined.
	if filter.Status != "" {
		args = append(args, string(filter.Status))
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if filter.Priority != "" {
		args = append(args, string(filter.Priority))
		query += fmt.Sprintf(" AND priority = $%d", len(args))
	}

	query += " ORDER BY created_at DESC, id ASC"

	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	r.logger.DebugContext(ctx, "postgres: executing filtered SELECT",
		"table", r.table,
		"status", filter.Status,
		"priority", filter.Priority,
		"limit", filter.Limit,
		"offset", filter.Offset,
	)

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		r.logger.ErrorContext(ctx, "postgres: filtered SELECT failed",
			"table", r.table, "duration_ms", durationMS(start), "error", err)
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	// Previous version, kept for comparison. Every row was scanned by hand
	// and the cursor was closed by hand:
	//
	//	defer rows.Close()
	//	results := make([]task.Task, 0, filter.Limit)
	//	for rows.Next() {
	//		value, scanErr := scanTask(rows)
	//		if scanErr != nil {
	//			return nil, fmt.Errorf("scan task row: %w", scanErr)
	//		}
	//		results = append(results, value)
	//	}
	//	if err := rows.Err(); err != nil {
	//		return nil, fmt.Errorf("iterate task rows: %w", err)
	//	}
	//
	// The version below lets pgx collect the rows, close the cursor, and
	// map the columns, so the loop disappears along with the hand-written
	// scan list.
	records, err := pgx.CollectRows(rows, pgx.RowToStructByName[taskRow])
	if err != nil {
		r.logger.ErrorContext(ctx, "postgres: row scan failed",
			"table", r.table, "error", err)
		return nil, fmt.Errorf("collect task rows: %w", err)
	}

	results := make([]task.Task, 0, len(records))
	for _, record := range records {
		results = append(results, record.toTask())
	}

	r.logger.InfoContext(ctx, "postgres: filtered SELECT completed",
		"table", r.table, "count", len(results), "duration_ms", durationMS(start))
	return results, nil
}

// Update replaces the mutable fields of an existing task.
func (r *TaskRepository) Update(ctx context.Context, value *task.Task) error {
	start := time.Now()
	query := fmt.Sprintf(`
		UPDATE %s
		SET title = $2,
		    description = $3,
		    status = $4,
		    priority = $5,
		    due_date = $6,
		    updated_at = $7
		WHERE id = $1`, r.table)

	r.logger.DebugContext(ctx, "postgres: executing UPDATE",
		"table", r.table, "task_id", value.ID)

	tag, err := r.db.Pool.Exec(ctx, query,
		value.ID,
		value.Title,
		value.Description,
		string(value.Status),
		string(value.Priority),
		value.DueDate,
		value.UpdatedAt,
	)
	if err != nil {
		r.logger.ErrorContext(ctx, "postgres: UPDATE failed",
			"table", r.table, "task_id", value.ID,
			"duration_ms", durationMS(start), "error", err)
		return translateError("update task", err)
	}
	if tag.RowsAffected() == 0 {
		r.logger.WarnContext(ctx, "postgres: UPDATE matched no row",
			"table", r.table, "task_id", value.ID)
		return task.ErrNotFound
	}

	r.logger.InfoContext(ctx, "postgres: UPDATE committed",
		"table", r.table, "task_id", value.ID, "duration_ms", durationMS(start))
	return nil
}

// Delete removes a task.
func (r *TaskRepository) Delete(ctx context.Context, id string) error {
	start := time.Now()
	query := fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, r.table)

	r.logger.DebugContext(ctx, "postgres: executing DELETE",
		"table", r.table, "task_id", id)

	tag, err := r.db.Pool.Exec(ctx, query, id)
	if err != nil {
		r.logger.ErrorContext(ctx, "postgres: DELETE failed",
			"table", r.table, "task_id", id,
			"duration_ms", durationMS(start), "error", err)
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		r.logger.WarnContext(ctx, "postgres: DELETE matched no row",
			"table", r.table, "task_id", id)
		return task.ErrNotFound
	}

	r.logger.InfoContext(ctx, "postgres: DELETE committed",
		"table", r.table, "task_id", id, "duration_ms", durationMS(start))
	return nil
}

// scanTask reads one row using the column order in taskColumns.
//
// Previous version, kept for comparison. The destinations were listed by
// hand and had to match taskColumns position for position:
//
//	func scanTask(row interface {
//		Scan(dest ...any) error
//	}) (task.Task, error) {
//		var (
//			value    task.Task
//			status   string
//			priority string
//		)
//
//		err := row.Scan(
//			&value.ID,
//			&value.Title,
//			&value.Description,
//			&status,
//			&priority,
//			&value.DueDate,
//			&value.CreatedAt,
//			&value.UpdatedAt,
//		)
//		if err != nil {
//			return task.Task{}, err
//		}
//
//		value.Status = task.Status(status)
//		value.Priority = task.Priority(priority)
//		value.CreatedAt = value.CreatedAt.UTC()
//		value.UpdatedAt = value.UpdatedAt.UTC()
//		if value.DueDate != nil {
//			utc := value.DueDate.UTC()
//			value.DueDate = &utc
//		}
//		return value, nil
//	}
//
// The replacement is taskRow plus pgx.RowToStructByName, which maps by column
// name and needs no list of destinations at all.

// translateError maps PostgreSQL's unique-violation code to the domain
// sentinel. Other errors keep their driver detail so logs remain useful.
func translateError(operation string, err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return task.ErrAlreadyExists
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// durationMS converts a start time into fractional milliseconds for logs.
func durationMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

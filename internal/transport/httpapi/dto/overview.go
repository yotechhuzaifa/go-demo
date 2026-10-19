package dto

// OverviewResponse is returned by GET /api/v1/tasks/{id}/overview.
//
// This is the single endpoint that intentionally reads from all three stores:
//
//	PostgreSQL -> the authoritative task (through the Redis cache)
//	MongoDB    -> attached notes and their count
//	Redis      -> the atomic overview-view counter
//
// Mixing sources in one response is a common dashboard pattern. The DTO makes
// the provenance explicit so clients know which values are strongly consistent
// (the PostgreSQL task) and which are eventually consistent (the Redis counter).
type OverviewResponse struct {
	Task        Task     `json:"task"`
	NotesCount  int64    `json:"notes_count" example:"2"`
	RecentNotes []Note   `json:"recent_notes"`
	ViewCount   int64    `json:"view_count" example:"42"`
	WeekLabel   string   `json:"week_label" example:"2026-W43"`
	GeneratedAt string   `json:"generated_at" example:"2026-10-19T12:00:00Z"`
	Sources     []string `json:"sources" example:"postgresql,mongodb,redis"`
}

// OverviewEnvelope wraps the overview in the standard `data` envelope.
type OverviewEnvelope struct {
	Data OverviewResponse `json:"data"`
}

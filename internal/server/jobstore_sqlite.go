package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS jobs (
	id         TEXT PRIMARY KEY,
	type       TEXT NOT NULL,
	status     TEXT NOT NULL,
	request    TEXT NOT NULL,
	result     TEXT,
	error      TEXT NOT NULL DEFAULT '',
	issue_ref  TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS job_logs (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id  TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
	time    TEXT NOT NULL,
	message TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(type);
CREATE INDEX IF NOT EXISTS idx_jobs_issue_ref ON jobs(issue_ref);
CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_job_logs_job_id ON job_logs(job_id);
`

// SQLiteJobStore persists jobs to a SQLite database.
type SQLiteJobStore struct {
	db *sql.DB
}

// NewSQLiteJobStore opens (or creates) a SQLite database at path and
// applies the schema. The path can be ":memory:" for testing.
func NewSQLiteJobStore(path string) (*SQLiteJobStore, error) {
	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite performs best with a single writer connection.
	db.SetMaxOpenConns(1)

	// Enable foreign keys via explicit PRAGMA — the modernc.org/sqlite
	// driver does not honour _foreign_keys=ON in the DSN.
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if _, err := db.Exec(sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &SQLiteJobStore{db: db}, nil
}

func (s *SQLiteJobStore) Create(ctx context.Context, job *Job) (string, error) {
	if job.ID == "" {
		job.ID = generateJobID()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now

	reqJSON, err := json.Marshal(job.Request)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	issueRef := extractIssueRef(job.Request)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, type, status, request, error, issue_ref, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '', ?, ?, ?)`,
		job.ID, job.Type, job.Status,
		string(reqJSON), issueRef,
		job.CreatedAt.Format(time.RFC3339Nano),
		job.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return "", fmt.Errorf("insert job: %w", err)
	}

	return job.ID, nil
}

func (s *SQLiteJobStore) Get(ctx context.Context, id string) (*Job, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, type, status, request, result, error, created_at, updated_at
		 FROM jobs WHERE id = ?`, id)

	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}

	logs, err := s.getLogs(ctx, id)
	if err != nil {
		return nil, err
	}
	job.Logs = logs

	return job, nil
}

func (s *SQLiteJobStore) Update(ctx context.Context, job *Job) error {
	var resultJSON []byte
	if job.Result != nil {
		var err error
		resultJSON, err = json.Marshal(job.Result)
		if err != nil {
			return fmt.Errorf("marshal result: %w", err)
		}
	}

	var resultStr *string
	if resultJSON != nil {
		rs := string(resultJSON)
		resultStr = &rs
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, result = ?, error = ?, updated_at = ? WHERE id = ?`,
		job.Status, resultStr, job.Error,
		job.UpdatedAt.Format(time.RFC3339Nano), job.ID,
	)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	return nil
}

func (s *SQLiteJobStore) List(ctx context.Context, f JobFilter) ([]*Job, error) {
	var conditions []string
	var args []interface{}

	if f.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, f.Status)
	}
	if f.Type != "" {
		conditions = append(conditions, "type = ?")
		args = append(args, f.Type)
	}
	if f.IssueRef != "" {
		conditions = append(conditions, "issue_ref = ?")
		args = append(args, f.IssueRef)
	}
	if !f.Since.IsZero() {
		conditions = append(conditions, "created_at > ?")
		args = append(args, f.Since.Format(time.RFC3339Nano))
	}
	if !f.Before.IsZero() {
		conditions = append(conditions, "created_at < ?")
		args = append(args, f.Before.Format(time.RFC3339Nano))
	}

	query := "SELECT id, type, status, request, result, error, created_at, updated_at FROM jobs"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, f.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		job, err := scanJobFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, job)
	}

	// Load logs for each job.
	for _, job := range jobs {
		logs, err := s.getLogs(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		job.Logs = logs
	}

	return jobs, rows.Err()
}

func (s *SQLiteJobStore) AddLog(ctx context.Context, jobID string, entry LogEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO job_logs (job_id, time, message) VALUES (?, ?, ?)`,
		jobID, entry.Time.Format(time.RFC3339Nano), entry.Message,
	)
	if err != nil {
		return fmt.Errorf("add log: %w", err)
	}
	return nil
}

func (s *SQLiteJobStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	return nil
}

func (s *SQLiteJobStore) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE created_at < ?`,
		before.Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, fmt.Errorf("delete old jobs: %w", err)
	}
	return result.RowsAffected()
}

func (s *SQLiteJobStore) Close() error {
	return s.db.Close()
}

// --- internal helpers ---

func (s *SQLiteJobStore) getLogs(ctx context.Context, jobID string) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT time, message FROM job_logs WHERE job_id = ? ORDER BY id`, jobID)
	if err != nil {
		return nil, fmt.Errorf("get logs: %w", err)
	}
	defer rows.Close()

	var logs []LogEntry
	for rows.Next() {
		var timeStr, msg string
		if err := rows.Scan(&timeStr, &msg); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		t, _ := time.Parse(time.RFC3339Nano, timeStr)
		logs = append(logs, LogEntry{Time: t, Message: msg})
	}
	return logs, rows.Err()
}

// scanJob scans a single job row from QueryRow.
func scanJob(row *sql.Row) (*Job, error) {
	var j Job
	var reqJSON, resultJSON sql.NullString
	var createdStr, updatedStr string

	err := row.Scan(&j.ID, &j.Type, &j.Status, &reqJSON, &resultJSON,
		&j.Error, &createdStr, &updatedStr)
	if err != nil {
		return nil, err
	}

	if reqJSON.Valid {
		var req interface{}
		json.Unmarshal([]byte(reqJSON.String), &req)
		j.Request = req
	}
	if resultJSON.Valid {
		var result interface{}
		json.Unmarshal([]byte(resultJSON.String), &result)
		j.Result = result
	}

	j.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	j.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)

	return &j, nil
}

// scanJobFromRows scans a job from an active Rows cursor.
func scanJobFromRows(rows *sql.Rows) (*Job, error) {
	var j Job
	var reqJSON, resultJSON sql.NullString
	var createdStr, updatedStr string

	err := rows.Scan(&j.ID, &j.Type, &j.Status, &reqJSON, &resultJSON,
		&j.Error, &createdStr, &updatedStr)
	if err != nil {
		return nil, err
	}

	if reqJSON.Valid {
		var req interface{}
		json.Unmarshal([]byte(reqJSON.String), &req)
		j.Request = req
	}
	if resultJSON.Valid {
		var result interface{}
		json.Unmarshal([]byte(resultJSON.String), &result)
		j.Result = result
	}

	j.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	j.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)

	return &j, nil
}

// extractIssueRef attempts to pull an issue reference from a job request
// for indexing. Supports BuildAPIRequest and generic maps.
func extractIssueRef(req interface{}) string {
	switch r := req.(type) {
	case BuildAPIRequest:
		return r.Issue
	case PlanAPIRequest:
		return r.Issue
	case map[string]interface{}:
		if v, ok := r["issue"].(string); ok {
			return v
		}
	}
	return ""
}

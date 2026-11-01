// Package store keeps durable idempotency claims and exact HTTP responses.
// The application must own its process lock before opening the store and before
// recovering interrupted operations. A stored unknown outcome must be reconciled
// with the device; it must never be automatically executed again.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	StateInProgress = "in_progress"
	StateCompleted  = "completed"
	StateUnknown    = "unknown"
)

var (
	ErrConflict     = errors.New("request ID was already used with different parameters")
	ErrNotFound     = errors.New("request not found")
	ErrInvalidState = errors.New("request is not in progress")
)

// Record is the persisted state of one request, scoped to a physical device's
// stable application ID. Fingerprint includes the canonical command and params.
type Record struct {
	DeviceID    string
	RequestID   string
	Fingerprint string
	Command     string
	Params      []byte
	State       string
	HTTPStatus  int
	Response    []byte
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ClaimResult distinguishes ownership from an existing request. Only the caller
// with Acquired=true may execute the command. An existing record may be in
// progress, completed (replay Response), or unknown (require reconciliation).
type ClaimResult struct {
	Acquired bool
	Record   Record
}

type Store struct {
	db *sql.DB
}

// Open opens a file-backed SQLite database. Newly created parent directories are
// private, and the database file is restricted to its owner. In-memory databases
// are rejected because idempotency must survive a process restart.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" || path == ":memory:" {
		return nil, errors.New("SQLite path must name a persistent file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("create SQLite directory: %w", err)
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create SQLite file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, fmt.Errorf("restrict SQLite file permissions: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close SQLite file: %w", err)
	}

	// _pragma is applied to every connection opened by the modernc driver.
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to SQLite: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS requests (
		device_id TEXT NOT NULL,
		request_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		command TEXT NOT NULL DEFAULT '',
		params BLOB NOT NULL DEFAULT X'',
		state TEXT NOT NULL CHECK (state IN ('in_progress', 'completed', 'unknown')),
		http_status INTEGER NOT NULL DEFAULT 0,
		response BLOB NOT NULL DEFAULT X'',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (device_id, request_id),
		CHECK ((state = 'completed' AND http_status BETWEEN 200 AND 599)
			OR (state <> 'completed' AND http_status = 0))
	) WITHOUT ROWID`); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Ping checks database availability without touching a physical device.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Claim atomically acquires a new request or returns its existing record. The
// unique primary key also protects claims made through independent connections.
func (s *Store) Claim(ctx context.Context, deviceID, requestID, fingerprint string) (ClaimResult, error) {
	return s.ClaimRequest(ctx, deviceID, requestID, fingerprint, "", nil)
}

// ClaimRequest atomically persists the canonical command and params together
// with its idempotency claim, retaining enough information for reconciliation
// after an interrupted or otherwise ambiguous device operation.
func (s *Store) ClaimRequest(ctx context.Context, deviceID, requestID, fingerprint, command string, params []byte) (ClaimResult, error) {
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(requestID) == "" || fingerprint == "" {
		return ClaimResult{}, errors.New("device ID, request ID and fingerprint are required")
	}
	if params == nil {
		params = []byte{}
	}
	now := time.Now().UTC().UnixNano()
	result, err := s.db.ExecContext(ctx, `INSERT INTO requests
		(device_id, request_id, fingerprint, command, params, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (device_id, request_id) DO NOTHING`,
		deviceID, requestID, fingerprint, command, params, StateInProgress, now, now)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("claim request: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return ClaimResult{}, fmt.Errorf("read claim result: %w", err)
	}
	record, err := s.Get(ctx, deviceID, requestID)
	if err != nil {
		return ClaimResult{}, err
	}
	if record.Fingerprint != fingerprint {
		return ClaimResult{}, ErrConflict
	}
	return ClaimResult{Acquired: count == 1, Record: record}, nil
}

// Complete persists the exact final response before the HTTP handler sends it.
// If persistence fails after executing a command, do not send a success response
// or re-execute the command; the existing claim remains conservative protection.
func (s *Store) Complete(ctx context.Context, deviceID, requestID string, httpStatus int, response []byte) error {
	if httpStatus < 200 || httpStatus > 599 {
		return errors.New("completed request must have a final HTTP status from 200 to 599")
	}
	if response == nil {
		response = []byte{}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE requests
		SET state = ?, http_status = ?, response = ?, updated_at = ?
		WHERE device_id = ? AND request_id = ? AND state = ?`,
		StateCompleted, httpStatus, response, time.Now().UTC().UnixNano(),
		deviceID, requestID, StateInProgress)
	if err != nil {
		return fmt.Errorf("complete request: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read completion result: %w", err)
	}
	if count == 1 {
		return nil
	}
	if _, err := s.Get(ctx, deviceID, requestID); err != nil {
		return err
	}
	return ErrInvalidState
}

// MarkUnknown records that communication failed after execution may have begun.
// Such a request remains claimed and can never be automatically acquired again.
func (s *Store) MarkUnknown(ctx context.Context, deviceID, requestID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE requests SET state = ?, updated_at = ?
		WHERE device_id = ? AND request_id = ? AND state = ?`,
		StateUnknown, time.Now().UTC().UnixNano(), deviceID, requestID, StateInProgress)
	if err != nil {
		return fmt.Errorf("mark request outcome unknown: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read unknown outcome result: %w", err)
	}
	if count == 1 {
		return nil
	}
	if _, err := s.Get(ctx, deviceID, requestID); err != nil {
		return err
	}
	return ErrInvalidState
}

func (s *Store) Get(ctx context.Context, deviceID, requestID string) (Record, error) {
	var record Record
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT device_id, request_id, fingerprint, command, params,
		state, http_status, response, created_at, updated_at
		FROM requests WHERE device_id = ? AND request_id = ?`, deviceID, requestID).Scan(
		&record.DeviceID, &record.RequestID, &record.Fingerprint, &record.Command, &record.Params, &record.State,
		&record.HTTPStatus, &record.Response, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("read request: %w", err)
	}
	record.CreatedAt = time.Unix(0, createdAt).UTC()
	record.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return record, nil
}

// RecoverInterrupted must run once at startup, before accepting requests and
// while the application holds its process lock. It never retries device work.
func (s *Store) RecoverInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE requests SET state = ?, updated_at = ?
		WHERE state = ?`, StateUnknown, time.Now().UTC().UnixNano(), StateInProgress)
	if err != nil {
		return fmt.Errorf("recover interrupted requests: %w", err)
	}
	return nil
}

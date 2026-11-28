// Package store persists wadad state in pure-Go SQLite: server identity,
// epochs, creation/delivery idempotency receipts, sequenced timelines, and
// the projects and workspaces.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"time"

	"wada-desktop-go/internal/protocol"
	dbstore "wada-desktop-go/internal/store"
)

const (
	DeliveryPending  = "pending"
	DeliveryAccepted = "accepted"
	DeliveryRejected = "rejected"
	DeliveryUnknown  = "unknown"
)

var daemonMigrations = []dbstore.Migration{{
	Version: 1,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range []string{
			`CREATE TABLE daemon_meta (
				key   TEXT PRIMARY KEY,
				value TEXT NOT NULL)`,
			`CREATE TABLE creation_receipts (
				idempotency_key TEXT PRIMARY KEY,
				kind            TEXT NOT NULL,
				resource_id     TEXT NOT NULL,
				request_hash    TEXT NOT NULL,
				created_at      INTEGER NOT NULL)`,
			`CREATE TABLE delivery_receipts (
				session_id   TEXT NOT NULL,
				message_id   TEXT NOT NULL,
				request_hash TEXT NOT NULL,
				status       TEXT NOT NULL,
				created_at   INTEGER NOT NULL,
				PRIMARY KEY (session_id, message_id))`,
			`CREATE TABLE timeline_events (
				session_id TEXT NOT NULL,
				epoch      INTEGER NOT NULL,
				seq        INTEGER NOT NULL,
				payload    TEXT NOT NULL,
				PRIMARY KEY (session_id, epoch, seq))`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	},
}, {
	Version: 2,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range []string{
			`CREATE TABLE projects (
				id       TEXT PRIMARY KEY,
				root     TEXT NOT NULL,
				name     TEXT NOT NULL,
				created  INTEGER NOT NULL,
				archived INTEGER NOT NULL)`,
			`CREATE UNIQUE INDEX projects_active_root ON projects(root) WHERE archived = 0`,
			`CREATE TABLE workspaces (
				id        TEXT PRIMARY KEY,
				project   TEXT NOT NULL REFERENCES projects(id),
				isolation TEXT NOT NULL,
				dir       TEXT NOT NULL,
				title     TEXT NOT NULL,
				created   INTEGER NOT NULL,
				archived  INTEGER NOT NULL)`,
			`CREATE UNIQUE INDEX workspaces_active_dir ON workspaces(dir) WHERE archived = 0`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	},
}, {
	Version: 3,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE workspaces ADD COLUMN worktree_root TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE workspaces ADD COLUMN owned INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE workspaces ADD COLUMN branch TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE workspaces ADD COLUMN base_ref TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE workspaces ADD COLUMN pr INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	},
}, {
	Version: 4,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range []string{
			`ALTER TABLE projects ADD COLUMN trusted_config TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE workspaces ADD COLUMN port_base INTEGER NOT NULL DEFAULT 0`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	},
}, {
	Version: 5,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ALTER TABLE workspaces ADD COLUMN auto_name INTEGER NOT NULL DEFAULT 0`)
		return err
	},
}, {
	Version: 6,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE schedules (
			id   TEXT PRIMARY KEY,
			data TEXT NOT NULL)`)
		return err
	},
}}

type CreationReceipt struct {
	IdempotencyKey string
	Kind           string
	ResourceID     string
	RequestHash    string
	CreatedAt      int64
}

type DeliveryReceipt struct {
	SessionID   string
	MessageID   string
	RequestHash string
	Status      string
	CreatedAt   int64
}

// Store owns wadad's SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens or initializes wadad.db inside dataDir.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	db, err := dbstore.Open(ctx, filepath.Join(dataDir, "wadad.db"), "daemon", daemonMigrations)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// ServerID returns the stable daemon identifier, creating one on first run.
func (s *Store) ServerID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM daemon_meta WHERE key = 'server_id'`).Scan(&id)
	if err == nil && id != "" {
		return id, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id = "srv_" + hex.EncodeToString(raw[:])
	if _, err := s.db.ExecContext(ctx, `INSERT INTO daemon_meta (key, value) VALUES ('server_id', ?) ON CONFLICT(key) DO NOTHING`, id); err != nil {
		return "", err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM daemon_meta WHERE key = 'server_id'`).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// NextEpoch increments and returns the daemon's monotonic startup epoch.
func (s *Store) NextEpoch(ctx context.Context) (uint64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	var raw string
	var epoch uint64
	err = tx.QueryRowContext(ctx, `SELECT value FROM daemon_meta WHERE key = 'epoch'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return 0, err
	}
	if raw != "" {
		epoch, _ = strconv.ParseUint(raw, 10, 64)
	}
	epoch++
	if _, err := tx.ExecContext(ctx, `INSERT INTO daemon_meta (key, value) VALUES ('epoch', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.FormatUint(epoch, 10)); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return epoch, nil
}

// MarkPendingDeliveriesUnknown transitions any in-flight pending prompt deliveries
// left behind by a previous daemon crash/restart to outcome unknown.
func (s *Store) MarkPendingDeliveriesUnknown(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE delivery_receipts SET status = ? WHERE status = ?`, DeliveryUnknown, DeliveryPending)
	return err
}

func (s *Store) GetCreationReceipt(ctx context.Context, key string) (CreationReceipt, bool, error) {
	var r CreationReceipt
	err := s.db.QueryRowContext(ctx, `SELECT idempotency_key, kind, resource_id, request_hash, created_at FROM creation_receipts WHERE idempotency_key = ?`, key).
		Scan(&r.IdempotencyKey, &r.Kind, &r.ResourceID, &r.RequestHash, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CreationReceipt{}, false, nil
	}
	if err != nil {
		return CreationReceipt{}, false, err
	}
	return r, true, nil
}

// ReserveCreationReceipt records r with no ResourceID before the resource is
// created, unless its key already has a receipt. It reports whether r was
// stored and otherwise returns the existing receipt; an empty ResourceID there
// means that creation is still running or its outcome is unknown.
func (s *Store) ReserveCreationReceipt(ctx context.Context, r CreationReceipt) (bool, CreationReceipt, error) {
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().UnixMilli()
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO creation_receipts (idempotency_key, kind, resource_id, request_hash, created_at) VALUES (?, ?, '', ?, ?)
		 ON CONFLICT(idempotency_key) DO NOTHING`,
		r.IdempotencyKey, r.Kind, r.RequestHash, r.CreatedAt)
	if err != nil {
		return false, CreationReceipt{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, CreationReceipt{}, err
	} else if n == 1 {
		return true, CreationReceipt{}, nil
	}
	existing, ok, err := s.GetCreationReceipt(ctx, r.IdempotencyKey)
	if err != nil {
		return false, CreationReceipt{}, err
	}
	if !ok {
		return false, CreationReceipt{}, errors.New("store: creation receipt vanished")
	}
	return false, existing, nil
}

// DeleteCreationReceipt removes a reservation whose creation failed.
func (s *Store) DeleteCreationReceipt(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM creation_receipts WHERE idempotency_key = ? AND resource_id = ''`, key)
	return err
}

func (s *Store) PutCreationReceipt(ctx context.Context, r CreationReceipt) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO creation_receipts (idempotency_key, kind, resource_id, request_hash, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(idempotency_key) DO UPDATE SET resource_id = excluded.resource_id`,
		r.IdempotencyKey, r.Kind, r.ResourceID, r.RequestHash, r.CreatedAt)
	return err
}

func (s *Store) GetDeliveryReceipt(ctx context.Context, sessionID, messageID string) (DeliveryReceipt, bool, error) {
	var r DeliveryReceipt
	err := s.db.QueryRowContext(ctx, `SELECT session_id, message_id, request_hash, status, created_at FROM delivery_receipts WHERE session_id = ? AND message_id = ?`, sessionID, messageID).
		Scan(&r.SessionID, &r.MessageID, &r.RequestHash, &r.Status, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DeliveryReceipt{}, false, nil
	}
	if err != nil {
		return DeliveryReceipt{}, false, err
	}
	return r, true, nil
}

func (s *Store) PutDeliveryReceipt(ctx context.Context, r DeliveryReceipt) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO delivery_receipts (session_id, message_id, request_hash, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		r.SessionID, r.MessageID, r.RequestHash, r.Status, r.CreatedAt)
	return err
}

func (s *Store) UpdateDeliveryReceipt(ctx context.Context, sessionID, messageID, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE delivery_receipts SET status = ? WHERE session_id = ? AND message_id = ?`, status, sessionID, messageID)
	return err
}

func (s *Store) AppendEvent(ctx context.Context, ev protocol.Event) error {
	if ev.Session == "" {
		return nil
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO timeline_events (session_id, epoch, seq, payload) VALUES (?, ?, ?, ?)
		 ON CONFLICT(session_id, epoch, seq) DO UPDATE SET payload = excluded.payload`,
		ev.Session, ev.Epoch, ev.Seq, string(raw))
	return err
}

// MaxSessionTimeline is how many of a session's most recent events in one
// epoch the timeline keeps for catch-up. Transcripts hold the full history.
const MaxSessionTimeline = 20000

// PruneTimeline deletes the events of epochs before the one preceding
// current: a client that missed more than the last daemon run reloads the
// conversation instead of replaying it.
func (s *Store) PruneTimeline(ctx context.Context, current uint64) error {
	if current < 2 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM timeline_events WHERE epoch < ?`, current-1)
	return err
}

// TrimSessionTimeline keeps only the last MaxSessionTimeline events of
// sessionID in epoch, counting back from seq.
func (s *Store) TrimSessionTimeline(ctx context.Context, sessionID string, epoch, seq uint64) error {
	if seq <= MaxSessionTimeline {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM timeline_events WHERE session_id = ? AND epoch = ? AND seq <= ?`,
		sessionID, epoch, seq-MaxSessionTimeline)
	return err
}

// DeleteSessionTimeline removes every event of sessionID.
func (s *Store) DeleteSessionTimeline(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM timeline_events WHERE session_id = ?`, sessionID)
	return err
}

// FetchTimeline returns sequenced events for sessionID after (epoch, afterSeq).
// If epoch == 0, it fetches from the latest stored epoch or all epochs cleanly
// in (epoch ASC, seq ASC) order after afterSeq within the target epoch.
// Retention removes old events, so a first event whose Seq is past
// afterSeq+1 means the caller missed some and should reload the conversation.
func (s *Store) FetchTimeline(ctx context.Context, sessionID string, epoch, afterSeq uint64, limit int) (events []protocol.Event, currentEpoch uint64, hasMore bool, err error) {
	if limit <= 0 {
		limit = 200
	}
	var rows *sql.Rows
	if epoch > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT payload FROM timeline_events WHERE session_id = ? AND ((epoch = ? AND seq > ?) OR epoch > ?) ORDER BY epoch ASC, seq ASC LIMIT ?`,
			sessionID, epoch, afterSeq, epoch, limit+1)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT payload FROM timeline_events WHERE session_id = ? AND seq > ? ORDER BY epoch ASC, seq ASC LIMIT ?`,
			sessionID, afterSeq, limit+1)
	}
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, false, err
		}
		var ev protocol.Event
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil, 0, false, err
		}
		if len(events) == limit {
			hasMore = true
			break
		}
		currentEpoch = ev.Epoch
		events = append(events, ev)
	}
	return events, currentEpoch, hasMore, rows.Err()
}

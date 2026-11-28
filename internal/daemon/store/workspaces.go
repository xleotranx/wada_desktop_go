package store

import (
	"context"
	"encoding/json"

	"wada-desktop-go/internal/protocol"
	dbstore "wada-desktop-go/internal/store"
)

// ProjectRow is a stored project. Name is the user's name; empty uses the
// root's base name.
type ProjectRow struct {
	ID       string
	Root     string
	Name     string
	Created  int64
	Archived int64 // 0 while active
	// TrustedConfig is the hash of the wada.json the user allowed to run.
	TrustedConfig string
}

// WorkspaceRow is a stored workspace.
type WorkspaceRow struct {
	ID           string
	Project      string
	Isolation    protocol.Isolation
	Dir          string
	WorktreeRoot string
	Owned        bool
	Branch       string
	BaseRef      string
	PR           int
	Title        string
	Created      int64
	Archived     int64 // 0 while active
	PortBase     int   // the first of its ports; 0 until it needs them
	// AutoName is set while its branch and title wait to be named from the
	// first message sent in it.
	AutoName bool
}

// Projects reads every project, archived ones included.
func (s *Store) Projects(ctx context.Context) ([]ProjectRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, root, name, created, archived, trusted_config FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectRow
	for rows.Next() {
		var p ProjectRow
		if err := rows.Scan(&p.ID, &p.Root, &p.Name, &p.Created, &p.Archived, &p.TrustedConfig); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Workspaces reads every workspace, archived ones included.
func (s *Store) Workspaces(ctx context.Context) ([]WorkspaceRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project, isolation, dir, worktree_root, owned, branch, base_ref, pr,
		title, created, archived, port_base, auto_name FROM workspaces`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceRow
	for rows.Next() {
		var w WorkspaceRow
		var iso string
		if err := rows.Scan(&w.ID, &w.Project, &iso, &w.Dir, &w.WorktreeRoot, &w.Owned, &w.Branch, &w.BaseRef, &w.PR,
			&w.Title, &w.Created, &w.Archived, &w.PortBase, &w.AutoName); err != nil {
			return nil, err
		}
		w.Isolation = protocol.Isolation(iso)
		out = append(out, w)
	}
	return out, rows.Err()
}

// PutProject writes projects and workspaces, inserting or replacing each, in
// one transaction.
func (s *Store) PutProject(ctx context.Context, projects []ProjectRow, workspaces []WorkspaceRow) error {
	var stmts []dbstore.Statement
	for _, p := range projects {
		stmts = append(stmts, dbstore.Statement{
			SQL: `INSERT INTO projects (id, root, name, created, archived, trusted_config) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET name = excluded.name, archived = excluded.archived,
					trusted_config = excluded.trusted_config`,
			Args: []any{p.ID, p.Root, p.Name, p.Created, p.Archived, p.TrustedConfig}})
	}
	for _, w := range workspaces {
		stmts = append(stmts, dbstore.Statement{
			SQL: `INSERT INTO workspaces (id, project, isolation, dir, worktree_root, owned, branch, base_ref, pr,
					title, created, archived, port_base, auto_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET branch = excluded.branch, title = excluded.title, archived = excluded.archived,
					port_base = excluded.port_base, auto_name = excluded.auto_name, pr = excluded.pr`,
			Args: []any{w.ID, w.Project, string(w.Isolation), w.Dir, w.WorktreeRoot, w.Owned, w.Branch, w.BaseRef, w.PR,
				w.Title, w.Created, w.Archived, w.PortBase, w.AutoName}})
	}
	return dbstore.ExecTx(ctx, s.db, stmts)
}

// Schedules reads every schedule.
func (s *Store) Schedules(ctx context.Context) ([]protocol.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM schedules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Schedule
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var sc protocol.Schedule
		if err := json.Unmarshal([]byte(raw), &sc); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// PutSchedule adds or replaces a schedule.
func (s *Store) PutSchedule(ctx context.Context, sc protocol.Schedule) error {
	raw, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO schedules (id, data) VALUES (?, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data`, sc.ID, string(raw))
	return err
}

// DeleteSchedule removes a schedule.
func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id)
	return err
}

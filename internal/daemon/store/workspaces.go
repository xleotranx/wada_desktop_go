package store

import (
	"context"

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
}

// WorkspaceRow is a stored workspace.
type WorkspaceRow struct {
	ID        string
	Project   string
	Isolation protocol.Isolation
	Dir       string
	Title     string
	Created   int64
	Archived  int64 // 0 while active
}

// Projects reads every project, archived ones included.
func (s *Store) Projects(ctx context.Context) ([]ProjectRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, root, name, created, archived FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectRow
	for rows.Next() {
		var p ProjectRow
		if err := rows.Scan(&p.ID, &p.Root, &p.Name, &p.Created, &p.Archived); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Workspaces reads every workspace, archived ones included.
func (s *Store) Workspaces(ctx context.Context) ([]WorkspaceRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, project, isolation, dir, title, created, archived FROM workspaces`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceRow
	for rows.Next() {
		var w WorkspaceRow
		var iso string
		if err := rows.Scan(&w.ID, &w.Project, &iso, &w.Dir, &w.Title, &w.Created, &w.Archived); err != nil {
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
			SQL: `INSERT INTO projects (id, root, name, created, archived) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET name = excluded.name, archived = excluded.archived`,
			Args: []any{p.ID, p.Root, p.Name, p.Created, p.Archived}})
	}
	for _, w := range workspaces {
		stmts = append(stmts, dbstore.Statement{
			SQL: `INSERT INTO workspaces (id, project, isolation, dir, title, created, archived) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (id) DO UPDATE SET title = excluded.title, archived = excluded.archived`,
			Args: []any{w.ID, w.Project, string(w.Isolation), w.Dir, w.Title, w.Created, w.Archived}})
	}
	return dbstore.ExecTx(ctx, s.db, stmts)
}

package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kuchmenko/workspace/internal/config"
	_ "modernc.org/sqlite"
)

var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrStaleRevision     = errors.New("workspace revision changed")
)

type Workspace struct {
	Name     string
	Root     string
	Revision int64
	State    *config.Workspace
}

type Store struct {
	db   *sql.DB
	path string
}

func Open(path string) (*Store, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if _, err = database.Exec(`PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS workspaces (
 name TEXT PRIMARY KEY,
 root TEXT NOT NULL UNIQUE,
 revision INTEGER NOT NULL,
 registry BLOB NOT NULL
);`); err != nil {
		_ = database.Close()
		return nil, err
	}
	store := &Store{db: database, path: path}
	if err = store.restrict(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func OpenDefault() (*Store, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return Open(path)
}

func Exists() (bool, error) {
	path, err := DefaultPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) Create(ctx context.Context, name, root string, state *config.Workspace) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, errors.New("workspace name is required")
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return Workspace{}, err
	}
	if _, err = store.db.ExecContext(ctx, `INSERT INTO workspaces(name,root,revision,registry) VALUES(?,?,1,?)`, name, canonical, body); err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	state.RestoreRoot(canonical)
	return Workspace{Name: name, Root: canonical, Revision: 1, State: state}, nil
}

func (store *Store) LoadByName(ctx context.Context, name string) (Workspace, error) {
	return scanWorkspace(store.db.QueryRowContext(ctx, `SELECT name,root,revision,registry FROM workspaces WHERE name=?`, name))
}

func (store *Store) LoadByRoot(ctx context.Context, root string) (Workspace, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	workspace, err := scanWorkspace(store.db.QueryRowContext(ctx, `SELECT name,root,revision,registry FROM workspaces WHERE root=?`, canonical))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, fmt.Errorf("%w for root %q", ErrWorkspaceNotFound, canonical)
	}
	return workspace, err
}

func (store *Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT name,root,revision,registry FROM workspaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var workspaces []Workspace
	for rows.Next() {
		workspace, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	return workspaces, rows.Err()
}

func (store *Store) Find(ctx context.Context, path string) (Workspace, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Workspace{}, err
	}
	workspaces, err := store.List(ctx)
	if err != nil {
		return Workspace{}, err
	}
	var found Workspace
	for _, candidate := range workspaces {
		relative, relErr := filepath.Rel(candidate.Root, absolute)
		outside := relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
		if relErr == nil && !outside && !filepath.IsAbs(relative) && (found.Root == "" || len(candidate.Root) > len(found.Root)) {
			found = candidate
		}
	}
	if found.Root == "" {
		return Workspace{}, ErrWorkspaceNotFound
	}
	return found, nil
}

func (store *Store) Update(ctx context.Context, name string, expectedRevision int64, state *config.Workspace) (Workspace, error) {
	body, err := config.EncodeWorkspace(state)
	if err != nil {
		return Workspace{}, err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE workspaces SET registry=?,revision=revision+1 WHERE name=? AND revision=?`, body, name, expectedRevision)
	if err != nil {
		return Workspace{}, fmt.Errorf("update workspace: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Workspace{}, err
	}
	if affected != 1 {
		var exists int
		if queryErr := store.db.QueryRowContext(ctx, `SELECT 1 FROM workspaces WHERE name=?`, name).Scan(&exists); errors.Is(queryErr, sql.ErrNoRows) {
			return Workspace{}, fmt.Errorf("%w: %q", ErrWorkspaceNotFound, name)
		} else if queryErr != nil {
			return Workspace{}, queryErr
		}
		return Workspace{}, ErrStaleRevision
	}
	return store.LoadByName(ctx, name)
}

func (store *Store) Mutate(ctx context.Context, root string, mutate func(*config.Workspace) error) (Workspace, error) {
	workspace, err := store.LoadByRoot(ctx, root)
	if err != nil {
		return Workspace{}, err
	}
	if err = mutate(workspace.State); err != nil {
		return Workspace{}, err
	}
	return store.Update(ctx, workspace.Name, workspace.Revision, workspace.State)
}

type scanner interface {
	Scan(...any) error
}

func scanWorkspace(row scanner) (Workspace, error) {
	var workspace Workspace
	var body []byte
	if err := row.Scan(&workspace.Name, &workspace.Root, &workspace.Revision, &body); err != nil {
		return workspace, err
	}
	state, err := config.DecodeStoredWorkspace(body)
	if err != nil {
		return workspace, err
	}
	state.RestoreRoot(workspace.Root)
	workspace.State = state
	return workspace, nil
}

func (store *Store) restrict() error {
	for _, candidate := range []string{store.path, store.path + "-wal", store.path + "-shm"} {
		if err := os.Chmod(candidate, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace root must be a directory")
	}
	return filepath.Clean(resolved), nil
}

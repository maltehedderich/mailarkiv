package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// schemaVersion is the index schema this server's search program writes.
const schemaVersion = 1

const (
	roleActive   = "active"
	roleBuilding = "building"
)

// generation is one index: a search_generation row and its directory,
// index/<id>.
type generation struct {
	id     int64
	role   string
	cursor int64
}

// Prepare creates DataDir/index with mode 0700 and removes everything in it
// that no search_generation row names. It runs before the search process
// starts, so nothing has those directories open.
func Prepare(ctx context.Context, dataDir string, db *sqlitedb.DB) error {
	index := filepath.Join(dataDir, "index")
	if err := os.Mkdir(index, 0o700); err == nil {
		// Mkdir applies the umask, so set the mode it asked for.
		if err := os.Chmod(index, 0o700); err != nil {
			return fmt.Errorf("prepare the index directory: %w", err)
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("prepare the index directory: %w", err)
	}

	named := make(map[string]bool)
	rows, err := db.Reader.QueryContext(ctx, "SELECT id FROM search_generation")
	if err != nil {
		return fmt.Errorf("read the search generations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("read the search generations: %w", err)
		}
		named[strconv.FormatInt(id, 10)] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read the search generations: %w", err)
	}

	entries, err := os.ReadDir(index)
	if err != nil {
		return fmt.Errorf("prepare the index directory: %w", err)
	}
	for _, entry := range entries {
		if named[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(index, entry.Name())); err != nil {
			return fmt.Errorf("remove an old index: %w", err)
		}
	}
	return nil
}

// loadGenerations returns the generation rows. With none, it first inserts
// a building row with no rebuild.
func (m *Module) loadGenerations(ctx context.Context) ([]generation, error) {
	var generations []generation
	err := sqlitedb.InTx(ctx, m.config.DB.Writer, func(tx *sql.Tx) error {
		var err error
		if generations, err = readGenerations(ctx, tx); err != nil || len(generations) > 0 {
			return err
		}
		created, err := m.insertBuilding(ctx, tx)
		generations = []generation{created}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("load the search generations: %w", err)
	}
	return generations, nil
}

// replaceGenerations deletes every row and inserts a building one, for an
// active index that is gone. Prepare removes the old directories at the next
// start.
func (m *Module) replaceGenerations(ctx context.Context) (generation, error) {
	var created generation
	err := sqlitedb.InTx(ctx, m.config.DB.Writer, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM search_generation"); err != nil {
			return err
		}
		var err error
		created, err = m.insertBuilding(ctx, tx)
		return err
	})
	if err != nil {
		return generation{}, fmt.Errorf("start a new index: %w", err)
	}
	return created, nil
}

func (m *Module) insertBuilding(ctx context.Context, tx *sql.Tx) (generation, error) {
	result, err := tx.ExecContext(ctx,
		"INSERT INTO search_generation (role, schema_version, cursor, created_at) VALUES (?, ?, 0, ?)",
		roleBuilding, schemaVersion, m.config.Now().UnixMilli())
	if err != nil {
		return generation{}, err
	}
	id, err := result.LastInsertId()
	return generation{id: id, role: roleBuilding}, err
}

func readGenerations(ctx context.Context, tx *sql.Tx) ([]generation, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, role, cursor FROM search_generation ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var generations []generation
	for rows.Next() {
		var g generation
		if err := rows.Scan(&g.id, &g.role, &g.cursor); err != nil {
			return nil, err
		}
		generations = append(generations, g)
	}
	return generations, rows.Err()
}

// saveCursor stores the highest processed_seq a commit saved in g.
func (m *Module) saveCursor(ctx context.Context, g *generation, cursor int64) error {
	if _, err := m.config.DB.Writer.ExecContext(ctx,
		"UPDATE search_generation SET cursor = ? WHERE id = ?", cursor, g.id); err != nil {
		return fmt.Errorf("save the index cursor: %w", err)
	}
	g.cursor = cursor
	return nil
}

// activate makes the building generation g the active one. The old active
// index stays open until the search process exits, and Prepare removes its
// directory at the next start.
func (m *Module) activate(ctx context.Context, g *generation) error {
	err := sqlitedb.InTx(ctx, m.config.DB.Writer, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM search_generation WHERE role = ?", roleActive); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE search_generation SET role = ? WHERE id = ?", roleActive, g.id)
		return err
	})
	if err != nil {
		return fmt.Errorf("activate the new index: %w", err)
	}
	g.role = roleActive
	return nil
}

// storedCursor reads generation id's cursor, which is 0 once its row is gone.
func (m *Module) storedCursor(ctx context.Context, id int64) (int64, error) {
	var cursor int64
	err := m.config.DB.Reader.QueryRowContext(ctx, "SELECT cursor FROM search_generation WHERE id = ?", id).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read the index cursor: %w", err)
	}
	return cursor, nil
}

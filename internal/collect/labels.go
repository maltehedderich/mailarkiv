package collect

import (
	"context"
	"fmt"
	"strings"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// Labeler names the folders that collection entries point to, and the
// sources of message events. It works without a collector, so both show even
// when serve doesn't collect.
type Labeler struct{ db *sqlitedb.DB }

func NewLabeler(db *sqlitedb.DB) *Labeler { return &Labeler{db: db} }

// SourceLabel names a source. A removed source keeps its labels.
type SourceLabel struct {
	ID       int64
	Name     string
	Host     string
	UserName string
	Removed  bool
}

// Label names a folder and its source.
type Label struct {
	Folder string
	Source SourceLabel
}

// Labels returns the label of each folder ID that exists, in one query.
func (l *Labeler) Labels(ctx context.Context, folderIDs []int64) (map[int64]Label, error) {
	labels := make(map[int64]Label, len(folderIDs))
	if len(folderIDs) == 0 {
		return labels, nil
	}
	args := make([]any, len(folderIDs))
	for i, id := range folderIDs {
		args[i] = id
	}
	rows, err := l.db.Reader.QueryContext(ctx, `
SELECT f.id, f.name, s.id, s.name, s.host, s.user_name, s.removed_at IS NOT NULL
FROM imap_folder f
JOIN imap_source s ON s.id = f.source_id
WHERE f.id IN (?`+strings.Repeat(", ?", len(folderIDs)-1)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("read folder labels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var label Label
		if err := rows.Scan(&id, &label.Folder, &label.Source.ID, &label.Source.Name, &label.Source.Host,
			&label.Source.UserName, &label.Source.Removed); err != nil {
			return nil, fmt.Errorf("read folder labels: %w", err)
		}
		labels[id] = label
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read folder labels: %w", err)
	}
	return labels, nil
}

// SourceNames returns the name of each source ID that exists, removed or
// not, in one query.
func (l *Labeler) SourceNames(ctx context.Context, sourceIDs []int64) (map[int64]string, error) {
	names := make(map[int64]string, len(sourceIDs))
	if len(sourceIDs) == 0 {
		return names, nil
	}
	args := make([]any, len(sourceIDs))
	for i, id := range sourceIDs {
		args[i] = id
	}
	rows, err := l.db.Reader.QueryContext(ctx,
		"SELECT id, name FROM imap_source WHERE id IN (?"+strings.Repeat(", ?", len(sourceIDs)-1)+")", args...)
	if err != nil {
		return nil, fmt.Errorf("read source names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("read source names: %w", err)
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read source names: %w", err)
	}
	return names, nil
}

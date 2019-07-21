package db

import (
	"context"

	"github.com/pkg/errors"
)

var schema = []string{
	`CREATE TABLE key_value	(
           name INTEGER,
           value BLOB,
           create_revision INTEGER NOT NULL,
           revision INTEGER NOT NULL,
           ttl INTEGER,
           version INTEGER,
           del INTEGER,
           old_value BLOB,
           id INTEGER primary key autoincrement,
           old_revision INTEGER)`,
	`CREATE INDEX name_idx ON key_value (name)`,
	`CREATE INDEX revision_idx ON key_value (revision)`,
	`CREATE INDEX name_del_revision_idx ON key_value (name, del, revision)`,
	`CREATE TABLE revision (
           id INTEGER PRIMARY KEY AUTOINCREMENT,
           t TEXT)`,
	`INSERT INTO revision(id, t) VALUES(1, NULL)`, // Initial revision will be 0 (1 - 1)
	`CREATE TRIGGER key_value_revision
           AFTER INSERT ON key_value
           FOR EACH ROW
           WHEN NEW.id IS NOT NULL
           BEGIN
             DELETE FROM revision;
             INSERT INTO revision(t) VALUES(NULL);
           END`,
}

// CreateSchema initializes the database schema.
func (d *DB) CreateSchema(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := d.db.ExecContext(ctx, stmt); err != nil {
			return errors.Wrap(err, "create schema")
		}
	}
	return nil
}

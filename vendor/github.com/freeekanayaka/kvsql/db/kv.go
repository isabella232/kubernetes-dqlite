package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
)

var (
	ErrExists        = fmt.Errorf("key exists")
	ErrNotExists     = fmt.Errorf("key and or Revision does not exists")
	ErrRevisionMatch = fmt.Errorf("revision does not match")
)

type KeyValue struct {
	ID             int64
	Key            string
	Value          []byte
	OldValue       []byte
	OldRevision    int64
	CreateRevision int64
	Revision       int64
	TTL            int64
	Version        int64
	Del            int64
}

var (
	fieldList = "name, value, old_value, old_revision, create_revision, revision, ttl, version, del"
	baseList  = `
SELECT kv.id, kv.name, kv.value, kv.old_value, kv.old_revision, kv.create_revision, kv.revision, kv.ttl, kv.version, kv.del
FROM key_value kv
  INNER JOIN
    (
      SELECT MAX(revision) revision, kvi.name
      FROM key_value kvi
		%REV%
        GROUP BY kvi.name
    ) AS r
    ON r.name = kv.name AND r.revision = kv.revision
WHERE kv.name like ? %RES% ORDER BY kv.name ASC limit ?
`
	cleanupSQL      = "DELETE FROM key_value WHERE ttl > 0 AND ttl < ?"
	getSQL          = "SELECT id, " + fieldList + " FROM key_value WHERE name = ? ORDER BY revision DESC limit ?"
	listSQL         = strings.Replace(strings.Replace(baseList, "%REV%", "", -1), "%RES%", "", -1)
	listRevisionSQL = strings.Replace(strings.Replace(baseList, "%REV%", "WHERE kvi.revision >= ?", -1), "%RES%", "", -1)
	listResumeSQL   = strings.Replace(strings.Replace(baseList, "%REV%", "WHERE kvi.revision <= ?", -1),
		"%RES%", "and kv.name > ? ", -1)
	insertSQL = `
INSERT INTO key_value(` + fieldList + `)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	replaySQL    = "SELECT id, " + fieldList + " FROM key_value WHERE name like ? and revision >= ? ORDER BY revision ASC"
	toDeleteSQL  = "SELECT count(*) c, name, max(revision) FROM key_value GROUP BY name HAVING c > 1 or (c = 1 and del = 1)"
	deleteOldSQL = "DELETE FROM key_value WHERE name = ? AND (revision < ? OR (revision = ? AND del = 1))"
	createSQL    = `
	INSERT INTO key_value(id, ` + fieldList + `)
          VALUES ((SELECT id FROM revision), ?, ?, NULL, 0,
            CASE
              WHEN (
                CASE
                  WHEN (SELECT revision FROM key_value where name=? AND del=1 UNION ALL SELECT 0 AS revision ORDER BY revision DESC LIMIT 1) = 0
                    THEN (SELECT revision FROM key_value WHERE name=? AND del=0 UNION ALL SELECT 0 AS revision ORDER BY revision DESC LIMIT 1)
                    ELSE (SELECT revision FROM key_value WHERE name=? AND del=0 AND revision > (SELECT max(revision) FROM key_value where name=? AND del=1) UNION ALL SELECT 0 AS revision ORDER BY revision DESC LIMIT 1)
                  END
                ) = 0
                THEN (SELECT id FROM revision)
                ELSE NULL
              END,
             (SELECT id FROM revision), ?, 1, 0)`
)

func (d *DB) List(ctx context.Context, revision, limit int64, rangeKey, startKey string) ([]*KeyValue, int64, error) {

	var resp []*KeyValue
	var listRevision int64
	var err error

	err = d.tx(func(tx *sql.Tx) error {
		resp, listRevision, err = listTx(ctx, tx, revision, limit, rangeKey, startKey)
		return err
	})
	if err != nil {
		return nil, 0, err
	}

	return resp, listRevision, nil
}

func listTx(ctx context.Context, tx *sql.Tx, revision, limit int64, rangeKey, startKey string) ([]*KeyValue, int64, error) {
	if limit == 0 {
		limit = 1000000
	} else {
		limit = limit + 1
	}

	var resp []*KeyValue
	listRevision, err := currentRevision(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	query := ""
	args := []interface{}{}
	if !strings.HasSuffix(rangeKey, "%") && revision <= 0 {
		query = getSQL
		args = append(args, rangeKey, 1)
	} else if revision <= 0 {
		query = listSQL
		args = append(args, rangeKey, limit)
	} else if len(startKey) > 0 {
		listRevision = revision
		query = listResumeSQL
		args = append(args, revision, rangeKey, startKey, limit)
	} else {
		query = listRevisionSQL
		args = append(args, revision, rangeKey, limit)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		value := KeyValue{}
		if err := scan(rows.Scan, &value); err != nil {
			return nil, 0, err
		}
		if value.Revision > listRevision {
			listRevision = value.Revision
		}
		if value.Del == 0 {
			resp = append(resp, &value)
		}
	}

	return resp, listRevision, nil
}

func (d *DB) Get(ctx context.Context, key string) (*KeyValue, error) {
	var kv *KeyValue
	err := d.tx(func(tx *sql.Tx) error {
		kvs, _, err := d.List(ctx, 0, 1, key, "")
		if err != nil {
			return err
		}
		if len(kvs) > 0 {
			kv = kvs[0]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return kv, nil
}

func getTx(ctx context.Context, tx *sql.Tx, key string) (*KeyValue, error) {
	kvs, _, err := listTx(ctx, tx, 0, 1, key, "")
	if err != nil {
		return nil, err
	}
	if len(kvs) > 0 {
		return kvs[0], nil
	}
	return nil, nil
}

var ErrKeyExists = fmt.Errorf("key already exists")

func (d *DB) Create(ctx context.Context, key string, value []byte, ttl int64) (*KeyValue, error) {
	if ttl > 0 {
		ttl = int64(time.Now().Unix()) + ttl
	}
	var result *KeyValue

	if d.createStmt == nil {
		stmt, err := d.db.Prepare(createSQL)
		if err != nil {
			return nil, errors.Wrap(err, "prepare create statement")
		}
		d.createStmt = stmt
	}

	err := retry(func() error {
		result = &KeyValue{
			Key:     key,
			Value:   value,
			TTL:     int64(ttl),
			Version: 1,
		}

		r, err := d.createStmt.ExecContext(ctx,
			result.Key,
			result.Value,
			result.Key,
			result.Key,
			result.Key,
			result.Key,
			result.TTL,
		)
		if err != nil {
			if strings.Contains(err.Error(), "NOT NULL constraint failed: key_value.create_revision") {
				return ErrKeyExists
			}
			return err
		}
		id, err := r.LastInsertId()
		if err != nil {
			return err
		}
		result.Revision = id
		result.CreateRevision = result.Revision

		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (d *DB) Mod(ctx context.Context, delete bool, key string, value []byte, revision int64, ttl int64) (*KeyValue, error) {
	var result *KeyValue
	err := d.tx(func(tx *sql.Tx) error {
		oldKv, err := getTx(ctx, tx, key)
		if err != nil {
			return err
		}

		if revision > 0 && oldKv == nil {
			return ErrNotExists
		}

		if revision > 0 && oldKv.Revision != revision {
			return ErrRevisionMatch
		}

		if ttl > 0 {
			ttl = int64(time.Now().Unix()) + ttl
		}

		newRevision, err := newRevision(ctx, tx)
		if err != nil {
			return err
		}
		result = &KeyValue{
			Key:            key,
			Value:          value,
			Revision:       newRevision,
			TTL:            int64(ttl),
			CreateRevision: newRevision,
			Version:        1,
		}
		if oldKv != nil {
			result.OldRevision = oldKv.Revision
			result.OldValue = oldKv.Value
			result.TTL = oldKv.TTL
			result.CreateRevision = oldKv.CreateRevision
			result.Version = oldKv.Version + 1
		}

		if delete {
			result.Del = 1
		}
		_, err = tx.ExecContext(ctx, insertSQL,
			result.Key,
			result.Value,
			result.OldValue,
			result.OldRevision,
			result.CreateRevision,
			result.Revision,
			result.TTL,
			result.Version,
			result.Del,
		)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (d *DB) Cleanup(ctx context.Context) error {
	return d.tx(func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, cleanupSQL, time.Now().Unix()); err != nil {
			return err
		}

		rows, err := tx.QueryContext(ctx, toDeleteSQL)
		if err != nil {
			return err
		}
		defer rows.Close()

		toDelete := map[string]int64{}
		for rows.Next() {
			var (
				count, revision int64
				name            string
			)
			err := rows.Scan(&count, &name, &revision)
			if err != nil {
				return err
			}
			toDelete[name] = revision
		}

		for name, rev := range toDelete {
			_, err = tx.ExecContext(ctx, deleteOldSQL, name, rev, rev)
			if err != nil {
				return err
			}
		}

		return nil
	})

	return nil
}

func (d *DB) Replay(ctx context.Context, key string, revision int64) ([]*KeyValue, error) {
	var resp []*KeyValue
	err := d.tx(func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, replaySQL, key, revision)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			value := KeyValue{}
			if err := scan(rows.Scan, &value); err != nil {
				return err
			}
			resp = append(resp, &value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// Bump the current revision. This is used at startup just to make sure a
// commit is made and the raft log has been applied.
func (d *DB) Bump(ctx context.Context) error {
	return d.tx(func(tx *sql.Tx) error {
		_, err := newRevision(ctx, tx)
		if err != nil {
			return errors.Wrap(err, "bump current revision")
		}
		return nil
	})
}

func currentRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	row := tx.QueryRowContext(ctx, "SELECT id FROM revision")
	rev := sql.NullInt64{}
	if err := row.Scan(&rev); err != nil {
		return 0, errors.Wrap(err, "get current revision")
	}
	if rev.Int64 == 0 {
		panic("current revision is 0")
	}
	return rev.Int64 - 1, nil
}

func newRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx, "DELETE FROM revision"); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO revision(t) VALUES(NULL)")
	if err != nil {
		return 0, err
	}
	revision, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return revision, nil
}

type scanner func(dest ...interface{}) error

func scan(s scanner, out *KeyValue) error {
	return s(
		&out.ID,
		&out.Key,
		&out.Value,
		&out.OldValue,
		&out.OldRevision,
		&out.CreateRevision,
		&out.Revision,
		&out.TTL,
		&out.Version,
		&out.Del)
}

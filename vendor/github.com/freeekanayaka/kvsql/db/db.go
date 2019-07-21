package db

import (
	"database/sql"
	"strings"
	"time"

	dqlitedriver "github.com/canonical/go-dqlite/driver"
	"github.com/pkg/errors"
)

type DB struct {
	db         *sql.DB
	createStmt *sql.Stmt
}

func (d *DB) DB() *sql.DB {
	return d.db
}

func Open(driver string, dsn string) (*DB, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, errors.Wrap(err, "open cluster database")
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for i := 0; i < 30; i++ {
		err = db.Ping()
		if err == nil {
			break
		}
		cause := errors.Cause(err)
		if cause != dqlitedriver.ErrNoAvailableLeader {
			return nil, errors.Wrap(err, "ping database")
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, errors.Wrap(err, "wait for database to be ready")
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	if d.createStmt != nil {
		if err := d.createStmt.Close(); err != nil {
			return errors.Wrap(err, "close create statement")
		}
	}
	if err := d.db.Close(); err != nil {
		return errors.Wrap(err, "close cluster database")
	}
	return nil
}

// Executes the given function within a database transaction.
func (d *DB) tx(f func(*sql.Tx) error) error {
	return retry(func() error {
		tx, err := d.db.Begin()
		if err != nil {
			return errors.Wrap(err, "failed to begin transaction")
		}

		err = f(tx)
		if err != nil {
			return rollback(tx, err)
		}

		err = tx.Commit()
		if err == sql.ErrTxDone {
			err = nil // Ignore duplicate commits/rollbacks
		}
		return err
	})
}

// Retry transient db errors.
func retry(f func() error) error {
	var err error
	for i := 0; i < 250; i++ {
		if err = f(); err != nil {
			if strings.Contains(err.Error(), "database is locked") {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return err
		}
		break
	}
	return err
}

// Rollback a transaction after the given error occurred. If the rollback
// succeeds the given error is returned, otherwise a new error that wraps it
// gets generated and returned.
func rollback(tx *sql.Tx, reason error) error {
	err := tx.Rollback()
	if err != nil {
		// TODO logger the error
	}
	return reason
}

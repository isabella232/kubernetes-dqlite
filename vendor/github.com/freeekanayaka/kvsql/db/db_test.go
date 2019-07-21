package db_test

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/freeekanayaka/kvsql/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen(t *testing.T) {
	db, err := db.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

func TestCreateSchema(t *testing.T) {
	db, err := db.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	assert.NoError(t, db.CreateSchema(context.Background()))
}

func TestCreate_KeyExists(t *testing.T) {
	db, err := db.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	assert.NoError(t, db.CreateSchema(ctx))

	_, err = db.Create(ctx, "foo", []byte{1, 2, 3}, 0)
	require.NoError(t, err)

	_, err = db.Create(ctx, "foo", []byte{1, 2, 3}, 0)
	assert.EqualError(t, err, "key already exists")
}

func TestCreate_AgainAfterDelete(t *testing.T) {
	db, err := db.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	assert.NoError(t, db.CreateSchema(ctx))

	kv, err := db.Create(ctx, "foo", []byte{1, 2, 3}, 0)
	require.NoError(t, err)

	_, err = db.Mod(ctx, true, "foo", []byte{1, 2, 3}, kv.Revision, 0)
	require.NoError(t, err)

	_, err = db.Create(ctx, "foo", []byte{1, 2, 3}, 0)
	assert.NoError(t, err)
}

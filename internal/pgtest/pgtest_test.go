package pgtest_test

import (
	"context"
	"testing"

	"github.com/dmitrysharkov/goaxon/internal/pgtest"
)

func TestMain(m *testing.M) { pgtest.Run(m) }

func TestSmoke(t *testing.T) {
	pool := pgtest.NewPool(t, `CREATE TABLE smoke (id int PRIMARY KEY, name text);`)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO smoke VALUES ($1, $2)`, 1, "hello"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM smoke WHERE id = $1`, 1).Scan(&name); err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != "hello" {
		t.Fatalf("got %q, want %q", name, "hello")
	}
}

// TestIsolation verifies pgtestdb hands each test a fresh database — if
// it didn't, this row would clash with TestSmoke's.
func TestIsolation(t *testing.T) {
	pool := pgtest.NewPool(t, `CREATE TABLE smoke (id int PRIMARY KEY, name text);`)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO smoke VALUES ($1, $2)`, 1, "world"); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

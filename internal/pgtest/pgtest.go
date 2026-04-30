// Package pgtest provides a Postgres-backed test harness for goaxon.
//
// On first use it downloads and starts an embedded Postgres binary; each
// test then gets a fresh, isolated database via pgtestdb's template-clone
// trick. There is nothing production-grade here — it exists so package
// tests can talk to a real Postgres without external setup.
package pgtest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // register "pgx" driver for database/sql, used by pgtestdb
	"github.com/peterldowns/pgtestdb"
)

const (
	pgUser     = "postgres"
	pgPass     = "postgres"
	pgDatabase = "postgres"
)

var (
	once       sync.Once
	pg         *embeddedpostgres.EmbeddedPostgres
	baseConf   pgtestdb.Config
	runtimeDir string
	bootErr    error
)

// boot extracts and starts an embedded Postgres in a private RuntimePath
// so multiple test binaries (which `go test ./...` runs in parallel)
// don't collide. The downloaded tarball is shared via a stable CachePath
// to avoid re-downloading on every run.
func boot() {
	port, err := freePort()
	if err != nil {
		bootErr = fmt.Errorf("pgtest free port: %w", err)
		return
	}
	runtimeDir, err = os.MkdirTemp("", "goaxon-pgtest-")
	if err != nil {
		bootErr = fmt.Errorf("pgtest runtime dir: %w", err)
		return
	}

	cacheDir, err := sharedCacheDir()
	if err != nil {
		bootErr = fmt.Errorf("pgtest cache dir: %w", err)
		return
	}

	pg = embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(uint32(port)).
		RuntimePath(runtimeDir).
		CachePath(cacheDir).
		Username(pgUser).
		Password(pgPass).
		Database(pgDatabase))
	if err := pg.Start(); err != nil {
		bootErr = fmt.Errorf("embedded-postgres start: %w", err)
		return
	}
	baseConf = pgtestdb.Config{
		DriverName: "pgx",
		Host:       "localhost",
		Port:       fmt.Sprintf("%d", port),
		User:       pgUser,
		Password:   pgPass,
		Database:   pgDatabase,
		Options:    "sslmode=disable",
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func sharedCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cache", "goaxon-pgtest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Run boots embedded Postgres, runs the test suite, then shuts it down.
// Call from TestMain in any package that needs a real Postgres:
//
//	func TestMain(m *testing.M) { pgtest.Run(m) }
func Run(m *testing.M) {
	once.Do(boot)
	if bootErr != nil {
		fmt.Fprintln(os.Stderr, bootErr)
		os.Exit(1)
	}
	code := m.Run()
	if pg != nil {
		_ = pg.Stop()
	}
	if runtimeDir != "" {
		_ = os.RemoveAll(runtimeDir)
	}
	os.Exit(code)
}

// NewPool returns a pgxpool.Pool connected to a fresh, isolated database.
// schema is the DDL applied to the template database; identical schemas
// across tests reuse the same template (a clone per test is milliseconds).
func NewPool(t *testing.T, schema string) *pgxpool.Pool {
	t.Helper()
	if bootErr != nil {
		t.Fatalf("pgtest not booted: %v", bootErr)
	}
	cfg := pgtestdb.Custom(t, baseConf, schemaMigrator(schema))
	pool, err := pgxpool.New(context.Background(), cfg.URL())
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// schemaMigrator implements pgtestdb.Migrator from a raw DDL string. The
// hash is over the DDL itself so two tests with the same schema share a
// template DB.
type schemaMigrator string

func (s schemaMigrator) Hash() (string, error) {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:]), nil
}

func (s schemaMigrator) Migrate(ctx context.Context, db *sql.DB, _ pgtestdb.Config) error {
	_, err := db.ExecContext(ctx, string(s))
	return err
}

package pg

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// stubOpenDB swaps the package-level openDB for the duration of a test and
// restores it afterwards. Tests must not run in parallel since openDB and
// retryDelay are shared package state.
func stubOpenDB(t *testing.T, fn func(dsn string) (*gorm.DB, error)) {
	t.Helper()
	orig := openDB
	openDB = fn
	t.Cleanup(func() { openDB = orig })
}

// shortenRetryDelay drops the inter-attempt sleep so failure-path tests don't
// spend ~4s sleeping.
func shortenRetryDelay(t *testing.T) {
	t.Helper()
	orig := retryDelay
	retryDelay = time.Millisecond
	t.Cleanup(func() { retryDelay = orig })
}

// newMockGormDB builds a *gorm.DB backed by an sqlmock connection, so the
// success path runs without a live Postgres. The returned mock lets the test
// assert that all expectations were met.
func newMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open with mock conn: %v", err)
	}
	return gdb, mock
}

func TestInit_Success(t *testing.T) {
	gdb, mock := newMockGormDB(t)

	var gotDSN string
	calls := 0
	stubOpenDB(t, func(dsn string) (*gorm.DB, error) {
		calls++
		gotDSN = dsn
		return gdb, nil
	})

	db, err := Init("host=example dbname=test", 7)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if db != gdb {
		t.Errorf("Init returned a different *gorm.DB than openDB provided")
	}
	if calls != 1 {
		t.Errorf("openDB called %d times, want 1 (no retries on success)", calls)
	}
	if gotDSN != "host=example dbname=test" {
		t.Errorf("openDB got DSN %q, want the DSN passed to Init", gotDSN)
	}

	// SetMaxOpenConns is applied to the underlying *sql.DB.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 7 {
		t.Errorf("MaxOpenConnections = %d, want 7", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

func TestInit_RetriesThenSucceeds(t *testing.T) {
	shortenRetryDelay(t)
	gdb, _ := newMockGormDB(t)

	calls := 0
	stubOpenDB(t, func(string) (*gorm.DB, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("connection refused")
		}
		return gdb, nil
	})

	db, err := Init("dsn", 1)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if db == nil {
		t.Fatal("Init returned nil db on eventual success")
	}
	if calls != 3 {
		t.Errorf("openDB called %d times, want 3 (2 failures then success)", calls)
	}
}

func TestInit_AllAttemptsFail(t *testing.T) {
	shortenRetryDelay(t)

	wantErr := errors.New("boom")
	calls := 0
	stubOpenDB(t, func(string) (*gorm.DB, error) {
		calls++
		return nil, wantErr
	})

	db, err := Init("dsn", 1)
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if db != nil {
		t.Errorf("expected nil db on failure, got %v", db)
	}
	if calls != 5 {
		t.Errorf("openDB called %d times, want 5 (maxRetries)", calls)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error %v does not wrap the underlying openDB error", err)
	}
	const wantMsg = "pg.Init: failed after 5 attempts"
	if got := err.Error(); len(got) < len(wantMsg) || got[:len(wantMsg)] != wantMsg {
		t.Errorf("error message = %q, want prefix %q", got, wantMsg)
	}
}

// The default openDB must fail fast (eagerly) against an unreachable address,
// confirming the real connector wiring is intact (not just the stub).
func TestDefaultOpenDB_FailsFast(t *testing.T) {
	_, err := openDB("host=127.0.0.1 port=1 user=x dbname=x sslmode=disable connect_timeout=1")
	if err == nil {
		t.Fatal("expected error opening unreachable Postgres, got nil")
	}
}

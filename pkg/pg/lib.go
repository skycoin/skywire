// Package pg internal/pg/lib.go
package pg

import (
	"fmt"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// openDB opens a gorm connection to the given DSN. It is a package var so
// tests can substitute a fake/mock connection without a live database.
var openDB = func(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}

// retryDelay is the wait between failed connection attempts. It is a package
// var so tests can shorten it.
var retryDelay = 1 * time.Second

// Init creates a connection to database with retry logic for startup resilience.
func Init(dns string, pgMaxOpenConn int) (*gorm.DB, error) {
	const maxRetries = 5

	var db *gorm.DB
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		db, err = openDB(dns)
		if err == nil {
			dbConf, _ := db.DB() //nolint:errcheck
			dbConf.SetMaxOpenConns(pgMaxOpenConn)
			return db, nil
		}
		if attempt < maxRetries {
			log.Printf("pg.Init: connection attempt %d/%d failed: %v, retrying in %v", attempt, maxRetries, err, retryDelay)
			time.Sleep(retryDelay)
		}
	}

	return nil, fmt.Errorf("pg.Init: failed after %d attempts: %w", maxRetries, err)
}

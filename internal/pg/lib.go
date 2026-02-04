// Package pg internal/pg/lib.go
package pg

import (
	"fmt"
	"log"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Init creates a connection to database with retry logic for startup resilience.
func Init(dns string, pgMaxOpenConn int) (*gorm.DB, error) {
	const maxRetries = 10
	const retryDelay = 3 * time.Second

	var db *gorm.DB
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		db, err = gorm.Open(postgres.Open(dns), &gorm.Config{})
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

// Package pg internal/pg/lib.go
package pg

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Init creates a connection to database
func Init(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return db, err
	}

	return db, nil
}

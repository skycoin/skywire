// Package pg internal/pg/lib.go
package pg

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Init creates a connection to database
func Init(dns string, pgMaxOpenConn int) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dns), &gorm.Config{})
	if err != nil {
		return db, err
	}
	dbConf, _ := db.DB() //nolint
	dbConf.SetMaxOpenConns(pgMaxOpenConn)
	return db, nil
}

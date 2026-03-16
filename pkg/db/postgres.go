package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

func Connect(dsn string) (*sql.DB, error) {
	var db *sql.DB
	var err error

	// Retry up to 3 times — Neon DB may be waking up
	for i := range 3 {
		db, err = sql.Open("postgres", dsn)
		if err != nil {
			return nil, fmt.Errorf("sql.Open: %w", err)
		}
		if pingErr := db.Ping(); pingErr != nil {
			db.Close()
			err = pingErr
			time.Sleep(time.Duration(i+1) * 2 * time.Second)
			continue
		}
		return db, nil
	}
	return nil, fmt.Errorf("db connect after retries: %w", err)
}

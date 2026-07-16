package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	_ = godotenv.Load("../../GolandProjects/nintendoflow/.env")
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is empty")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, title_raw, source_type, created_at
		FROM articles
		WHERE source_type = 'highlight'
		ORDER BY id DESC
		LIMIT 20
	`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("HIGHLIGHT ARTICLES:")
	for rows.Next() {
		var id int
		var titleRaw, sourceType string
		var createdAt time.Time
		if err := rows.Scan(&id, &titleRaw, &sourceType, &createdAt); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ID: %d | Created: %s | Title: %s\n",
			id, createdAt.Format("2006-01-02 15:04:05"), titleRaw)
	}
}

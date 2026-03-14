package cleaner

import (
	"database/sql"
	"log/slog"

	"github.com/deuswork/nintendoflow/internal/db"
)

// Run deletes articles older than 30 days.
func Run(database *sql.DB) {
	if err := db.Cleanup(database); err != nil {
		slog.Warn("cleanup failed", "error", err)
		return
	}
	slog.Info("db cleanup: removed articles older than 30 days")
}

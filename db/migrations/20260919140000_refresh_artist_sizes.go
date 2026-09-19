package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(upRefreshArtistSizes, downRefreshArtistSizes)
}

// Recalculate persisted artist sizes even when no source files have changed.
func upRefreshArtistSizes(ctx context.Context, tx *sql.Tx) error {
	// Keep the full scan pending across startup failures or interrupted scans.
	if _, err := tx.ExecContext(ctx, `UPDATE library SET full_scan_in_progress = true`); err != nil {
		return err
	}
	return forceFullRescan(ctx, tx)
}

func downRefreshArtistSizes(_ context.Context, _ *sql.Tx) error {
	return nil
}

package migrations

import (
	"context"
	"database/sql"

	"github.com/navidrome/navidrome/consts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("upRefreshArtistSizes", func() {
	It("schedules all libraries for a full refresh and preserves retry state", func() {
		db, err := sql.Open("sqlite3", ":memory:")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = db.Close() })
		_, err = db.Exec(`
   CREATE TABLE property (id text primary key, value text);
   CREATE TABLE library (id integer primary key, full_scan_in_progress boolean);
   INSERT INTO library VALUES (1, false), (2, false);
  `)
		Expect(err).ToNot(HaveOccurred())
		tx, err := db.Begin()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = tx.Rollback() })
		Expect(upRefreshArtistSizes(context.Background(), tx)).To(Succeed())
		var flag string
		Expect(tx.QueryRowContext(context.Background(), "SELECT value FROM property WHERE id = ?", consts.FullScanAfterMigrationFlagKey).Scan(&flag)).To(Succeed())
		Expect(flag).To(Equal("1"))
		// Startup consumes the property before scanning; library flags must survive.
		_, err = tx.ExecContext(context.Background(), "DELETE FROM property WHERE id = ?", consts.FullScanAfterMigrationFlagKey)
		Expect(err).ToNot(HaveOccurred())
		var pending int
		Expect(tx.QueryRowContext(context.Background(), "SELECT count(*) FROM library WHERE full_scan_in_progress = true").Scan(&pending)).To(Succeed())
		Expect(pending).To(Equal(2))
	})
})

package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

const (
	defaultWispReaperInterval = 30 * time.Minute
	wispReaperQueryTimeout    = 30 * time.Second
	// Wisps older than this are reaped (closed).
	defaultWispMaxAge = 24 * time.Hour
	// Closed wisps older than this are permanently deleted.
	defaultWispDeleteAge = 7 * 24 * time.Hour
	// Issues with no status change older than this are auto-closed.
	defaultStaleIssueAge = 30 * 24 * time.Hour
	// Alert threshold: if open wisp count exceeds this, escalate.
	wispAlertThreshold = 500
	// Batch size for DELETE operations to avoid long-running transactions.
	deleteBatchSize = 100
)

// WispReaperConfig holds configuration for the wisp_reaper patrol.
type WispReaperConfig struct {
	// Enabled controls whether the reaper runs.
	Enabled bool `json:"enabled"`

	// IntervalStr is how often to run, as a string (e.g., "30m").
	IntervalStr string `json:"interval,omitempty"`

	// MaxAgeStr is how old a wisp must be before reaping (e.g., "24h").
	MaxAgeStr string `json:"max_age,omitempty"`

	// DeleteAgeStr is how long after closing before wisps are deleted (e.g., "168h" for 7 days).
	DeleteAgeStr string `json:"delete_age,omitempty"`

	// StaleIssueAgeStr is how long an issue can be unchanged before auto-close (e.g., "720h" for 30 days).
	StaleIssueAgeStr string `json:"stale_issue_age,omitempty"`

	// Databases lists specific database names to reap.
	// If empty, auto-discovers from dolt server.
	Databases []string `json:"databases,omitempty"`
}

// wispReaperInterval returns the configured interval, or the default (30m).
func wispReaperInterval(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.WispReaper != nil {
		if config.Patrols.WispReaper.IntervalStr != "" {
			if d, err := time.ParseDuration(config.Patrols.WispReaper.IntervalStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultWispReaperInterval
}

// wispReaperMaxAge returns the configured max age, or the default (24h).
func wispReaperMaxAge(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.WispReaper != nil {
		if config.Patrols.WispReaper.MaxAgeStr != "" {
			if d, err := time.ParseDuration(config.Patrols.WispReaper.MaxAgeStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultWispMaxAge
}

// wispDeleteAge returns the configured delete age, or the default (7 days).
func wispDeleteAge(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.WispReaper != nil {
		if config.Patrols.WispReaper.DeleteAgeStr != "" {
			if d, err := time.ParseDuration(config.Patrols.WispReaper.DeleteAgeStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultWispDeleteAge
}

// staleIssueAge returns the configured stale issue age, or the default (30 days).
func staleIssueAge(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.WispReaper != nil {
		if config.Patrols.WispReaper.StaleIssueAgeStr != "" {
			if d, err := time.ParseDuration(config.Patrols.WispReaper.StaleIssueAgeStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultStaleIssueAge
}

// reapWisps closes stale wisps across all configured databases.
// Non-fatal: errors are logged but don't stop the daemon.
func (d *Daemon) reapWisps() {
	if !IsPatrolEnabled(d.patrolConfig, "wisp_reaper") {
		return
	}

	config := d.patrolConfig.Patrols.WispReaper
	maxAge := wispReaperMaxAge(d.patrolConfig)
	cutoff := time.Now().UTC().Add(-maxAge)

	// Determine databases to reap.
	databases := config.Databases
	if len(databases) == 0 {
		databases = d.discoverDoltDatabases()
	}
	if len(databases) == 0 {
		d.logger.Printf("wisp_reaper: no databases to reap")
		return
	}

	deleteAge := wispDeleteAge(d.patrolConfig)
	deleteCutoff := time.Now().UTC().Add(-deleteAge)
	issueAge := staleIssueAge(d.patrolConfig)
	issueCutoff := time.Now().UTC().Add(-issueAge)

	totalReaped := 0
	totalOpen := 0
	totalDeleted := 0
	totalIssuesClosed := 0

	for _, dbName := range databases {
		if !validDBName.MatchString(dbName) {
			d.logger.Printf("wisp_reaper: skipping invalid database name: %q", dbName)
			continue
		}

		// Pass 1: Close stale wisps.
		reaped, open, err := d.reapWispsInDB(dbName, cutoff)
		if err != nil {
			d.logger.Printf("wisp_reaper: %s: close error: %v", dbName, err)
		} else {
			totalReaped += reaped
			totalOpen += open
			if reaped > 0 {
				d.logger.Printf("wisp_reaper: %s: closed %d stale wisps (older than %v), %d open remain",
					dbName, reaped, maxAge, open)
			}
		}

		// Pass 2: Delete closed wisps older than delete age.
		deleted, err := d.deleteClosedWispsInDB(dbName, deleteCutoff)
		if err != nil {
			d.logger.Printf("wisp_reaper: %s: delete error: %v", dbName, err)
		} else {
			totalDeleted += deleted
		}

		// Pass 3: Auto-close stale issues.
		closed, err := d.autoCloseStaleIssuesInDB(dbName, issueCutoff)
		if err != nil {
			d.logger.Printf("wisp_reaper: %s: auto-close issues error: %v", dbName, err)
		} else {
			totalIssuesClosed += closed
		}
	}

	if totalReaped > 0 {
		d.logger.Printf("wisp_reaper: total closed %d stale wisps across %d databases, %d open remain",
			totalReaped, len(databases), totalOpen)
	}
	if totalDeleted > 0 {
		d.logger.Printf("wisp_reaper: total deleted %d closed wisp rows across %d databases",
			totalDeleted, len(databases))
	}
	if totalIssuesClosed > 0 {
		d.logger.Printf("wisp_reaper: total auto-closed %d stale issues across %d databases",
			totalIssuesClosed, len(databases))
	}

	// Alert if open wisp count is too high.
	if totalOpen > wispAlertThreshold {
		d.logger.Printf("wisp_reaper: WARNING: %d open wisps exceed threshold %d — investigate wisp lifecycle",
			totalOpen, wispAlertThreshold)
	}
}

// reapWispsInDB closes stale wisps in a single database.
// Returns (reaped count, remaining open count, error).
func (d *Daemon) reapWispsInDB(dbName string, cutoff time.Time) (int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wispReaperQueryTimeout)
	defer cancel()

	dsn := fmt.Sprintf("root@tcp(%s:%d)/%s?parseTime=true&timeout=5s&readTimeout=10s&writeTimeout=10s",
		"127.0.0.1", d.doltServerPort(), dbName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return 0, 0, fmt.Errorf("open connection: %w", err)
	}
	defer db.Close()

	// Close stale open wisps (status=open, created before cutoff).
	// Also close stale hooked/in_progress wisps — these are abandoned molecule steps.
	closeQuery := fmt.Sprintf(
		"UPDATE `%s`.wisps SET status='closed', closed_at=NOW() WHERE status IN ('open', 'hooked', 'in_progress') AND created_at < ?",
		dbName)
	result, err := db.ExecContext(ctx, closeQuery, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("close stale wisps: %w", err)
	}

	reaped, _ := result.RowsAffected()

	// Count remaining open wisps.
	var openCount int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM `%s`.wisps WHERE status IN ('open', 'hooked', 'in_progress')", dbName)
	if err := db.QueryRowContext(ctx, countQuery).Scan(&openCount); err != nil {
		return int(reaped), 0, fmt.Errorf("count open wisps: %w", err)
	}

	return int(reaped), openCount, nil
}

// deleteClosedWispsInDB deletes closed wisp rows (and their auxiliary data) older than
// the delete cutoff. Deletes in batches to avoid long-running transactions.
// Returns the number of wisp rows deleted.
func (d *Daemon) deleteClosedWispsInDB(dbName string, deleteCutoff time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dsn := fmt.Sprintf("root@tcp(%s:%d)/%s?parseTime=true&timeout=5s&readTimeout=30s&writeTimeout=30s",
		"127.0.0.1", d.doltServerPort(), dbName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return 0, fmt.Errorf("open connection: %w", err)
	}
	defer db.Close()

	// Digest: count closed wisps eligible for deletion, grouped by wisp_type.
	digestQuery := fmt.Sprintf(
		"SELECT COALESCE(wisp_type, 'unknown') AS wtype, COUNT(*) AS cnt FROM `%s`.wisps WHERE status = 'closed' AND closed_at < ? GROUP BY wtype",
		dbName)
	rows, err := db.QueryContext(ctx, digestQuery, deleteCutoff)
	if err != nil {
		return 0, fmt.Errorf("digest query: %w", err)
	}
	digestTotal := 0
	for rows.Next() {
		var wtype string
		var cnt int
		if err := rows.Scan(&wtype, &cnt); err != nil {
			rows.Close()
			return 0, fmt.Errorf("digest scan: %w", err)
		}
		if cnt > 0 {
			d.logger.Printf("wisp_reaper: %s: delete digest: type=%s count=%d", dbName, wtype, cnt)
		}
		digestTotal += cnt
	}
	rows.Close()

	if digestTotal == 0 {
		return 0, nil
	}

	d.logger.Printf("wisp_reaper: %s: deleting %d closed wisp rows (closed before %v)",
		dbName, digestTotal, deleteCutoff.Format(time.RFC3339))

	// Batch delete: select IDs, delete aux tables first, then wisps.
	totalDeleted := 0
	for {
		// Get a batch of IDs to delete.
		idQuery := fmt.Sprintf(
			"SELECT id FROM `%s`.wisps WHERE status = 'closed' AND closed_at < ? LIMIT %d",
			dbName, deleteBatchSize)
		idRows, err := db.QueryContext(ctx, idQuery, deleteCutoff)
		if err != nil {
			return totalDeleted, fmt.Errorf("select batch: %w", err)
		}

		var ids []string
		for idRows.Next() {
			var id string
			if err := idRows.Scan(&id); err != nil {
				idRows.Close()
				return totalDeleted, fmt.Errorf("scan id: %w", err)
			}
			ids = append(ids, id)
		}
		idRows.Close()

		if len(ids) == 0 {
			break
		}

		// Build IN clause with placeholders.
		placeholders := make([]string, len(ids))
		args := make([]interface{}, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			args[i] = id
		}
		inClause := "(" + joinStrings(placeholders, ",") + ")"

		// Delete from auxiliary tables first (foreign key safety).
		auxTables := []string{"wisp_labels", "wisp_comments", "wisp_events", "wisp_dependencies"}
		for _, tbl := range auxTables {
			delAux := fmt.Sprintf("DELETE FROM `%s`.`%s` WHERE issue_id IN %s", dbName, tbl, inClause)
			if _, err := db.ExecContext(ctx, delAux, args...); err != nil {
				// Log but continue — table might not exist in all databases.
				d.logger.Printf("wisp_reaper: %s: delete from %s: %v", dbName, tbl, err)
			}
		}

		// Delete the wisp rows themselves.
		delWisps := fmt.Sprintf("DELETE FROM `%s`.wisps WHERE id IN %s", dbName, inClause)
		result, err := db.ExecContext(ctx, delWisps, args...)
		if err != nil {
			return totalDeleted, fmt.Errorf("delete wisps batch: %w", err)
		}
		affected, _ := result.RowsAffected()
		totalDeleted += int(affected)
	}

	if totalDeleted > 0 {
		d.logger.Printf("wisp_reaper: %s: deleted %d closed wisp rows and associated data",
			dbName, totalDeleted)
	}

	return totalDeleted, nil
}

// autoCloseStaleIssuesInDB closes issues that have been open with no status change
// for longer than the stale cutoff. Exempts P0 and P1 issues (priority <= 1).
// Returns the number of issues auto-closed.
func (d *Daemon) autoCloseStaleIssuesInDB(dbName string, staleCutoff time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wispReaperQueryTimeout)
	defer cancel()

	dsn := fmt.Sprintf("root@tcp(%s:%d)/%s?parseTime=true&timeout=5s&readTimeout=10s&writeTimeout=10s",
		"127.0.0.1", d.doltServerPort(), dbName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return 0, fmt.Errorf("open connection: %w", err)
	}
	defer db.Close()

	// Check if issues table exists (not all databases have it).
	var dummy int
	checkQuery := fmt.Sprintf("SELECT 1 FROM `%s`.issues LIMIT 1", dbName)
	if err := db.QueryRowContext(ctx, checkQuery).Scan(&dummy); err != nil {
		// Table doesn't exist or is empty — skip silently.
		return 0, nil
	}

	// Auto-close stale issues: open >30 days, priority > 1 (exempt P0/P1).
	// Use updated_at as proxy for "last status change".
	closeQuery := fmt.Sprintf(
		"UPDATE `%s`.issues SET status='closed', closed_at=NOW(), close_reason='stale:auto-closed by reaper' WHERE status IN ('open', 'in_progress') AND updated_at < ? AND priority > 1",
		dbName)
	result, err := db.ExecContext(ctx, closeQuery, staleCutoff)
	if err != nil {
		return 0, fmt.Errorf("auto-close stale issues: %w", err)
	}

	closed, _ := result.RowsAffected()
	if closed > 0 {
		d.logger.Printf("wisp_reaper: %s: auto-closed %d stale issues (unchanged for >%v, priority > P1)",
			dbName, closed, defaultStaleIssueAge)
	}

	return int(closed), nil
}

// joinStrings joins strings with a separator. Simple helper to avoid importing strings.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}

// discoverDoltDatabases returns the list of known production databases.
// Hardcoded for now — matches the databases in daemon.json and dolt-data.
func (d *Daemon) discoverDoltDatabases() []string {
	return []string{"hq", "beads", "gastown"}
}

// doltServerPort returns the configured Dolt server port.
func (d *Daemon) doltServerPort() int {
	if d.doltServer != nil {
		return d.doltServer.config.Port
	}
	return 3307 // Default
}

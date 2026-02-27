package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

const (
	defaultCompactorDogInterval       = 24 * time.Hour
	compactorDogQueryTimeout          = 30 * time.Second
	compactorDogFlattenTimeout        = 5 * time.Minute
	compactorDogDefaultCommitThreshold = 500
	compactorDogTempBranch            = "gt-compaction"
)

// validDoltHash matches a 32-char hex hash (Dolt commit hash format).
var validDoltHash = regexp.MustCompile(`^[0-9a-v]{32}$`)

// CompactorDogConfig holds configuration for the compactor_dog patrol.
type CompactorDogConfig struct {
	// Enabled controls whether the compactor dog runs.
	Enabled bool `json:"enabled"`

	// IntervalStr is how often to run, as a string (e.g., "24h").
	IntervalStr string `json:"interval,omitempty"`

	// Databases lists the production databases to compact.
	// If empty, uses the default set.
	Databases []string `json:"databases,omitempty"`

	// CommitThreshold is the minimum commit count before flattening.
	// Databases with fewer commits are skipped.
	CommitThreshold int `json:"commit_threshold,omitempty"`
}

// compactorDogInterval returns the configured interval, or the default (24h).
func compactorDogInterval(config *DaemonPatrolConfig) time.Duration {
	if config != nil && config.Patrols != nil && config.Patrols.CompactorDog != nil {
		if config.Patrols.CompactorDog.IntervalStr != "" {
			if d, err := time.ParseDuration(config.Patrols.CompactorDog.IntervalStr); err == nil && d > 0 {
				return d
			}
		}
	}
	return defaultCompactorDogInterval
}

// compactorDogDatabases returns the list of production databases to compact.
func compactorDogDatabases(config *DaemonPatrolConfig) []string {
	if config != nil && config.Patrols != nil && config.Patrols.CompactorDog != nil {
		if len(config.Patrols.CompactorDog.Databases) > 0 {
			return config.Patrols.CompactorDog.Databases
		}
	}
	return []string{"hq", "beads", "gastown"}
}

// compactorDogCommitThreshold returns the commit count threshold for flattening.
func compactorDogCommitThreshold(config *DaemonPatrolConfig) int {
	if config != nil && config.Patrols != nil && config.Patrols.CompactorDog != nil {
		if config.Patrols.CompactorDog.CommitThreshold > 0 {
			return config.Patrols.CompactorDog.CommitThreshold
		}
	}
	return compactorDogDefaultCommitThreshold
}

// runCompactorDog flattens commit history on production databases.
//
// Dolt stores every mutation as a commit. Over time, the commit graph grows
// unbounded (5,000-10,000 wisp mutations/day). This dog periodically flattens
// the history to a single commit, reclaiming graph storage. The Doctor Dog's
// gc cycle then reclaims the chunk storage.
//
// Algorithm per database:
//  1. Count commits. Skip if below threshold.
//  2. Record main's HEAD hash (for concurrency safety).
//  3. Create temp branch, soft-reset to root, commit all data.
//  4. Verify main hasn't moved. If it has, abort (retry next cycle).
//  5. Reset main to the flattened commit. Delete temp branch.
func (d *Daemon) runCompactorDog() {
	if !IsPatrolEnabled(d.patrolConfig, "compactor_dog") {
		return
	}

	d.logger.Printf("compactor_dog: starting compaction cycle")

	// Pour molecule for observability.
	mol := d.pourDogMolecule("mol-dog-compactor", nil)
	defer mol.close()

	host := "127.0.0.1"
	port := d.doltServerPort()
	threshold := compactorDogCommitThreshold(d.patrolConfig)
	databases := compactorDogDatabases(d.patrolConfig)

	var totalCompacted int
	var totalSkipped int
	var totalFailed int

	// Inspect phase: count commits across all databases.
	mol.closeStep("inspect")

	for _, dbName := range databases {
		compacted, err := d.compactorDogFlattenDatabase(host, port, dbName, threshold)
		if err != nil {
			d.logger.Printf("compactor_dog: %s: flatten failed: %v", dbName, err)
			d.escalate("compactor_dog", fmt.Sprintf("Flatten failed on %s: %v", dbName, err))
			totalFailed++
			continue
		}
		if compacted {
			totalCompacted++
		} else {
			totalSkipped++
		}
	}

	// Close compact step.
	if totalFailed > 0 {
		mol.failStep("compact", fmt.Sprintf("%d database(s) failed", totalFailed))
	} else {
		mol.closeStep("compact")
	}

	// Verify phase: spot-check that databases are still readable.
	allHealthy := true
	for _, dbName := range databases {
		if !d.compactorDogVerifyDatabase(host, port, dbName) {
			allHealthy = false
		}
	}
	if allHealthy {
		mol.closeStep("verify")
	} else {
		mol.failStep("verify", "post-compaction health check failed")
	}

	// Report.
	d.logger.Printf("compactor_dog: cycle complete — compacted=%d skipped=%d failed=%d",
		totalCompacted, totalSkipped, totalFailed)
	mol.closeStep("report")
}

// compactorDogFlattenDatabase flattens a single database's commit history.
// Returns true if compaction was performed, false if skipped (below threshold).
func (d *Daemon) compactorDogFlattenDatabase(host string, port int, dbName string, threshold int) (bool, error) {
	dsn := fmt.Sprintf("root@tcp(%s:%d)/%s?timeout=5s&readTimeout=10s&writeTimeout=10s",
		host, port, dbName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return false, fmt.Errorf("open connection: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), compactorDogFlattenTimeout)
	defer cancel()

	// 1. Count commits.
	commitCount, err := d.compactorDogCountCommits(ctx, db, dbName)
	if err != nil {
		return false, fmt.Errorf("count commits: %w", err)
	}

	if commitCount < threshold {
		d.logger.Printf("compactor_dog: %s: %d commits (threshold %d), skipping",
			dbName, commitCount, threshold)
		return false, nil
	}

	d.logger.Printf("compactor_dog: %s: %d commits (threshold %d), flattening",
		dbName, commitCount, threshold)

	// 2. Record main's HEAD hash for concurrency check.
	mainHead, err := d.compactorDogGetHead(ctx, db)
	if err != nil {
		return false, fmt.Errorf("get main HEAD: %w", err)
	}

	// 3. Get root (initial) commit hash.
	rootHash, err := d.compactorDogGetRootHash(ctx, db)
	if err != nil {
		return false, fmt.Errorf("get root hash: %w", err)
	}

	// Safety: validate hashes look like Dolt commit hashes.
	if !validDoltHash.MatchString(mainHead) {
		return false, fmt.Errorf("invalid main HEAD hash: %q", mainHead)
	}
	if !validDoltHash.MatchString(rootHash) {
		return false, fmt.Errorf("invalid root hash: %q", rootHash)
	}

	// Don't flatten if there's only one commit (already flat).
	if mainHead == rootHash {
		d.logger.Printf("compactor_dog: %s: already flat (single commit), skipping", dbName)
		return false, nil
	}

	// 4. Clean up any leftover temp branch from a previous failed run.
	_, _ = db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch))

	// 5. Create temp branch from current HEAD.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_CHECKOUT('-b', '%s')", compactorDogTempBranch)); err != nil {
		return false, fmt.Errorf("create temp branch: %w", err)
	}

	// 6. Soft reset to root commit (keeps all data in working set).
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_RESET('--soft', '%s')", rootHash)); err != nil {
		// Cleanup: try to get back to main and delete temp branch.
		db.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')")
		db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch))
		return false, fmt.Errorf("soft reset to root: %w", err)
	}

	// 7. Commit all data as a single commit.
	if _, err := db.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'compaction: flatten history')"); err != nil {
		db.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')")
		db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch))
		return false, fmt.Errorf("commit flattened data: %w", err)
	}

	// 8. Get the new (flattened) commit hash.
	newHead, err := d.compactorDogGetHead(ctx, db)
	if err != nil {
		db.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')")
		db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch))
		return false, fmt.Errorf("get flattened HEAD: %w", err)
	}

	// 9. Switch back to main.
	if _, err := db.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')"); err != nil {
		return false, fmt.Errorf("checkout main: %w", err)
	}

	// 10. Concurrency check: verify main hasn't moved since we started.
	currentMain, err := d.compactorDogGetHead(ctx, db)
	if err != nil {
		db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch))
		return false, fmt.Errorf("verify main HEAD: %w", err)
	}
	if currentMain != mainHead {
		d.logger.Printf("compactor_dog: %s: main moved during compaction (%s → %s), aborting",
			dbName, mainHead[:8], currentMain[:8])
		db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch))
		return false, nil // Not an error — retry next cycle.
	}

	// 11. Hard reset main to the flattened commit.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_RESET('--hard', '%s')", newHead)); err != nil {
		db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch))
		return false, fmt.Errorf("reset main to flattened: %w", err)
	}

	// 12. Delete temp branch.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", compactorDogTempBranch)); err != nil {
		d.logger.Printf("compactor_dog: %s: warning: failed to delete temp branch: %v", dbName, err)
		// Non-fatal — the branch will be cleaned up next cycle.
	}

	// 13. Verify the flatten worked.
	newCount, err := d.compactorDogCountCommits(ctx, db, dbName)
	if err != nil {
		d.logger.Printf("compactor_dog: %s: warning: post-flatten commit count failed: %v", dbName, err)
	} else {
		d.logger.Printf("compactor_dog: %s: flattened %d → %d commits", dbName, commitCount, newCount)
	}

	return true, nil
}

// compactorDogCountCommits counts commits in the current database.
func (d *Daemon) compactorDogCountCommits(ctx context.Context, db *sql.DB, dbName string) (int, error) {
	queryCtx, cancel := context.WithTimeout(ctx, compactorDogQueryTimeout)
	defer cancel()

	var count int
	// Use a limit to avoid scanning millions of rows on pathologically large histories.
	err := db.QueryRowContext(queryCtx,
		"SELECT COUNT(*) FROM (SELECT 1 FROM dolt_log LIMIT 10000) AS t").Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// compactorDogGetHead returns the HEAD commit hash of the current branch.
func (d *Daemon) compactorDogGetHead(ctx context.Context, db *sql.DB) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, compactorDogQueryTimeout)
	defer cancel()

	var hash string
	err := db.QueryRowContext(queryCtx,
		"SELECT commit_hash FROM dolt_log LIMIT 1").Scan(&hash)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// compactorDogGetRootHash returns the initial (oldest) commit hash.
func (d *Daemon) compactorDogGetRootHash(ctx context.Context, db *sql.DB) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, compactorDogQueryTimeout)
	defer cancel()

	var hash string
	err := db.QueryRowContext(queryCtx,
		"SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1").Scan(&hash)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// compactorDogVerifyDatabase does a basic health check after compaction.
func (d *Daemon) compactorDogVerifyDatabase(host string, port int, dbName string) bool {
	dsn := fmt.Sprintf("root@tcp(%s:%d)/%s?timeout=5s&readTimeout=10s",
		host, port, dbName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		d.logger.Printf("compactor_dog: verify %s: open failed: %v", dbName, err)
		return false
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), compactorDogQueryTimeout)
	defer cancel()

	// Check that we can read the issues table (the primary data).
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&count); err != nil {
		d.logger.Printf("compactor_dog: verify %s: issues count failed: %v", dbName, err)
		d.escalate("compactor_dog", fmt.Sprintf("Post-compaction verify failed on %s: %v", dbName, err))
		return false
	}

	d.logger.Printf("compactor_dog: verify %s: %d issues (OK)", dbName, count)
	return true
}

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/oona-insurance/dev-portal/internal/tasks"
)

// ScanScheduleRecord represents a record in service_scan_schedules table
type ScanScheduleRecord struct {
	ID             string
	ServiceName    string
	IsEnabled      bool
	TargetBranches string
	ScheduleTime   string
	Frequency      string
	Timezone       string
	LastRunAt      sql.NullTime
	LastStatus     sql.NullString
}

// StartScanScheduler launches a background goroutine that polls every minute,
// matches Asia/Jakarta local time against configured schedule_time, and enqueues
// Trivy scans one-by-one into the trivy_scan queue (Queue-based FIFO).
func StartScanScheduler(ctx context.Context, redisClient *asynq.Client, dbDSN string) {
	if dbDSN == "" {
		slog.Warn("ScanScheduler: DB_DSN not provided, scheduler disabled.")
		return
	}

	db, err := sql.Open("pgx", dbDSN)
	if err != nil {
		slog.Error("ScanScheduler: Failed to connect to postgres", "error", err)
		return
	}

	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		slog.Warn("ScanScheduler: Failed to load Asia/Jakarta timezone, falling back to FixedZone UTC+7", "error", err)
		loc = time.FixedZone("WIB", 7*3600)
	}

	ticker := time.NewTicker(1 * time.Minute)

	slog.Info("ScanScheduler: Started successfully", "timezone", "Asia/Jakarta (WIB, UTC+7)", "interval", "1m")

	go func() {
		defer db.Close()
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				slog.Info("ScanScheduler: Stopping...")
				return
			case t := <-ticker.C:
				nowWIB := t.In(loc)
				currentHM := nowWIB.Format("15:04")
				currentWeekday := nowWIB.Weekday()
				slog.Debug("ScanScheduler: Tick", "time_wib", currentHM, "weekday", currentWeekday.String())

				checkAndEnqueueSchedules(ctx, db, redisClient, currentHM, currentWeekday, nowWIB)
			}
		}
	}()
}

func checkAndEnqueueSchedules(ctx context.Context, db *sql.DB, client *asynq.Client, currentHM string, weekday time.Weekday, nowWIB time.Time) {
	query := `
		SELECT id, service_name, is_enabled, target_branches, schedule_time, frequency, timezone, last_run_at, last_status
		FROM service_scan_schedules
		WHERE is_enabled = true AND schedule_time = $1
	`
	rows, err := db.QueryContext(ctx, query, currentHM)
	if err != nil {
		slog.Error("ScanScheduler: DB query failed", "error", err, "time", currentHM)
		return
	}
	defer rows.Close()

	matchedCount := 0
	for rows.Next() {
		var s ScanScheduleRecord
		if err := rows.Scan(&s.ID, &s.ServiceName, &s.IsEnabled, &s.TargetBranches, &s.ScheduleTime, &s.Frequency, &s.Timezone, &s.LastRunAt, &s.LastStatus); err != nil {
			slog.Error("ScanScheduler: Row scan failed", "error", err)
			continue
		}

		// Frequency check
		if s.Frequency == "weekly" && weekday != time.Sunday {
			continue
		}

		// Avoid duplicate triggers in the same minute
		if s.LastRunAt.Valid {
			lastWIB := s.LastRunAt.Time.In(nowWIB.Location())
			if lastWIB.Year() == nowWIB.Year() && lastWIB.YearDay() == nowWIB.YearDay() && lastWIB.Format("15:04") == currentHM {
				slog.Info("ScanScheduler: Skipping duplicate trigger", "service", s.ServiceName, "time", currentHM)
				continue
			}
		}

		matchedCount++

		// Enqueue scan task for each target branch
		branches := strings.Split(s.TargetBranches, ",")
		for _, b := range branches {
			branch := strings.TrimSpace(b)
			if branch == "" {
				continue
			}

			repoURL := fmt.Sprintf("https://github.com/oona-insurance/%s", s.ServiceName)
			payload, _ := json.Marshal(tasks.TrivyScanPayload{
				TicketID: s.ServiceName,
				RepoURL:  repoURL,
			})

			task := asynq.NewTask(
				tasks.TypeTrivyScan,
				payload,
				asynq.Queue("trivy_scan"),
				asynq.MaxRetry(2),
				asynq.Timeout(10*time.Minute),
			)

			info, err := client.EnqueueContext(ctx, task)
			if err != nil {
				slog.Error("ScanScheduler: Failed to enqueue trivy task", "service", s.ServiceName, "branch", branch, "error", err)
			} else {
				slog.Info("ScanScheduler: Queued Trivy scan", "service", s.ServiceName, "branch", branch, "task_id", info.ID, "queue", "trivy_scan")
			}
		}

		// Update last_run_at in DB
		_, _ = db.ExecContext(ctx, `UPDATE service_scan_schedules SET last_run_at = $1, last_status = 'QUEUED', updated_at = NOW() WHERE id = $2`, nowWIB, s.ID)
	}

	if matchedCount > 0 {
		slog.Info("ScanScheduler: Matched and queued services", "count", matchedCount, "time", currentHM)
	}
}

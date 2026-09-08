package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// ScanScheduleDTO models the schedule configuration payload
type ScanScheduleDTO struct {
	ServiceName    string `json:"service_name"`
	IsEnabled      bool   `json:"is_enabled"`
	TargetBranches string `json:"target_branches"`
	ScheduleTime   string `json:"schedule_time"`
	Frequency      string `json:"frequency"`
	Timezone       string `json:"timezone"`
	LastRunAt      string `json:"last_run_at,omitempty"`
	LastStatus     string `json:"last_status,omitempty"`
}

// GetScanScheduleHandler handles GET /api/v1/catalog/{service}/scan-schedule
func GetScanScheduleHandler(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "service")
	if serviceName == "" {
		http.Error(w, "Service name required", http.StatusBadRequest)
		return
	}

	dto := ScanScheduleDTO{
		ServiceName:    serviceName,
		IsEnabled:      true,
		TargetBranches: "main,uat",
		ScheduleTime:   "01:30",
		Frequency:      "daily",
		Timezone:       "Asia/Jakarta",
		LastStatus:     "IDLE",
	}

	if DBPool != nil {
		var isEnabled bool
		var targetBranches, scheduleTime, frequency, timezone string
		var lastRunAt sql.NullTime
		var lastStatus sql.NullString

		query := `
			SELECT is_enabled, target_branches, schedule_time, frequency, timezone, last_run_at, last_status
			FROM service_scan_schedules
			WHERE service_name = $1
		`
		err := DBPool.QueryRow(r.Context(), query, serviceName).Scan(
			&isEnabled, &targetBranches, &scheduleTime, &frequency, &timezone, &lastRunAt, &lastStatus,
		)
		if err == nil {
			dto.IsEnabled = isEnabled
			dto.TargetBranches = targetBranches
			dto.ScheduleTime = scheduleTime
			dto.Frequency = frequency
			dto.Timezone = timezone
			if lastRunAt.Valid {
				loc, _ := time.LoadLocation("Asia/Jakarta")
				if loc == nil {
					loc = time.FixedZone("WIB", 7*3600)
				}
				dto.LastRunAt = lastRunAt.Time.In(loc).Format("02 Jan 2006 15:04 WIB")
			}
			if lastStatus.Valid {
				dto.LastStatus = lastStatus.String
			}
		}
	}

	renderScheduleFormHTML(w, dto, false)
}

// SaveScanScheduleHandler handles POST /api/v1/catalog/{service}/scan-schedule
func SaveScanScheduleHandler(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "service")
	if serviceName == "" {
		http.Error(w, "Service name required", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	isEnabled := r.FormValue("is_enabled") == "true" || r.FormValue("is_enabled") == "on"
	scheduleTime := strings.TrimSpace(r.FormValue("schedule_time"))
	if scheduleTime == "" {
		scheduleTime = "01:30"
	}
	frequency := strings.TrimSpace(r.FormValue("frequency"))
	if frequency == "" {
		frequency = "daily"
	}

	// Branches checkboxes
	var branches []string
	if r.FormValue("branch_main") != "" {
		branches = append(branches, "main")
	}
	if r.FormValue("branch_uat") != "" {
		branches = append(branches, "uat")
	}
	if r.FormValue("branch_dev") != "" {
		branches = append(branches, "dev")
	}
	if r.FormValue("branch_staging") != "" {
		branches = append(branches, "staging")
	}
	if len(branches) == 0 {
		branches = []string{"main"}
	}
	targetBranches := strings.Join(branches, ",")

	dto := ScanScheduleDTO{
		ServiceName:    serviceName,
		IsEnabled:      isEnabled,
		TargetBranches: targetBranches,
		ScheduleTime:   scheduleTime,
		Frequency:      frequency,
		Timezone:       "Asia/Jakarta",
		LastStatus:     "CONFIGURED",
	}

	if DBPool != nil {
		upsertQuery := `
			INSERT INTO service_scan_schedules (service_name, is_enabled, target_branches, schedule_time, frequency, timezone, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
			ON CONFLICT (service_name) DO UPDATE SET
				is_enabled = EXCLUDED.is_enabled,
				target_branches = EXCLUDED.target_branches,
				schedule_time = EXCLUDED.schedule_time,
				frequency = EXCLUDED.frequency,
				timezone = EXCLUDED.timezone,
				updated_at = NOW()
		`
		_, _ = DBPool.Exec(r.Context(), upsertQuery, serviceName, isEnabled, targetBranches, scheduleTime, frequency, "Asia/Jakarta")

		RecordAudit(r.Context(), r, "UPDATE_SCAN_SCHEDULE", "service", serviceName, map[string]interface{}{
			"is_enabled":      isEnabled,
			"target_branches": targetBranches,
			"schedule_time":   scheduleTime,
			"frequency":       frequency,
			"timezone":        "Asia/Jakarta",
		})
	}

	renderScheduleFormHTML(w, dto, true)
}

func renderScheduleFormHTML(w http.ResponseWriter, dto ScanScheduleDTO, isSavedToast bool) {
	w.Header().Set("Content-Type", "text/html")

	toastHTML := ""
	if isSavedToast {
		toastHTML = `
		<div class="mb-4 p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 text-xs font-semibold flex items-center justify-between">
			<span class="flex items-center gap-1.5">
				<svg class="w-4 h-4 text-emerald-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>
				Scan schedule saved successfully! (Timezone locked to Asia/Jakarta - WIB)
			</span>
		</div>`
	}

	hasMain := strings.Contains(dto.TargetBranches, "main")
	hasUAT := strings.Contains(dto.TargetBranches, "uat")
	hasDev := strings.Contains(dto.TargetBranches, "dev")
	hasStaging := strings.Contains(dto.TargetBranches, "staging")

	checkedStr := func(b bool) string {
		if b {
			return "checked"
		}
		return ""
	}

	enabledChecked := checkedStr(dto.IsEnabled)
	dailyChecked := checkedStr(dto.Frequency == "daily" || dto.Frequency == "")
	weeklyChecked := checkedStr(dto.Frequency == "weekly")

	lastRunText := "Never run yet"
	if dto.LastRunAt != "" {
		lastRunText = dto.LastRunAt
	}

	html := fmt.Sprintf(`%s
	<form hx-post="/api/v1/catalog/%s/scan-schedule" hx-target="#scan-schedule-container" class="space-y-4">
		<div class="flex items-center justify-between pb-3 border-b border-slate-800">
			<div class="flex items-center gap-3">
				<input type="checkbox" id="sched-is-enabled" name="is_enabled" value="true" %s class="w-4 h-4 rounded bg-slate-950 border-slate-700 text-indigo-600 focus:ring-indigo-500 cursor-pointer">
				<label for="sched-is-enabled" class="text-xs font-bold text-white cursor-pointer select-none">
					Enable Automated Scheduled Security Scan for this Service
				</label>
			</div>
			<span class="text-[10px] font-mono font-bold px-2 py-0.5 rounded bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
				Queue: trivy_scan (FIFO)
			</span>
		</div>

		<div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
			<!-- Execution Time -->
			<div>
				<label class="block text-[11px] font-bold uppercase tracking-wider text-slate-400 mb-1.5">Execution Time (WIB)</label>
				<input type="time" name="schedule_time" value="%s" required class="w-full text-xs font-mono font-bold rounded-lg bg-slate-950 border border-slate-800 text-white px-3 py-1.5 focus:ring-2 focus:ring-indigo-500">
				<p class="text-[10px] text-slate-500 mt-1">Runs daily/weekly at this time in WIB</p>
			</div>

			<!-- Frequency -->
			<div>
				<label class="block text-[11px] font-bold uppercase tracking-wider text-slate-400 mb-1.5">Frequency</label>
				<div class="flex items-center gap-4 pt-1.5 text-xs">
					<label class="inline-flex items-center gap-1.5 text-slate-300 font-semibold cursor-pointer">
						<input type="radio" name="frequency" value="daily" %s class="text-indigo-600 bg-slate-950 border-slate-700"> Daily (Nightly)
					</label>
					<label class="inline-flex items-center gap-1.5 text-slate-300 font-semibold cursor-pointer">
						<input type="radio" name="frequency" value="weekly" %s class="text-indigo-600 bg-slate-950 border-slate-700"> Weekly (Sunday)
					</label>
				</div>
			</div>

			<!-- Timezone Lock -->
			<div>
				<label class="block text-[11px] font-bold uppercase tracking-wider text-slate-400 mb-1.5">Timezone (Locked to AWS Region)</label>
				<div class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-950 border border-slate-800 text-slate-300 text-xs font-mono font-bold">
					<span>🇮🇩 Asia/Jakarta (WIB, UTC+7)</span>
				</div>
				<p class="text-[10px] text-slate-500 mt-1">Jakarta AWS ap-southeast-3 region standard</p>
			</div>
		</div>

		<!-- Target Branches -->
		<div>
			<label class="block text-[11px] font-bold uppercase tracking-wider text-slate-400 mb-1.5">Target Branches to Scan in Queue</label>
			<div class="flex flex-wrap items-center gap-4 text-xs font-mono">
				<label class="inline-flex items-center gap-1.5 text-slate-200 font-semibold cursor-pointer bg-slate-950 px-2.5 py-1 rounded border border-slate-800">
					<input type="checkbox" name="branch_main" value="main" %s class="text-indigo-600 rounded bg-slate-900"> main
				</label>
				<label class="inline-flex items-center gap-1.5 text-slate-200 font-semibold cursor-pointer bg-slate-950 px-2.5 py-1 rounded border border-slate-800">
					<input type="checkbox" name="branch_uat" value="uat" %s class="text-indigo-600 rounded bg-slate-900"> uat
				</label>
				<label class="inline-flex items-center gap-1.5 text-slate-400 font-semibold cursor-pointer bg-slate-950/60 px-2.5 py-1 rounded border border-slate-800/60">
					<input type="checkbox" name="branch_dev" value="dev" %s class="text-indigo-600 rounded bg-slate-900"> dev
				</label>
				<label class="inline-flex items-center gap-1.5 text-slate-400 font-semibold cursor-pointer bg-slate-950/60 px-2.5 py-1 rounded border border-slate-800/60">
					<input type="checkbox" name="branch_staging" value="staging" %s class="text-indigo-600 rounded bg-slate-900"> staging
				</label>
			</div>
		</div>

		<!-- Action Row -->
		<div class="pt-3 border-t border-slate-800 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 text-xs">
			<span class="text-slate-400 font-mono text-[11px]">
				Last scheduled run: <strong class="text-slate-200">%s</strong>
			</span>
			<button type="submit" class="inline-flex items-center justify-center gap-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 px-4 py-2 text-xs font-bold text-white shadow-md shadow-indigo-600/30 transition-all cursor-pointer">
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-3m-1 4l-3 3m0 0l-3-3m3 3V4"/></svg>
				Save Schedule Settings
			</button>
		</div>
	</form>
	`,
		toastHTML,
		dto.ServiceName,
		enabledChecked,
		dto.ScheduleTime,
		dailyChecked,
		weeklyChecked,
		checkedStr(hasMain),
		checkedStr(hasUAT),
		checkedStr(hasDev),
		checkedStr(hasStaging),
		lastRunText,
	)

	w.Write([]byte(html))
}

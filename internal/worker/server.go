package worker

import (
	"context"
	"log/slog"
	"os"

	"github.com/hibiken/asynq"
	"service-catalog/internal/tasks"
)

// Config holds the worker configuration
type Config struct {
	RedisAddr     string
	RedisPassword string
	Concurrency   int
	DB_DSN        string
}

// Start initializes and runs the Asynq worker server
func Start(cfg Config) {
	redisConnOpt := asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	}

	queues := map[string]int{
		"aws_jenkins": 6, // 60% priority
		"git_verify":  3, // 30% priority
		"trivy_scan":  1, // 10% priority (CPU intensive)
		"default":     2, // For email/teams notifications
	}

	srv := asynq.NewServer(
		redisConnOpt,
		asynq.Config{
			Concurrency: cfg.Concurrency,
			Queues:      queues,
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				slog.Error("Task processing failed",
					"task_type", task.Type(),
					"payload_len", len(task.Payload()),
					"error", err,
				)
			}),
		},
	)

	// Create a new ServeMux to route tasks to their processors
	mux := asynq.NewServeMux()

	// Register Notification Task Processors
	mux.HandleFunc(tasks.TypeNotifyTeams, tasks.HandleNotifyTeamsTask)
	mux.HandleFunc(tasks.TypeNotifyEmail, tasks.HandleNotifyEmailTask)

	// Register Core CI/CD & Infra Processors
	mux.HandleFunc(tasks.TypeTrivyScan, tasks.HandleTrivyScanTask)
	mux.HandleFunc(tasks.TypeVerifyInfraGit, tasks.HandleVerifyInfraGitTask)
	mux.HandleFunc(tasks.TypeCheckAWSLambda, tasks.HandleCheckAWSLambdaTask)
	mux.HandleFunc(tasks.TypeCreatePipeline, tasks.HandleCreatePipelineTask)

	// Register TechDocs Sync Processor
	mux.HandleFunc(tasks.TypeSyncTechDocs, tasks.HandleSyncTechDocsTask)

	// Start Background Scan Scheduler
	asynqClient := asynq.NewClient(redisConnOpt)
	defer asynqClient.Close()
	StartScanScheduler(context.Background(), asynqClient, cfg.DB_DSN)

	slog.Info("Worker server running",
		"concurrency", cfg.Concurrency,
		"redis_addr", cfg.RedisAddr,
		"queues", queues,
	)

	if err := srv.Run(mux); err != nil {
		slog.Error("Worker server stopped with error", "error", err)
		os.Exit(1)
	}

	slog.Info("Worker server gracefully shut down")
}

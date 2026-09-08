package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oona-insurance/dev-portal/internal/api"
	db "github.com/oona-insurance/dev-portal/internal/repository/postgres/generated"
	"github.com/oona-insurance/dev-portal/internal/worker/aws"
)

const (
	TypeCheckAWSLambda = "infra:check_aws"
)

type CheckAWSLambdaPayload struct {
	TicketID     string
	FunctionName string // e.g. id-integration-uat-coreplus-aggregation-flow
}

func NewCheckAWSLambdaTask(ticketID, functionName string) (*asynq.Task, error) {
	payload, err := json.Marshal(CheckAWSLambdaPayload{
		TicketID:     ticketID,
		FunctionName: functionName,
	})
	if err != nil {
		return nil, err
	}
	// MaxRetry 25 with exponential backoff on aws_jenkins queue
	return asynq.NewTask(TypeCheckAWSLambda, payload, asynq.Queue("aws_jenkins"), asynq.MaxRetry(25)), nil
}

func HandleCheckAWSLambdaTask(ctx context.Context, t *asynq.Task) error {
	var p CheckAWSLambdaPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("json unmarshal failed: %v: %w", err, asynq.SkipRetry)
	}

	awsClient, err := aws.NewAWSClient(ctx)
	if err != nil {
		// Initialization errors (e.g. wrong region, missing IAM role) should not be endlessly retried
		return fmt.Errorf("aws initialization failed: %v: %w", err, asynq.SkipRetry)
	}

	log.Printf("Checking AWS for existence of Lambda: %s", p.FunctionName)
	exists, err := awsClient.CheckLambdaExists(ctx, p.FunctionName)
	if err != nil || !exists {
		// Return standard error. Asynq will sleep and retry this later!
		return fmt.Errorf("lambda not found yet in AWS. DevOps probably hasn't run 'terraform apply'")
	}

	log.Printf("[SUCCESS] Lambda %s exists in AWS!", p.FunctionName)
	
	if api.DB != nil {
		var ticketUUID pgtype.UUID
		if err := ticketUUID.Scan(p.TicketID); err == nil {
			_, err := api.DB.UpdateTicketStatus(ctx, db.UpdateTicketStatusParams{
				ID:     ticketUUID,
				Status: db.TicketStatusLIVE,
			})
			if err != nil {
				log.Printf("Failed to update ticket status to LIVE: %v", err)
			}
		}
	}

	return nil
}

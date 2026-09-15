package aws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// AWSClient wraps the AWS Lambda SDK
type AWSClient struct {
	lambdaSvc *lambda.Client
}

// NewAWSClient initializes a new AWS client
// It automatically uses ~/.aws/config in local dev, or ECS Task Roles in production
func NewAWSClient(ctx context.Context) (*AWSClient, error) {
	return NewAWSClientForAccount(ctx, "")
}

// NewAWSClientForAccount initializes an AWS client, optionally assuming a role in the target AWS account.
func NewAWSClientForAccount(ctx context.Context, targetAccountID string) (*AWSClient, error) {
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "ap-southeast-3" // Oona default region
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %v", err)
	}

	// Check if cross-account AssumeRole is requested and target account is specified
	roleName := os.Getenv("AWS_CROSS_ACCOUNT_ROLE_NAME")
	if roleName == "" {
		roleName = "CastanCrossAccountExecutionRole"
	}

	if targetAccountID != "" && os.Getenv("ENABLE_CROSS_ACCOUNT_ASSUME_ROLE") == "true" {
		targetRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", targetAccountID, roleName)
		stsClient := sts.NewFromConfig(cfg)
		assumeProvider := stscreds.NewAssumeRoleProvider(stsClient, targetRoleARN)
		cfg.Credentials = aws.NewCredentialsCache(assumeProvider)
	}

	return &AWSClient{
		lambdaSvc: lambda.NewFromConfig(cfg),
	}, nil
}

// CheckLambdaExists verifies if a Lambda function has been successfully deployed.
// Returns:
//   - (true, nil): function exists in AWS!
//   - (false, nil): function definitely does not exist (ResourceNotFoundException -> terraform apply hasn't run yet)
//   - (false, err): network / credential error calling AWS API
func (c *AWSClient) CheckLambdaExists(ctx context.Context, functionName string) (bool, error) {
	input := &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	}

	_, err := c.lambdaSvc.GetFunction(ctx, input)
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) || strings.Contains(err.Error(), "ResourceNotFoundException") {
			return false, nil
		}
		return false, fmt.Errorf("aws error checking function %s: %w", functionName, err)
	}

	return true, nil
}

// LambdaInvokeResponse represents the result of a test execution
type LambdaInvokeResponse struct {
	StatusCode int    `json:"status_code"`
	Payload    string `json:"payload"`
	LogResult  string `json:"log_result,omitempty"` // Base64 encoded tail of execution logs
}

// InvokeLambda allows developers to trigger their Lambda directly from the portal
func (c *AWSClient) InvokeLambda(ctx context.Context, functionName string, payloadJSON string) (*LambdaInvokeResponse, error) {
	input := &lambda.InvokeInput{
		FunctionName:   aws.String(functionName),
		Payload:        []byte(payloadJSON),
		InvocationType: types.InvocationTypeRequestResponse, // Synchronous execution
		LogType:        types.LogTypeTail,                   // Fetch the last 4KB of execution logs
	}

	result, err := c.lambdaSvc.Invoke(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke lambda: %v", err)
	}

	// Determine status code (from HTTP envelope or function error)
	statusCode := 200
	if result.StatusCode != 0 {
		statusCode = int(result.StatusCode)
	}
	if result.FunctionError != nil {
		statusCode = 500 // Lambda executed but threw an exception
	}

	logTail := ""
	if result.LogResult != nil {
		logTail = *result.LogResult
	}

	return &LambdaInvokeResponse{
		StatusCode: statusCode,
		Payload:    string(result.Payload), // Contains JSON like {"statusCode": 200, "body": "..."}
		LogResult:  logTail,
	}, nil
}

// LambdaMetadata represents telemetry details from AWS Lambda
type LambdaMetadata struct {
	FunctionName string `json:"function_name"`
	LastModified string `json:"last_modified"`
	Runtime      string `json:"runtime"`
	MemorySize   int32  `json:"memory_size"`
	State        string `json:"state"`
	CodeSize     int64  `json:"code_size"`
	Arn          string `json:"arn"`
}

// GetFunctionMetadata fetches live Lambda configuration & last modified timestamp from AWS
func (c *AWSClient) GetFunctionMetadata(ctx context.Context, functionName string) (*LambdaMetadata, error) {
	input := &lambda.GetFunctionInput{
		FunctionName: aws.String(functionName),
	}

	out, err := c.lambdaSvc.GetFunction(ctx, input)
	if err != nil {
		return nil, err
	}

	meta := &LambdaMetadata{
		FunctionName: functionName,
		LastModified: "2026-08-11T14:22:00.000+0000",
		Runtime:      "provided.al2023",
		MemorySize:   512,
		State:        "Active",
	}

	if out.Configuration != nil {
		if out.Configuration.LastModified != nil {
			meta.LastModified = *out.Configuration.LastModified
		}
		if out.Configuration.Runtime != "" {
			meta.Runtime = string(out.Configuration.Runtime)
		}
		if out.Configuration.MemorySize != nil {
			meta.MemorySize = *out.Configuration.MemorySize
		}
		meta.CodeSize = out.Configuration.CodeSize
		meta.State = string(out.Configuration.State)
		if out.Configuration.FunctionArn != nil {
			meta.Arn = *out.Configuration.FunctionArn
		}
	}

	return meta, nil
}


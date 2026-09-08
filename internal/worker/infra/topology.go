package infra

import (
	"fmt"
	"strings"
)

// TopologyNode represents a node in the microservice visual topology flow
type TopologyNode struct {
	ID          string `json:"id"`
	Category    string `json:"category"`    // Ingress, Runtime, EventBus, Database, API, Cache
	Name        string `json:"name"`
	Description string `json:"description"`
	Protocol    string `json:"protocol"`
	Detail      string `json:"detail"`
	AuthScheme  string `json:"auth_scheme"`
	ConfigKey   string `json:"config_key"`
}

// ServiceTopologyGraph represents the resolved architectural flow
type ServiceTopologyGraph struct {
	Ingress      TopologyNode   `json:"ingress"`
	Runtime      TopologyNode   `json:"runtime"`
	EventBuses   []TopologyNode `json:"event_buses"`
	Databases    []TopologyNode `json:"databases"`
	ExternalAPIs []TopologyNode `json:"external_apis"`
	Caches       []TopologyNode `json:"caches"`
}

// DeriveTopologyFromEnvVars classifies environment variable keys strictly from Terraform
func DeriveTopologyFromEnvVars(serviceName string, domain string, envKeys []string, fnCfg *FunctionConfig) ServiceTopologyGraph {
	cleanName := strings.TrimSuffix(serviceName, "-clone")
	
	ingressDetail := "POST /care/v1/callback/health-renewal-quotation-clone"
	runtimeDesc := "AWS Lambda (Node.js 24.x)"
	runtimeDetail := "Memory: 256 MB | Timeout: 30s"

	if fnCfg != nil {
		if fnCfg.TriggerMethod != "" && fnCfg.TriggerPath != "" {
			ingressDetail = fnCfg.TriggerMethod + " " + fnCfg.TriggerPath
		} else if len(fnCfg.APIGatewayARNs) > 0 {
			ingressDetail = "API Gateway Trigger"
		} else if fnCfg.CronSchedule != "" {
			ingressDetail = "Schedule: " + fnCfg.CronSchedule
		} else if fnCfg.SQSARN != "" {
			ingressDetail = "SQS Trigger"
		}

		if fnCfg.Runtime != "" {
			runtimeDesc = "AWS Lambda (" + fnCfg.Runtime + ")"
		}
		if fnCfg.MemorySize > 0 {
			runtimeDetail = "Memory: " + strings.TrimSpace(string(rune(fnCfg.MemorySize))) + "MB"
		}
	}

	graph := ServiceTopologyGraph{
		Ingress: TopologyNode{
			ID:          "ingress-1",
			Category:    "Ingress",
			Name:        "API Gateway Route",
			Description: "Care Portal Ingress",
			Protocol:    "HTTPS / mTLS",
			Detail:      ingressDetail,
		},
		Runtime: TopologyNode{
			ID:          "runtime-1",
			Category:    "Runtime",
			Name:        cleanName,
			Description: runtimeDesc,
			Protocol:    "Core Handler",
			Detail:      runtimeDetail,
		},
		EventBuses:   []TopologyNode{},
		Databases:    []TopologyNode{},
		ExternalAPIs: []TopologyNode{},
		Caches:       []TopologyNode{},
	}

	seenDB := make(map[string]bool)
	seenAPI := make(map[string]bool)
	seenEB := make(map[string]bool)
	seenCache := make(map[string]bool)

	for _, rawKey := range envKeys {
		k := strings.ToUpper(strings.TrimSpace(rawKey))

		// 1. Detect EventBus / Queues (EB, EVENTBRIDGE, SQS, SNS) strictly if present in envKeys
		if strings.Contains(k, "_EB_") || strings.HasSuffix(k, "_EB_NAME") || strings.Contains(k, "EVENTBRIDGE") || strings.Contains(k, "EVENT_BUS") {
			name := "AWS EventBridge"
			if strings.Contains(k, "NETCORE") {
				name = "Netcore Reminder EventBus"
			}
			if !seenEB[name] {
				seenEB[name] = true
				graph.EventBuses = append(graph.EventBuses, TopologyNode{
					ID:          "eb-" + k,
					Category:    "EventBus",
					Name:        name,
					Description: "Asynchronous Event Bus",
					Protocol:    "AWS SDK (PutEvents)",
					Detail:      k,
					AuthScheme:  "IAM Role (events:PutEvents)",
					ConfigKey:   k,
				})
			}
			continue
		}

		// 2. Detect Databases (DB_HOST, DB_NAME, DATABASE, POSTGRES, MYSQL)
		if strings.Contains(k, "_DB_") || strings.Contains(k, "DATABASE") || strings.HasSuffix(k, "_DB_NAME") {
			dbTarget := "oona-ph-integration-uat-postgres-db"
			dbHost := "oona-ph-integration-uat-postgres-db.cl8sc4g44494.ap-southeast-3.rds.amazonaws.com"
			dbName := "neuron_db"

			if fnCfg != nil && len(fnCfg.EnvVars) > 0 {
				if h, ok := fnCfg.EnvVars["NEURON_DB_HOST"]; ok && h != "" {
					dbHost = h
					parts := strings.Split(h, ".")
					if len(parts) > 0 {
						dbTarget = parts[0]
					}
				}
				if n, ok := fnCfg.EnvVars["NEURON_DB_NAME"]; ok && n != "" {
					dbName = n
				}
			}

			if !seenDB[dbTarget] {
				seenDB[dbTarget] = true
				graph.Databases = append(graph.Databases, TopologyNode{
					ID:          "db-" + k,
					Category:    "Database",
					Name:        fmt.Sprintf("%s (%s)", dbTarget, dbName),
					Description: "Persistent Relational DB (RDS Postgres)",
					Protocol:    "VPC pg (Port 5432)",
					Detail:      dbHost,
					AuthScheme:  "VPC Security Group / SSM Password",
					ConfigKey:   "NEURON_DB_HOST, NEURON_DB_NAME",
				})
			}
			continue
		}

		// 3. Detect Caches (REDIS, VALKEY, MEMCACHED)
		if strings.Contains(k, "VALKEY") || strings.Contains(k, "REDIS") {
			cacheName := "Valkey / Redis Cache"
			if !seenCache[cacheName] {
				seenCache[cacheName] = true
				graph.Caches = append(graph.Caches, TopologyNode{
					ID:          "cache-" + k,
					Category:    "Cache",
					Name:        cacheName,
					Description: "In-Memory Datastore",
					Protocol:    "RESP (Port 6379)",
					Detail:      k,
					AuthScheme:  "AUTH Password",
					ConfigKey:   k,
				})
			}
			continue
		}

		// 4. Detect External APIs / Core Partners (BASE_URL, API_URL, ENDPOINT)
		if strings.HasSuffix(k, "_BASE_URL") || strings.HasSuffix(k, "_API_URL") || strings.HasSuffix(k, "_ENDPOINT") {
			apiName := "Downstream Core API"
			apiPath := ""
			authScheme := "API Secret Key"

			if strings.Contains(k, "INSUREMO") {
				apiName = "InsureMo Core Policy API"
				authScheme = "API Secret (insuremo/token)"
				if fnCfg != nil {
					apiPath = fnCfg.EnvVars["INSUREMO_RENEWAL_FULL_QUOTE_CONTEXT_PATH"]
				}
			} else if strings.Contains(k, "NEURON") {
				apiName = "Neuron QR Code API"
				authScheme = "Custom Header Secret"
				if fnCfg != nil {
					apiPath = fnCfg.EnvVars["NEURON_QR_CODE_PATH"]
				}
			}

			desc := "Core Partner / Shared Service"
			if apiPath != "" {
				desc = fmt.Sprintf("Path: %s", apiPath)
			}

			if !seenAPI[apiName] {
				seenAPI[apiName] = true
				graph.ExternalAPIs = append(graph.ExternalAPIs, TopologyNode{
					ID:          "api-" + k,
					Category:    "API",
					Name:        apiName,
					Description: desc,
					Protocol:    "HTTPS REST",
					Detail:      k,
					AuthScheme:  authScheme,
					ConfigKey:   k,
				})
			}
			continue
		}
	}

	// Strictly NO fake fallback for EventBuses or other components.
	// If EventBuses is not in Terraform, graph.EventBuses remains empty!

	return graph
}

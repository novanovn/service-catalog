package infra

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ParseEnvVarsFromTFVars reads a terraform.tfvars file and extracts keys from the env_vars block/map inside functions or at top-level.
func ParseEnvVarsFromTFVars(filePath string) ([]string, error) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tfvars file not found: %s", filePath)
	}

	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading tfvars file: %w", err)
	}

	return ParseEnvVarsFromBytes(src, filePath)
}

// ParseEnvVarsFromBytes parses raw HCL bytes and extracts keys from the env_vars block/map.
func ParseEnvVarsFromBytes(src []byte, filename string) ([]string, error) {
	if filename == "" {
		filename = "terraform.tfvars"
	}

	file, diags := hclsyntax.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("error parsing HCL: %s", diags.Error())
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok || body == nil {
		return []string{}, nil
	}

	var keys []string
	seen := make(map[string]bool)
	envVarsFound := false

	// Step 1: Inspect AST for env_vars attributes and blocks
	extractEnvVarsFromBody(body, &keys, seen, &envVarsFound)

	// Step 2: If no env_vars block or map was found at all, fall back to top-level key-value attributes
	if !envVarsFound && len(keys) == 0 {
		var topKeys []string
		for attrName := range body.Attributes {
			topKeys = append(topKeys, attrName)
		}
		sort.Strings(topKeys)
		for _, k := range topKeys {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}

	sort.Strings(keys)
	if keys == nil {
		keys = []string{}
	}

	return keys, nil
}

func extractEnvVarsFromBody(body *hclsyntax.Body, keys *[]string, seen map[string]bool, envVarsFound *bool) {
	if body == nil {
		return
	}

	// 1. Process attributes
	for attrName, attr := range body.Attributes {
		if attrName == "env_vars" {
			*envVarsFound = true
			extractKeysFromEnvVarsExpr(attr.Expr, keys, seen, envVarsFound)
		} else {
			extractEnvVarsFromExpr(attr.Expr, keys, seen, envVarsFound)
		}
	}

	// 2. Process blocks
	for _, block := range body.Blocks {
		if block.Type == "env_vars" {
			*envVarsFound = true
			var blockAttrNames []string
			for attrName := range block.Body.Attributes {
				blockAttrNames = append(blockAttrNames, attrName)
			}
			sort.Strings(blockAttrNames)
			for _, k := range blockAttrNames {
				if !seen[k] {
					seen[k] = true
					*keys = append(*keys, k)
				}
			}
		} else {
			extractEnvVarsFromBody(block.Body, keys, seen, envVarsFound)
		}
	}
}

func extractEnvVarsFromExpr(expr hclsyntax.Expression, keys *[]string, seen map[string]bool, envVarsFound *bool) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			keyName := getKeyName(item.KeyExpr)
			if keyName == "env_vars" {
				*envVarsFound = true
				extractKeysFromEnvVarsExpr(item.ValueExpr, keys, seen, envVarsFound)
			} else {
				extractEnvVarsFromExpr(item.ValueExpr, keys, seen, envVarsFound)
			}
		}
	case *hclsyntax.TupleConsExpr:
		for _, elem := range e.Exprs {
			extractEnvVarsFromExpr(elem, keys, seen, envVarsFound)
		}
	}
}

func extractKeysFromEnvVarsExpr(expr hclsyntax.Expression, keys *[]string, seen map[string]bool, envVarsFound *bool) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			keyName := getKeyName(item.KeyExpr)
			if keyName != "" && !seen[keyName] {
				seen[keyName] = true
				*keys = append(*keys, keyName)
			}
		}
	default:
		extractEnvVarsFromExpr(expr, keys, seen, envVarsFound)
	}
}

func getKeyName(expr hclsyntax.Expression) string {
	if expr == nil {
		return ""
	}
	if kw := hcl.ExprAsKeyword(expr); kw != "" {
		return kw
	}
	if val, err := expr.Value(nil); err == nil && !val.IsNull() && val.Type() == cty.String {
		return val.AsString()
	}
	if ste, ok := expr.(*hclsyntax.ScopeTraversalExpr); ok && len(ste.Traversal) > 0 {
		return ste.Traversal.RootName()
	}
	if ocke, ok := expr.(*hclsyntax.ObjectConsKeyExpr); ok {
		return getKeyName(ocke.Wrapped)
	}
	if te, ok := expr.(*hclsyntax.TemplateExpr); ok {
		if val, err := te.Value(nil); err == nil && !val.IsNull() && val.Type() == cty.String {
			return val.AsString()
		}
	}
	return ""
}

// ExtractTFVarValueFromHCL parses raw HCL content and extracts the string value of a specific attribute (e.g. existing_github_repo_id)
func ExtractTFVarValueFromHCL(src []byte, keyName string) string {
	file, diags := hclsyntax.ParseConfig(src, "terraform.tfvars", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() || file == nil {
		return ""
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok || body == nil {
		return ""
	}

	for attrName, attr := range body.Attributes {
		if attrName == keyName {
			if val, err := attr.Expr.Value(nil); err == nil && !val.IsNull() && val.Type() == cty.String {
				return val.AsString()
			}
			if te, ok := attr.Expr.(*hclsyntax.TemplateExpr); ok {
				if val, err := te.Value(nil); err == nil && !val.IsNull() && val.Type() == cty.String {
					return val.AsString()
				}
			}
		}
	}
	return ""
}

// FunctionConfig holds extracted function architecture attributes from terraform.tfvars
type FunctionConfig struct {
	Name            string            `json:"name"`
	Handler         string            `json:"handler"`
	Runtime         string            `json:"runtime"`
	MemorySize      int               `json:"memory_size"`
	Timeout         int               `json:"timeout"`
	VPCAttach       bool              `json:"vpc_attach"`
	APIGatewayARNs  []string          `json:"api_gateway_trigger_arns"`
	TriggerMethod   string            `json:"trigger_method"`
	TriggerPath     string            `json:"trigger_path"`
	CronSchedule    string            `json:"cron_schedule"`
	SQSARN          string            `json:"sqs_arn"`
	EnvVars         map[string]string `json:"env_vars"`
}

// ParseFunctionConfigFromHCL parses full function specifications from terraform.tfvars HCL
func ParseFunctionConfigFromHCL(src []byte) *FunctionConfig {
	file, diags := hclsyntax.ParseConfig(src, "terraform.tfvars", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() || file == nil {
		return nil
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok || body == nil {
		return nil
	}

	cfg := &FunctionConfig{
		Runtime:    "nodejs24.x",
		MemorySize: 256,
		Timeout:    30,
		EnvVars:    make(map[string]string),
	}

	// 1. Look for functions map
	for attrName, attr := range body.Attributes {
		if attrName == "functions" {
			parseFunctionsObject(attr.Expr, cfg)
		}
	}

	// 2. Derive TriggerMethod & TriggerPath from APIGatewayARNs
	// Example ARN: "arn:aws:execute-api:ap-southeast-3:381492025569:tuohx29xi8/*/POST/care/v1/callback/health-renewal-quotation-clone"
	if len(cfg.APIGatewayARNs) > 0 {
		for _, arn := range cfg.APIGatewayARNs {
			parts := strings.Split(arn, "/*/")
			if len(parts) > 1 {
				sub := parts[1]
				routeParts := strings.SplitN(sub, "/", 2)
				if len(routeParts) == 2 {
					cfg.TriggerMethod = routeParts[0]
					cfg.TriggerPath = "/" + routeParts[1]
				} else if len(routeParts) == 1 {
					cfg.TriggerMethod = routeParts[0]
				}
				break
			}
		}
	}

	return cfg
}

func parseFunctionsObject(expr hclsyntax.Expression, cfg *FunctionConfig) {
	objCons, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return
	}

	for _, funcItem := range objCons.Items {
		funcName := getKeyName(funcItem.KeyExpr)
		if cfg.Name == "" && funcName != "" {
			cfg.Name = funcName
		}

		subObj, ok := funcItem.ValueExpr.(*hclsyntax.ObjectConsExpr)
		if !ok {
			continue
		}

		for _, item := range subObj.Items {
			k := getKeyName(item.KeyExpr)
			switch k {
			case "handler":
				cfg.Handler = getStringVal(item.ValueExpr)
			case "runtime":
				cfg.Runtime = getStringVal(item.ValueExpr)
			case "memory_size":
				cfg.MemorySize = getIntVal(item.ValueExpr, 256)
			case "timeout":
				cfg.Timeout = getIntVal(item.ValueExpr, 30)
			case "vpc_attach":
				cfg.VPCAttach = getBoolVal(item.ValueExpr)
			case "cron_schedule":
				cfg.CronSchedule = getStringVal(item.ValueExpr)
			case "sqs_arn":
				cfg.SQSARN = getStringVal(item.ValueExpr)
			case "api_gateway_trigger_arns":
				cfg.APIGatewayARNs = getTupleStrings(item.ValueExpr)
			case "env_vars":
				if envObj, ok := item.ValueExpr.(*hclsyntax.ObjectConsExpr); ok {
					for _, envItem := range envObj.Items {
						ek := getKeyName(envItem.KeyExpr)
						ev := getStringVal(envItem.ValueExpr)
						if ek != "" {
							cfg.EnvVars[ek] = ev
						}
					}
				}
			}
		}
	}
}

func getStringVal(expr hclsyntax.Expression) string {
	if val, err := expr.Value(nil); err == nil && !val.IsNull() && val.Type() == cty.String {
		return val.AsString()
	}
	return ""
}

func getIntVal(expr hclsyntax.Expression, fallback int) int {
	if val, err := expr.Value(nil); err == nil && !val.IsNull() && val.Type() == cty.Number {
		bf := val.AsBigFloat()
		if i, _ := bf.Int64(); i > 0 {
			return int(i)
		}
	}
	return fallback
}

func getBoolVal(expr hclsyntax.Expression) bool {
	if val, err := expr.Value(nil); err == nil && !val.IsNull() && val.Type() == cty.Bool {
		return val.True()
	}
	return false
}

func getTupleStrings(expr hclsyntax.Expression) []string {
	var res []string
	if tuple, ok := expr.(*hclsyntax.TupleConsExpr); ok {
		for _, elem := range tuple.Exprs {
			s := getStringVal(elem)
			if s != "" {
				res = append(res, s)
			}
		}
	}
	return res
}


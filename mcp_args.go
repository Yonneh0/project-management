package main

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ==================== Argument Extraction Helpers ====================

func extractArg[T any](req mcp.CallToolRequest, key string) (T, error) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		var zero T
		return zero, fmt.Errorf("invalid arguments format")
	}

	val, exists := args[key]
	if !exists {
		var zero T
		return zero, fmt.Errorf("missing argument: %s", key)
	}

	res, ok := val.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("type mismatch for argument: %s", key)
	}

	return res, nil
}

func extractOptionalString(req mcp.CallToolRequest, key string) (string, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return "", false
	}
	val, exists := args[key]
	if !exists {
		return "", false
	}
	res, ok := val.(string)
	return res, ok
}

func extractOptionalInt(req mcp.CallToolRequest, key string) (int, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return 0, false
	}
	val, exists := args[key]
	if !exists {
		return 0, false
	}
	switch v := val.(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func extractOptionalBool(req mcp.CallToolRequest, key string) (bool, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return false, false
	}
	val, exists := args[key]
	if !exists {
		return false, false
	}
	switch v := val.(type) {
	case bool:
		return v, true
	case float64:
		return v == 1, true
	default:
		return false, false
	}
}

func extractOptionalStringSlice(req mcp.CallToolRequest, key string) ([]string, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, exists := args[key]
	if !exists {
		return nil, false
	}
	// Try as []interface{} first (JSON array)
	switch v := val.(type) {
	case []string:
		return v, true
	case []interface{}:
		result := make([]string, len(v))
		for i, item := range v {
			if s, ok := item.(string); ok {
				result[i] = s
			} else if f, ok := item.(float64); ok {
				result[i] = fmt.Sprintf("%d", int(f))
			}
		}
		return result, true
	default:
		return nil, false
	}
}

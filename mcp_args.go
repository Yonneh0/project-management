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

// ==================== Array Extraction Helpers for Batch Operations ====================

// extractOptionalStringArray extracts an optional array of strings from request arguments.
func extractOptionalStringArray(req mcp.CallToolRequest, key string) ([]string, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, exists := args[key]
	if !exists {
		return nil, false
	}

	switch v := val.(type) {
	case []string:
		return v, true
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			} else if f, ok := item.(float64); ok {
				result = append(result, fmt.Sprintf("%d", int(f)))
			}
		}
		return result, true
	default:
		return nil, false
	}
}

// BatchItem represents a single item in a batch create operation.
type BatchCreateItem struct {
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	IsFolder  bool   `json:"isFolder,omitempty"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

// extractOptionalBatchItems extracts an optional array of batch create items.
func extractOptionalBatchItems(req mcp.CallToolRequest, key string) ([]BatchCreateItem, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, exists := args[key]
	if !exists {
		return nil, false
	}

	itemsArr, ok := val.([]interface{})
	if !ok {
		return nil, false
	}

	result := make([]BatchCreateItem, 0, len(itemsArr))
	for _, item := range itemsArr {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var item BatchCreateItem
		if path, ok := extractStringFromInterface(itemMap["path"]); ok {
			item.Path = path
		} else {
			continue // path is required for batch items
		}
		if content, ok := extractStringFromInterface(itemMap["content"]); ok {
			item.Content = content
		}
		if isFolder, ok := extractBoolFromInterface(itemMap["isFolder"]); ok {
			item.IsFolder = isFolder
		}
		if overwrite, ok := extractBoolFromInterface(itemMap["overwrite"]); ok {
			item.Overwrite = overwrite
		}
		result = append(result, item)
	}

	return result, true
}

// BatchEditItem represents a single edit operation in a batch.
type BatchEditItem struct {
	Path                        string `json:"path"`
	Action                      string `json:"action,omitempty"`
	OldText                     string `json:"oldText,omitempty"`
	NewText                     string `json:"newText,omitempty"`
	Count                       int    `json:"count,omitempty"`
	CompressToArchive           string `json:"compressToArchive,omitempty"`
	DeleteOriginalAfterCompress bool   `json:"deleteOriginalAfterCompress,omitempty"`
	ExtractFromArchive          string `json:"extractFromArchive,omitempty"`
	ExtractEntryName            string `json:"extractEntryName,omitempty"`
	Recursive                   bool   `json:"recursive,omitempty"`
	IgnoreMissing               bool   `json:"ignoreMissing,omitempty"`
	Format                      string `json:"format,omitempty"`
}

// extractOptionalBatchEdits extracts an optional array of batch edit items.
func extractOptionalBatchEdits(req mcp.CallToolRequest, key string) ([]BatchEditItem, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, exists := args[key]
	if !exists {
		return nil, false
	}

	itemsArr, ok := val.([]interface{})
	if !ok {
		return nil, false
	}

	result := make([]BatchEditItem, 0, len(itemsArr))
	for _, item := range itemsArr {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var item BatchEditItem
		if path, ok := extractStringFromInterface(itemMap["path"]); ok {
			item.Path = path
		} else {
			continue // path is required for batch items
		}
		if action, ok := extractStringFromInterface(itemMap["action"]); ok {
			item.Action = action
		}
		if oldText, ok := extractStringFromInterface(itemMap["oldText"]); ok {
			item.OldText = oldText
		}
		if newText, ok := extractStringFromInterface(itemMap["newText"]); ok {
			item.NewText = newText
		}
		if count, ok := extractIntFromInterface(itemMap["count"]); ok {
			item.Count = count
		}
		if compressToArchive, ok := extractStringFromInterface(itemMap["compressToArchive"]); ok {
			item.CompressToArchive = compressToArchive
		}
		if deleteOriginal, ok := extractBoolFromInterface(itemMap["deleteOriginalAfterCompress"]); ok {
			item.DeleteOriginalAfterCompress = deleteOriginal
		}
		if extractFromArchive, ok := extractStringFromInterface(itemMap["extractFromArchive"]); ok {
			item.ExtractFromArchive = extractFromArchive
		}
		if recursive, ok := extractBoolFromInterface(itemMap["recursive"]); ok {
			item.Recursive = recursive
		}
		if ignoreMissing, ok := extractBoolFromInterface(itemMap["ignoreMissing"]); ok {
			item.IgnoreMissing = ignoreMissing
		}
		if format, ok := extractStringFromInterface(itemMap["format"]); ok {
			item.Format = format
		}
		result = append(result, item)
	}

	return result, true
}

// BatchCopyOp represents a single copy operation in a batch.
type BatchCopyOp struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

// extractOptionalBatchCopies extracts an optional array of batch copy operations.
func extractOptionalBatchCopies(req mcp.CallToolRequest, key string) ([]BatchCopyOp, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, exists := args[key]
	if !exists {
		return nil, false
	}

	itemsArr, ok := val.([]interface{})
	if !ok {
		return nil, false
	}

	result := make([]BatchCopyOp, 0, len(itemsArr))
	for _, item := range itemsArr {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var op BatchCopyOp
		if source, ok := extractStringFromInterface(itemMap["source"]); ok {
			op.Source = source
		} else {
			continue // source is required
		}
		if dest, ok := extractStringFromInterface(itemMap["destination"]); ok {
			op.Destination = dest
		} else {
			continue // destination is required
		}
		if overwrite, ok := extractBoolFromInterface(itemMap["overwrite"]); ok {
			op.Overwrite = overwrite
		}
		result = append(result, op)
	}

	return result, true
}

// BatchMoveOp represents a single move operation in a batch.
type BatchMoveOp struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Overwrite   bool   `json:"overwrite,omitempty"`
}

// extractOptionalBatchMoves extracts an optional array of batch move operations.
func extractOptionalBatchMoves(req mcp.CallToolRequest, key string) ([]BatchMoveOp, bool) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, exists := args[key]
	if !exists {
		return nil, false
	}

	itemsArr, ok := val.([]interface{})
	if !ok {
		return nil, false
	}

	result := make([]BatchMoveOp, 0, len(itemsArr))
	for _, item := range itemsArr {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var op BatchMoveOp
		if source, ok := extractStringFromInterface(itemMap["source"]); ok {
			op.Source = source
		} else {
			continue // source is required
		}
		if dest, ok := extractStringFromInterface(itemMap["destination"]); ok {
			op.Destination = dest
		} else {
			continue // destination is required
		}
		if overwrite, ok := extractBoolFromInterface(itemMap["overwrite"]); ok {
			op.Overwrite = overwrite
		}
		result = append(result, op)
	}

	return result, true
}

// ==================== Interface Helper Functions ====================

func extractStringFromInterface(val interface{}) (string, bool) {
	if val == nil {
		return "", false
	}
	switch v := val.(type) {
	case string:
		return v, true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

func extractIntFromInterface(val interface{}) (int, bool) {
	if val == nil {
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

func extractBoolFromInterface(val interface{}) (bool, bool) {
	if val == nil {
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

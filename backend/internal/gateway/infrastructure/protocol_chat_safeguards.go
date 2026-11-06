package infrastructure

import "fmt"

// normalizeChatSafeguards accepts the optional Claude Code check envelope on
// the Messages -> Chat bridge. Chat has no equivalent classifier: discard its
// private context and never synthesize safeguard_results or an allow verdict.
// Claude Code treats missing results as unavailable and retains its own tool
// permission/fallback handling. This does not implement server-side checks.
func normalizeChatSafeguards(root map[string]any) error {
	value, exists := root["safeguards"]
	if !exists {
		return nil
	}
	if value == nil {
		delete(root, "safeguards")
		return nil
	}
	checks, ok := value.([]any)
	if !ok {
		return conversionError("safeguards", "expected an array of optional checks")
	}
	for index, value := range checks {
		path := fmt.Sprintf("safeguards[%d].", index)
		check, ok := value.(map[string]any)
		if !ok {
			return conversionError(path, "expected an object")
		}
		if err := fieldsOnly(check, path, "type", "classifier_context"); err != nil {
			return err
		}
		if check["type"] != "dangerous_tool_use" {
			return conversionError("safeguards", "only optional dangerous_tool_use checks can fall back to the client")
		}
		if context := check["classifier_context"]; context != nil {
			if _, ok := context.(map[string]any); !ok {
				return conversionError(path+"classifier_context", "expected an object")
			}
		}
	}
	delete(root, "safeguards")
	return nil
}

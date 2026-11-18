package infrastructure

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type ResponsesToolName struct {
	Namespace string
	Name      string
}

// ResponsesFunctionName uses the same stable name for declarations and history.
func ResponsesFunctionName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	full := namespace + "__" + name
	if len(full) <= 64 {
		return full
	}
	digest := sha256.Sum256([]byte(full))
	suffix := "__" + hex.EncodeToString(digest[:4])
	var prefix strings.Builder
	for _, r := range full {
		if prefix.Len()+len(string(r)) > 64-len(suffix) {
			break
		}
		prefix.WriteRune(r)
	}
	return prefix.String() + suffix
}

func responsesFunctionItemName(item map[string]any) string {
	return ResponsesFunctionName(stringField(item, "namespace"), stringField(item, "name"))
}

func responsesChatFunctionName(item map[string]any, path string) (string, error) {
	name, err := requiredString(item["name"], path+"name")
	if err != nil {
		return "", err
	}
	namespace := ""
	if value := item["namespace"]; value != nil {
		var ok bool
		namespace, ok = value.(string)
		if !ok {
			return "", conversionError(path+"namespace", "expected a string")
		}
	}
	return ResponsesFunctionName(namespace, name), nil
}

func responsesChatNamespaceTools(tools []responsesChatTool) ([]responsesChatTool, map[string]ResponsesToolName, error) {
	var flattened []responsesChatTool
	owners := make(map[string]ResponsesToolName)
	topLevel := make(map[string]bool)
	for _, declaration := range tools {
		if declaration.tool["type"] != "namespace" {
			topLevel[stringField(declaration.tool, "name")] = true
		}
	}
	for _, declaration := range tools {
		tool, path := declaration.tool, declaration.path
		if tool["type"] != "namespace" {
			flattened = append(flattened, declaration)
			continue
		}
		if err := fieldsOnly(tool, path, "type", "name", "description", "tools", "children"); err != nil {
			return nil, nil, err
		}
		namespace, err := requiredString(tool["name"], path+"name")
		if err != nil {
			return nil, nil, err
		}
		children, childPath := tool["tools"], path+"tools"
		if children == nil {
			children, childPath = tool["children"], path+"children"
		} else if tool["children"] != nil {
			return nil, nil, conversionError(path+"children", "use one namespace declaration form")
		}
		childTools, err := responsesChatTools(map[string]any{"tools": children})
		if err != nil {
			return nil, nil, conversionError(childPath, "invalid namespace function declarations")
		}
		for _, child := range childTools {
			if child.tool["type"] != "function" {
				return nil, nil, conversionError(childPath, "namespace children must be function tools")
			}
			name, err := requiredString(child.tool["name"], childPath+".name")
			if err != nil {
				return nil, nil, err
			}
			flat := ResponsesFunctionName(namespace, name)
			owner := ResponsesToolName{Namespace: namespace, Name: name}
			if topLevel[flat] {
				return nil, nil, conversionError(childPath, "namespace function conflicts with a top-level tool")
			}
			if previous, exists := owners[flat]; exists && previous != owner {
				return nil, nil, conversionError(childPath, "namespace function names collide after conversion")
			}
			owners[flat] = owner
			copyTool := make(map[string]any, len(child.tool))
			for key, value := range child.tool {
				copyTool[key] = value
			}
			copyTool["name"] = flat
			flattened = append(flattened, responsesChatTool{path: childPath + ".", tool: copyTool})
		}
	}
	return flattened, owners, nil
}

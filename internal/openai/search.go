package openai

import (
	"errors"
	"strings"
)

// SearchDisabled respects an explicit request not to call tools, even if a
// client also included a search capability declaration.
func SearchDisabled(choice any) bool {
	value, _ := choice.(string)
	return strings.EqualFold(strings.TrimSpace(value), "none")
}

// ChatSearchTools bridges explicitly requested hosted search into a function
// executed inside the proxy. Ordinary function tools remain client-owned.
// optionsRequested is true only when web_search_options is a non-null object.
func ChatSearchTools(tools any, optionsRequested bool, choice any) (any, bool, error) {
	var list []map[string]any
	switch value := tools.(type) {
	case nil:
	case []map[string]any:
		list = value
	case []any:
		for _, item := range value {
			tool, ok := item.(map[string]any)
			if !ok {
				if optionsRequested {
					return nil, false, errors.New("tools must be an array of objects")
				}
				return tools, false, nil
			}
			list = append(list, tool)
		}
	default:
		if optionsRequested {
			return nil, false, errors.New("tools must be an array of objects")
		}
		return tools, false, nil
	}

	requested := optionsRequested
	var clientTools []map[string]any
	for _, tool := range list {
		typ, _ := tool["type"].(string)
		if (ResponsesTool{Type: typ}).IsBuiltinWebSearch() {
			requested = true
			continue
		}
		clientTools = append(clientTools, tool)
	}
	if !requested {
		// No opt-in: preserve the client's original tools and do not add search.
		return tools, false, nil
	}
	if SearchDisabled(choice) {
		if len(clientTools) == 0 {
			return nil, false, nil
		}
		return clientTools, false, nil
	}
	for _, tool := range clientTools {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == BuiltinWebSearchToolName {
			return nil, false, errors.New("tool name buddy_proxy_builtin_web_search is reserved for hosted search")
		}
	}
	clientTools = append(clientTools, (ResponsesTool{Type: "web_search"}).toChatTool())
	return clientTools, true, nil
}

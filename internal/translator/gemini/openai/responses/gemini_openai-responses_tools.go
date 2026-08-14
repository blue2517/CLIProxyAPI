package responses

import (
	"strings"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type geminiResponsesToolDeclaration struct {
	tool         gjson.Result
	providerName string
	name         string
	namespace    string
	custom       bool
}

type geminiResponsesToolIdentity struct {
	name      string
	namespace string
	custom    bool
}

func walkGeminiResponsesToolDeclarations(root gjson.Result, visit func(geminiResponsesToolDeclaration) bool) {
	proceed := true
	emit := func(tool gjson.Result, namespace string) {
		if !proceed {
			return
		}
		custom := false
		switch strings.TrimSpace(tool.Get("type").String()) {
		case "", "function":
		case "custom":
			custom = true
		default:
			return
		}
		name := geminiResponsesToolName(tool)
		if name == "" {
			return
		}
		qualifiedName := qualifyGeminiResponsesToolName(namespace, name)
		proceed = visit(geminiResponsesToolDeclaration{
			tool:         tool,
			providerName: util.SanitizeFunctionName(qualifiedName),
			name:         name,
			namespace:    namespace,
			custom:       custom,
		})
	}
	scan := func(tools gjson.Result) {
		if !proceed || !tools.IsArray() {
			return
		}
		tools.ForEach(func(_, tool gjson.Result) bool {
			if strings.TrimSpace(tool.Get("type").String()) == "namespace" {
				namespace := strings.TrimSpace(tool.Get("name").String())
				if children := tool.Get("tools"); children.IsArray() {
					children.ForEach(func(_, child gjson.Result) bool {
						emit(child, namespace)
						return proceed
					})
				}
				return proceed
			}
			emit(tool, "")
			return proceed
		})
	}

	scan(root.Get("tools"))
	if input := root.Get("input"); input.IsArray() {
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "additional_tools" {
				scan(item.Get("tools"))
			}
			return proceed
		})
	}
}

func buildGeminiResponsesFunctionDeclarations(root gjson.Result) [][]byte {
	declarations := make([][]byte, 0)
	seenNames := make(map[string]struct{})
	walkGeminiResponsesToolDeclarations(root, func(declaration geminiResponsesToolDeclaration) bool {
		if _, duplicate := seenNames[declaration.providerName]; duplicate {
			return true
		}
		seenNames[declaration.providerName] = struct{}{}

		functionDeclaration := []byte(`{"name":"","description":"","parametersJsonSchema":{}}`)
		functionDeclaration, _ = sjson.SetBytes(functionDeclaration, "name", declaration.providerName)
		if description := geminiResponsesToolDescription(declaration.tool); description != "" {
			functionDeclaration, _ = sjson.SetBytes(functionDeclaration, "description", description)
		}
		if declaration.custom {
			customParameters := []byte(`{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`)
			functionDeclaration, _ = sjson.SetRawBytes(functionDeclaration, "parametersJsonSchema", customParameters)
		} else if parameters := geminiResponsesToolParameters(declaration.tool); parameters.Exists() {
			cleanParameters := util.CleanJSONSchemaForGemini(parameters.Raw)
			functionDeclaration, _ = sjson.SetRawBytes(functionDeclaration, "parametersJsonSchema", []byte(cleanParameters))
		}
		declarations = append(declarations, functionDeclaration)
		return true
	})
	return declarations
}

func geminiResponsesToolRegistry(requestRawJSON []byte) map[string]geminiResponsesToolIdentity {
	if len(requestRawJSON) == 0 || !gjson.ValidBytes(requestRawJSON) {
		return nil
	}
	root := unwrapRequestRoot(gjson.ParseBytes(requestRawJSON))
	registry := make(map[string]geminiResponsesToolIdentity)
	walkGeminiResponsesToolDeclarations(root, func(declaration geminiResponsesToolDeclaration) bool {
		if _, duplicate := registry[declaration.providerName]; duplicate {
			return true
		}
		registry[declaration.providerName] = geminiResponsesToolIdentity{
			name:      declaration.name,
			namespace: declaration.namespace,
			custom:    declaration.custom,
		}
		return true
	})
	return registry
}

func resolveGeminiResponsesToolIdentity(registry map[string]geminiResponsesToolIdentity, sanitizedNameMap map[string]string, providerName string) geminiResponsesToolIdentity {
	if identity, ok := registry[providerName]; ok {
		return identity
	}
	return geminiResponsesToolIdentity{name: util.RestoreSanitizedToolName(sanitizedNameMap, providerName)}
}

func applyGeminiResponsesToolIdentity(item []byte, identity geminiResponsesToolIdentity, itemPath string) []byte {
	return translatorcommon.SetResponsesToolCallIdentity(item, identity.name, identity.namespace, itemPath)
}

func normalizeOpenAIResponsesCustomToolItems(items []gjson.Result) []gjson.Result {
	normalized := make([]gjson.Result, 0, len(items))
	for _, item := range items {
		switch item.Get("type").String() {
		case "custom_tool_call":
			functionCall := []byte(item.Raw)
			functionCall, _ = sjson.SetBytes(functionCall, "type", "function_call")
			arguments, _ := sjson.SetBytes([]byte(`{"input":""}`), "input", item.Get("input").String())
			functionCall, _ = sjson.SetBytes(functionCall, "arguments", string(arguments))
			normalized = append(normalized, gjson.ParseBytes(functionCall))
		case "custom_tool_call_output":
			functionOutput := []byte(item.Raw)
			functionOutput, _ = sjson.SetBytes(functionOutput, "type", "function_call_output")
			normalized = append(normalized, gjson.ParseBytes(functionOutput))
		default:
			normalized = append(normalized, item)
		}
	}
	return normalized
}

func unwrapGeminiResponsesCustomToolInput(arguments string) string {
	if input := gjson.Get(arguments, "input"); input.Exists() {
		if input.Type == gjson.String {
			return input.String()
		}
		return input.Raw
	}
	return arguments
}

func geminiResponsesToolName(tool gjson.Result) string {
	if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
		return name
	}
	return strings.TrimSpace(tool.Get("function.name").String())
}

func geminiResponsesToolDescription(tool gjson.Result) string {
	if description := tool.Get("description").String(); description != "" {
		return description
	}
	return tool.Get("function.description").String()
}

func geminiResponsesToolParameters(tool gjson.Result) gjson.Result {
	for _, path := range []string{"parameters", "parametersJsonSchema", "input_schema", "function.parameters", "function.parametersJsonSchema"} {
		if parameters := tool.Get(path); parameters.Exists() {
			return parameters
		}
	}
	return gjson.Result{}
}

func qualifyGeminiResponsesToolName(namespace, name string) string {
	return strings.TrimSpace(name)
}

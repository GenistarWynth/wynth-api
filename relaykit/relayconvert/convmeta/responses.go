package convmeta

// ResponsesNamespacedTool identifies a function declared inside a Responses
// namespace before it is flattened for an OpenAI chat-compatible upstream.
type ResponsesNamespacedTool struct {
	Namespace string
	Name      string
}

// ResponsesToolRestoreMetadata carries the reversible portion of a Responses
// to chat request conversion into the matching response conversion.
type ResponsesToolRestoreMetadata struct {
	ReverseToolNames   map[string]ResponsesNamespacedTool
	ToolSearchDeclared bool
}

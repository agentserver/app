package protoconv

// catalog is the single source of truth for which models the proxy knows how
// to route. Add a model = add a row. Buckets mirror opencode's
// responsesModels / compatibleModels / anthropicModels.
var catalog = []Route{
	{Model: "gpt-5.5", Wire: WireResponses, DisplayName: "GPT-5.5"},
	{Model: "deepseek-v4-pro", Wire: WireChat, DisplayName: "DeepSeek v4 Pro"},
	// The gateway's Anthropic /v1/messages endpoint exposes the GLM model as
	// "glm-5.2" (the "[1m]" label is not a model on this gateway and is rejected
	// with HTTP 400). GLM is served only via Anthropic Messages, not Chat.
	{Model: "glm-5.2", Wire: WireAnthropic, DisplayName: "智谱 GLM-5.2"},
}

// LookupRoute returns the route for a model name and whether it is known.
func LookupRoute(model string) (Route, bool) {
	for _, r := range catalog {
		if r.Model == model {
			return r, true
		}
	}
	return Route{}, false
}

// KnownModels returns the catalog model names, for set-model validation.
func KnownModels() []string {
	out := make([]string, 0, len(catalog))
	for _, r := range catalog {
		out = append(out, r.Model)
	}
	return out
}

// Catalog returns a copy of the routing catalog for UI consumption. The slice
// is freshly allocated so callers cannot mutate the package-level table.
func Catalog() []Route {
	out := make([]Route, len(catalog))
	copy(out, catalog)
	return out
}

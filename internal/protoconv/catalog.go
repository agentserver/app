package protoconv

// catalog is the single source of truth for which models the proxy knows how
// to route. Add a model = add a row. Buckets mirror opencode's
// responsesModels / compatibleModels / anthropicModels.
var catalog = []Route{
	{Model: "gpt-5.5", Wire: WireResponses},
	{Model: "deepseek-v4-pro", Wire: WireChat},
	{Model: "glm-5.2[1m]", Wire: WireAnthropic},
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

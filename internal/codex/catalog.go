package codex

import _ "embed"

// glmCatalogJSON is the model catalog shipped for the GLM model. Codex CLI
// looks up model metadata (context window, reasoning levels, etc.) from a
// catalog; the bundled catalog only knows OpenAI models, so `glm-5.2` falls
// back to degraded defaults. This catalog entry teaches Codex about the GLM
// model (1,000,000-token context, xhigh reasoning) so the local-proxy path is
// first-class. UpdateConfig provisions it next to config.toml and points
// `model_catalog_json` at it.
//
//go:embed assets/glm-catalog.json
var glmCatalogJSON []byte

// glmCatalogFilename is the on-disk filename written into the Codex home dir.
const glmCatalogFilename = "glm-catalog.json"

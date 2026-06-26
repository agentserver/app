package codex

import _ "embed"

// modelCatalogJSON is the model catalog shipped for all proxy-served models.
// Codex CLI looks up model metadata (context window, reasoning levels, etc.)
// from a catalog; the bundled catalog only knows OpenAI models, so custom
// models fall back to degraded defaults. This catalog teaches Codex about
// gpt-5.5, deepseek-v4-pro, and glm-5.2 so the local-proxy path is
// first-class. UpdateConfig provisions it next to config.toml and points
// `model_catalog_json` at it.
//
//go:embed assets/model-catalog.json
var modelCatalogJSON []byte

// modelCatalogFilename is the on-disk filename written into the Codex home dir.
const modelCatalogFilename = "model-catalog.json"

// legacyGLMCatalogFilename is the old single-model catalog filename. Cleaned
// up on first provision of the new multi-model catalog.
const legacyGLMCatalogFilename = "glm-catalog.json"

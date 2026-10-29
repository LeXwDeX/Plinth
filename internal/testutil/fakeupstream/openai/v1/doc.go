// Package openaiv1 is the fake of the OpenAI-compatible /v1 chat API that the
// Catalog's llm_base_url points at (chat/completions with streaming, tool
// calls and reasoning_content, and models). It is mounted at /openai/v1 and is
// frozen once released; another wire version gets a sibling package.
package openaiv1

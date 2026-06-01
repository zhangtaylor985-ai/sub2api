// Package claudegptcompat contains compatibility policy for the Claude
// client -> GPT/OpenAI Responses path.
//
// Keep this package limited to Claude->GPT behavior: client detection,
// WebSearch presentation, search-query safety, and source/citation helpers.
// Native Claude forwarding must not depend on this package.
package claudegptcompat

---
name: web-search
description: "Search the web to find recent news, look up current facts, research topics online, and retrieve real-time information. Use when the user asks to search, look up, google, find online, or needs current, recent, or latest information not available in training data."
---

# Web Search

Use the `web_search` tool to find current information when the user asks to search, look up, google, or find something online.

## Workflow

1. **Formulate query** — convert the user's request into a concise, targeted search query
2. **Search** — call `web_search` with the query
3. **Evaluate results** — pick the most relevant, recent, and authoritative sources
4. **Respond** — summarize findings with inline citations

## Guidelines

- Prefer results from the last 12 months unless the user asks for older information
- Cross-reference multiple sources when facts conflict
- If the first search returns poor results, reformulate the query and retry once

## Citation Format

Always cite sources inline using this format:

```
According to [Source Title](url), ...
```

## Example

User: "What's the latest version of Go?"

1. Search: `web_search("latest Go programming language version release date")`
2. Pick the top authoritative result (e.g., go.dev blog)
3. Respond: "The latest stable release is Go 1.24, released in February 2025 ([Go Blog](https://go.dev/blog))."

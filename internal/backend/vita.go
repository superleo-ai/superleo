package backend

import (
	"os"
	"strings"
)

// Vita is the person's health as the Vita iPhone app reads it from Apple
// Health: one summary per day (sleep, resting heart rate, HRV, activity,
// Vita's recovery and stress scores and suggestions), kept by a vita server
// the app reports to. Like noted it is an MCP server in mcpServers.json,
// usually remote over HTTP with "Authorization": "Bearer ${VITA_TOKEN}"; and
// it is optional the same way: an entry whose token is not set is left out,
// so a SuperLeo without VITA_TOKEN knows nothing of it.

// VitaServer is the name Vita is configured under in mcpServers.json.
const VitaServer = "vita"

// vitaSection tells the model where health questions go when Vita is
// connected.
func vitaSection(configured bool) string {
	if !configured {
		return ""
	}
	return `

The person's health lives in Vita (tools mcp_vita_*), read from Apple Health on their iPhone, one summary per day: sleep, resting heart rate, HRV, blood oxygen, steps and activity, weight, Vita's recovery and stress scores, and its insights and suggestions. For "how did I sleep", "is my resting heart rate up", "how active was I this week" use health_today, health_days or health_trend, never memory, and answer from the numbers with their dates. A day only has what the phone knew; a missing value is not a zero. health_today says when the phone last reported: if that is old, say so. Give the numbers and what Vita said about them, plainly; this is not medical advice, and anything worrying is for a doctor.`
}

// VitaEndpoint is the vita server's base address and the headers its MCP
// entry sends (its bearer token, expanded), for the two calls that are not
// MCP: the phone's report (POST /api/days), which SuperLeo passes on, and
// the status (GET /api/status). ok is false when vita is not configured or
// is not reached by URL.
func VitaEndpoint() (base string, headers map[string]string, ok bool) {
	entry, missing, _, found := findMCPServer(MCPConfigSources(), VitaServer, os.LookupEnv)
	if !found || len(missing) > 0 {
		return "", nil, false
	}
	u, _ := entry["url"].(string)
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return "", nil, false
	}
	base = strings.TrimSuffix(u, "/mcp")
	headers = map[string]string{}
	if h, ok := entry["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}
	return base, headers, true
}

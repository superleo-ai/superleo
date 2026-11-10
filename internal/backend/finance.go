package backend

import "os"

// Finance is the person's investments: holdings with their trades and monthly
// buying plans, valued at live market prices, and the news filed against each
// holding. It took over from noted's holdings and SuperLeo's own quotes. Like
// noted and vita it is an MCP server in mcpServers.json, remote over HTTP
// with "Authorization": "Bearer ${FINANCE_TOKEN}", and optional the same way:
// without it there are no holdings, and prices come from SuperLeo itself.

// FinanceServer is the name finance is configured under in mcpServers.json.
const FinanceServer = "finance"

// FinanceConfigured reports whether finance has an entry whose variables are
// all set.
func FinanceConfigured() bool {
	_, missing, _, found := findMCPServer(MCPConfigSources(), FinanceServer, os.LookupEnv)
	return found && len(missing) == 0
}

// financeSection tells the model where money questions go when finance is
// connected.
func financeSection(configured bool) string {
	if !configured {
		return ""
	}
	return `

The person's investments live in finance (tools mcp_finance_*): holdings with every buy and sell, average cost and gain or loss at live prices, the monthly buying plan (定投) with its reminder, quotes for any symbol (stocks, funds, BTC, 黄金, A-shares by their code), and the news filed against each holding. Holdings, trades and plans are recorded there, never in noted: "bought 10 VOO at 520" is record_trade. For "how is my portfolio", "what moved", "why did X drop" read portfolio_summary or list_holdings, quote and news, and answer from the numbers and the reports with their dates and outlets. For "what is going on with X" or "what are people saying about X" read topics too: the stories about it with the viewpoints in them and how many outlets carry each. It records and reports; it never moves money.`
}

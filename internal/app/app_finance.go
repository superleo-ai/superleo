package app

import "github.com/liliang-cn/superleo/internal/backend"

// Finance: the person's investments, kept by the finance server (holdings,
// trades, monthly plans, quotes and the news filed against each holding) and
// reached as the MCP server "finance". NotedHoldings and Quotes read it when
// it is connected; this is what only finance has.

// FinanceNews is the latest reports about one symbol, or about every holding
// when symbol is empty, newest first.
func (a *App) FinanceNews(symbol string, limit int) (any, error) {
	args := map[string]any{}
	if symbol != "" {
		args["symbol"] = symbol
	}
	if limit > 0 {
		args["limit"] = limit
	}
	return a.mcpRead(backend.FinanceServer, "news", args)
}

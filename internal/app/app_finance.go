package app

import "github.com/liliang-cn/superleo/internal/backend"

// Finance: the person's investments, kept by the finance server (holdings,
// trades, monthly plans, quotes, the news filed against each holding and
// argus's topic cards) and reached as the MCP server "finance". NotedHoldings
// and Quotes read it too; these are what only finance has.

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

// FinanceTopics is what is being said about one holding now: argus's stories
// about it, each with a summary and the viewpoints in it and the outlets that
// carry each one (finance's topics tool). since is "24h", "7d" or empty (7d).
func (a *App) FinanceTopics(symbol, since string, limit int) (any, error) {
	args := map[string]any{"symbol": symbol}
	if since != "" {
		args["since"] = since
	}
	if limit > 0 {
		args["limit"] = limit
	}
	return a.mcpRead(backend.FinanceServer, "topics", args)
}

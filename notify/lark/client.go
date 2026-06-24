package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"time"

	"astock_trade/core"
)

var cst = time.FixedZone("CST", 8*3600)

// Client sends messages to one or more Lark (飞书) webhook bots simultaneously.
// When multiple webhooks are configured, each message is dispatched to every
// bot concurrently; a per-bot error is logged but does not block the others.
type Client struct {
	webhookURLs []string
	httpClient  *http.Client
}

// CloseReportSummary holds account-level figures appended to the daily close report.
type CloseReportSummary struct {
	Cash          float64 // perfTracker.Cash()
	PositionValue float64 // Σ qty × current price
	TotalEquity   float64 // Cash + PositionValue
	TodayClosePnl float64 // Σ today's closed-trade PnL (absolute)
}

// New creates a Lark Client configured from environment variables.
//
// Reads webhook URLs from, in order:
//   - LARK_WEBHOOK_URL  (primary bot)
//   - LARK_WEBHOOK_URL2 (secondary bot, optional)
//
// Returns nil when no URL is configured.
func New() *Client {
	var urls []string
	for _, env := range []string{"LARK_WEBHOOK_URL", "LARK_WEBHOOK_URL2"} {
		if u := os.Getenv(env); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	return &Client{
		webhookURLs: urls,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// NewWithURLs creates a Client from an explicit list of webhook URLs.
// Returns nil when urls is empty.
func NewWithURLs(urls ...string) *Client {
	var valid []string
	for _, u := range urls {
		if u != "" {
			valid = append(valid, u)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	return &Client{
		webhookURLs: valid,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// SendCard POSTs an interactive card message to all configured webhooks.
//
// Messages are delivered to each bot concurrently. The call blocks until all
// deliveries complete (or the context is cancelled). If any bot returns an
// error it is collected and returned as a combined error; the other bots are
// not affected.
func (c *Client) SendCard(ctx context.Context, card map[string]interface{}) error {
	reqBody, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("lark: marshal card: %w", err)
	}

	type result struct {
		url string
		err error
	}
	results := make(chan result, len(c.webhookURLs))

	for _, url := range c.webhookURLs {
		url := url
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
			if err != nil {
				results <- result{url, fmt.Errorf("lark: create request: %w", err)}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := c.httpClient.Do(req)
			if err != nil {
				results <- result{url, fmt.Errorf("lark: send: %w", err)}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				results <- result{url, fmt.Errorf("lark: webhook returned HTTP %d", resp.StatusCode)}
				return
			}
			results <- result{url, nil}
		}()
	}

	var errs []error
	for range c.webhookURLs {
		if r := <-results; r.err != nil {
			errs = append(errs, r.err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Errorf("lark: %d webhook(s) failed: %s", len(errs), joinStrings(msgs, "; "))
}

func joinStrings(ss []string, sep string) string {
	var b bytes.Buffer
	for i, s := range ss {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(s)
	}
	return b.String()
}

// SendDailyCloseReport formats and sends the daily close report card.
// Sends even when closedTrades is empty (summary-only card).
// date is the CST date string in "2006-01-02" format.
func (c *Client) SendDailyCloseReport(ctx context.Context, date string, closedTrades []core.ClosedTrade, summary CloseReportSummary) error {
	card := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title": map[string]interface{}{
					"content": fmt.Sprintf("📊 今日平仓报告 | %s", date),
					"tag":     "plain_text",
				},
				"template": "blue",
			},
			"elements": buildCloseReportElements(closedTrades, summary),
		},
	}

	return c.SendCard(ctx, card)
}

// SendTradeAlert sends an immediate Lark card when a buy or sell fill is confirmed.
func (c *Client) SendTradeAlert(ctx context.Context, trade *core.Trade) error {
	if trade == nil {
		return nil
	}

	sideLabel, template := "买入", "green"
	if trade.Side == "SELL" {
		sideLabel, template = "卖出", "orange"
	}

	card := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title": map[string]interface{}{
					"content": fmt.Sprintf("%s %s", sideLabel, trade.Symbol),
					"tag":     "plain_text",
				},
				"template": template,
			},
			"elements": []interface{}{
				map[string]interface{}{
					"tag": "div",
					"text": map[string]interface{}{
						"tag":     "lark_md",
						"content": buildTradeAlertBlock(trade, sideLabel),
					},
				},
			},
		},
	}

	return c.SendCard(ctx, card)
}

// TradeDisplayPrice returns the per-share price shown in trade alerts.
// Uses OrderPrice (quote-side limit without fee allocation); falls back to Price.
func TradeDisplayPrice(trade *core.Trade) float64 {
	if trade == nil {
		return 0
	}
	if trade.OrderPrice > 0 {
		return trade.OrderPrice
	}
	return trade.Price
}

func buildTradeAlertBlock(trade *core.Trade, sideLabel string) string {
	price := TradeDisplayPrice(trade)
	amount := price * float64(trade.Quantity)
	return fmt.Sprintf(
		"**%s %s**\n成交价 **%.2f**  |  数量 **%d** 股\n成交金额 %s\n时间 %s",
		sideLabel,
		trade.Symbol,
		price,
		trade.Quantity,
		formatMoneyPlain(amount),
		formatCSTTime(trade.Timestamp),
	)
}

// PnlAbs returns absolute PnL for one closed trade: (ExitPrice − EntryPrice) × Quantity.
func PnlAbs(ct core.ClosedTrade) float64 {
	return (ct.ExitPrice - ct.EntryPrice) * float64(ct.Quantity)
}

func buildCloseReportElements(closedTrades []core.ClosedTrade, summary CloseReportSummary) []interface{} {
	elements := make([]interface{}, 0, len(closedTrades)*2+2)

	for i, ct := range closedTrades {
		elements = append(elements, map[string]interface{}{
			"tag": "div",
			"text": map[string]interface{}{
				"tag":     "lark_md",
				"content": buildTradeBlock(ct),
			},
		})
		if i < len(closedTrades)-1 {
			elements = append(elements, map[string]interface{}{"tag": "hr"})
		}
	}

	if len(closedTrades) > 0 {
		elements = append(elements, map[string]interface{}{"tag": "hr"})
	}

	elements = append(elements, buildSummaryBlock(summary))
	return elements
}

func buildTradeBlock(ct core.ClosedTrade) string {
	return fmt.Sprintf("**%s**\n开仓 %.2f → 平仓 %.2f\n收益率 %+.2f%%  |  收益额 %s\n%s → %s",
		ct.Symbol,
		ct.EntryPrice,
		ct.ExitPrice,
		ct.PnlPct,
		formatMoney(PnlAbs(ct)),
		formatCSTTime(ct.OpenTime),
		formatCSTTime(ct.CloseTime),
	)
}

func buildSummaryBlock(summary CloseReportSummary) map[string]interface{} {
	return map[string]interface{}{
		"tag": "div",
		"fields": []interface{}{
			summaryField("当日平仓收益", formatMoney(summary.TodayClosePnl)),
			summaryField("现金", formatMoneyPlain(summary.Cash)),
			summaryField("剩余持仓市值", formatMoneyPlain(summary.PositionValue)),
			summaryField("当前总和", formatMoneyPlain(summary.TotalEquity)),
		},
	}
}

func summaryField(label, value string) map[string]interface{} {
	return map[string]interface{}{
		"is_short": true,
		"text": map[string]interface{}{
			"tag":     "lark_md",
			"content": fmt.Sprintf("**%s**\n%s", label, value),
		},
	}
}

// formatMoney formats a signed currency amount, e.g. +¥1,234.56 or -¥800.00.
func formatMoney(v float64) string {
	sign := "+"
	if v < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s¥%s", sign, formatAmount(math.Abs(v)))
}

// formatMoneyPlain formats an unsigned currency amount, e.g. ¥12,345.67.
func formatMoneyPlain(v float64) string {
	return "¥" + formatAmount(math.Abs(v))
}

func formatAmount(v float64) string {
	// Round to 2 decimal places for display.
	cents := int64(math.Round(v * 100))
	whole := cents / 100
	frac := cents % 100
	if frac < 0 {
		frac = -frac
	}

	wholeStr := formatThousands(whole)
	return fmt.Sprintf("%s.%02d", wholeStr, frac)
}

func formatThousands(n int64) string {
	if n < 0 {
		return "-" + formatThousands(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var buf bytes.Buffer
	rem := len(s) % 3
	if rem == 0 {
		rem = 3
	}
	buf.WriteString(s[:rem])
	for i := rem; i < len(s); i += 3 {
		buf.WriteByte(',')
		buf.WriteString(s[i : i+3])
	}
	return buf.String()
}

// formatCSTTime formats a Unix millisecond timestamp as "MM-DD HH:MM" in CST.
func formatCSTTime(ts int64) string {
	if ts == 0 {
		return "-"
	}
	t := time.UnixMilli(ts).In(cst)
	return t.Format("01-02 15:04")
}

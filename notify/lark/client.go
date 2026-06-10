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

// Client sends messages to a Lark (飞书) channel via webhook.
type Client struct {
	webhookURL string
	httpClient *http.Client
}

// CloseReportSummary holds account-level figures appended to the daily close report.
type CloseReportSummary struct {
	Cash          float64 // perfTracker.Cash()
	PositionValue float64 // Σ qty × current price
	TotalEquity   float64 // Cash + PositionValue
	TodayClosePnl float64 // Σ today's closed-trade PnL (absolute)
}

// New creates a Lark Client. Returns nil when LARK_WEBHOOK_URL is not set or empty.
func New() *Client {
	url := os.Getenv("LARK_WEBHOOK_URL")
	if url == "" {
		return nil
	}
	return &Client{
		webhookURL: url,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SendCard POSTs an interactive card message to the webhook.
// card must be a map representing the Lark card JSON (msg_type + card fields).
// Returns nil on 2xx; returns error on network failure or non-2xx response.
func (c *Client) SendCard(ctx context.Context, card map[string]interface{}) error {
	reqBody, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("lark: marshal card: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("lark: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("lark: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("lark: webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
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

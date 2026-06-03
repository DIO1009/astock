package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// cardRecord represents one row in the daily close report card.
type cardRecord struct {
	Symbol    string
	BuyPrice  float64
	SellPrice float64
	PnlPct    float64
	OpenTime  int64
	CloseTime int64
}

// SendDailyCloseReport formats and sends the daily close report card.
// Skips silently when closedTrades is empty.
// date is the CST date string in "2006-01-02" format.
func (c *Client) SendDailyCloseReport(ctx context.Context, date string, closedTrades []core.ClosedTrade) error {
	if len(closedTrades) == 0 {
		return nil
	}

	// Build records and format the markdown table.
	records := make([]cardRecord, 0, len(closedTrades))
	for _, ct := range closedTrades {
		records = append(records, cardRecord{
			Symbol:    ct.Symbol,
			BuyPrice:  ct.BuyPrice,
			SellPrice: ct.SellPrice,
			PnlPct:    ct.PnlPct,
			OpenTime:  ct.OpenTime,
			CloseTime: ct.CloseTime,
		})
	}

	md := buildMarkdownTable(date, records)

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
			"elements": []interface{}{
				map[string]interface{}{
					"tag":     "markdown",
					"content": md,
				},
			},
		},
	}

	return c.SendCard(ctx, card)
}

// buildMarkdownTable builds the markdown table string for the close report.
// Format: | 股票 | 开仓价 | 平仓价 | 收益率 | 开仓时间 | 平仓时间 |
// No summary row.
func buildMarkdownTable(date string, records []cardRecord) string {
	var buf bytes.Buffer

	buf.WriteString("| 股票 | 开仓价 | 平仓价 | 收益率 | 开仓时间 | 平仓时间 |\n")
	buf.WriteString("|------|--------|--------|--------|----------|----------|\n")

	for _, r := range records {
		buf.WriteString(fmt.Sprintf("| %s | %.2f | %.2f | %+.2f%% | %s | %s |\n",
			r.Symbol,
			r.BuyPrice,
			r.SellPrice,
			r.PnlPct,
			formatCSTTime(r.OpenTime),
			formatCSTTime(r.CloseTime),
		))
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

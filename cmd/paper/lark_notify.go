package main

import (
	"context"
	"log"
	"time"

	"astock_trade/core"
	"astock_trade/notify/lark"
)

// larkNotifyExecutor wraps a core.Executor and sends Lark alerts after fills.
type larkNotifyExecutor struct {
	inner  core.Executor
	client *lark.Client
}

func (w *larkNotifyExecutor) Execute(order *core.Order, quote *core.Quote) (*core.Trade, error) {
	trade, err := w.inner.Execute(order, quote)
	if err != nil || trade == nil || w.client == nil {
		return trade, err
	}
	go func(t *core.Trade) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := w.client.SendTradeAlert(ctx, t); err != nil {
			log.Printf("[Lark] 成交通知发送失败 %s %s: %v", t.Side, t.Symbol, err)
		}
	}(trade)
	return trade, err
}

func wrapExecutorWithLark(inner core.Executor, client *lark.Client) core.Executor {
	if inner == nil || client == nil {
		return inner
	}
	return &larkNotifyExecutor{inner: inner, client: client}
}

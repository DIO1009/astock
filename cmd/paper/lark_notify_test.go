package main

import (
	"errors"
	"testing"

	"astock_trade/core"
	"astock_trade/notify/lark"
)

type stubExecutor struct {
	trade *core.Trade
	err   error
}

func (s *stubExecutor) Execute(_ *core.Order, _ *core.Quote) (*core.Trade, error) {
	return s.trade, s.err
}

func TestWrapExecutorWithLarkNilSafe(t *testing.T) {
	t.Parallel()
	inner := &stubExecutor{trade: &core.Trade{Symbol: "000001", Side: "BUY", OrderPrice: 10, Quantity: 100}}
	if wrapExecutorWithLark(inner, nil) != inner {
		t.Fatal("nil lark client should return inner executor unchanged")
	}
	if wrapExecutorWithLark(nil, lark.New()) != nil {
		t.Fatal("nil inner should return nil")
	}
}

func TestLarkNotifyExecutorDelegates(t *testing.T) {
	t.Parallel()
	want := &core.Trade{Symbol: "600000", Side: "SELL", OrderPrice: 8.8, Quantity: 200}
	inner := &stubExecutor{trade: want}
	wrapped := wrapExecutorWithLark(inner, &lark.Client{})
	got, err := wrapped.Execute(&core.Order{Symbol: "600000", Side: "SELL"}, &core.Quote{Symbol: "600000"})
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}
	if got != want {
		t.Fatalf("trade = %p, want %p", got, want)
	}
}

func TestLarkNotifyExecutorSkipsOnError(t *testing.T) {
	t.Parallel()
	inner := &stubExecutor{err: errors.New("rejected")}
	wrapped := wrapExecutorWithLark(inner, &lark.Client{})
	got, err := wrapped.Execute(&core.Order{}, &core.Quote{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got != nil {
		t.Fatal("expected nil trade on error")
	}
}

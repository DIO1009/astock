// Package filelog provides a TradeLogger that appends trades as newline-
// delimited JSON to a file. The file is safe for concurrent writes.
// It also exposes Close so callers can flush and release the file descriptor.
package filelog

import (
	"encoding/json"
	"log"
	"os"
	"sync"

	"astock_trade/core"
)

// Logger satisfies core.TradeLogger.
type Logger struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// New opens (or creates) the file at path and returns a Logger.
// The file is opened in append mode so restarts do not overwrite history.
func New(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{f: f, enc: json.NewEncoder(f)}, nil
}

// Log appends the trade as a single JSON line.
// Errors are surfaced via the standard logger rather than returned, because
// core.TradeLogger.Log must not disrupt the trading loop.
func (l *Logger) Log(trade *core.Trade) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.enc.Encode(trade); err != nil {
		log.Printf("[TradeLogger] write error: %v", err)
	}
}

// Close flushes and closes the underlying file.
// Call this via defer in main after the engine shuts down.
func (l *Logger) Close() error {
	return l.f.Close()
}

// Package execution provides a TradeLogger that persists ExecutionRecords
// as newline-delimited JSON for post-run slippage analysis.
//
// The logger is safe for concurrent writes.  It implements an io.Closer so
// the caller can flush and release the file descriptor after the run.
package execution

import (
	"encoding/json"
	"log"
	"os"
	"sync"

	"astock_trade/core"
)

// Logger writes ExecutionRecords to a json file.
type Logger struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// New opens (or creates) the file at path in append mode and returns a Logger.
// The file survives restarts without overwriting earlier records.
func New(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{f: f, enc: json.NewEncoder(f)}, nil
}

// Log appends the record as a single JSON line.
// Errors are surfaced via the standard logger to avoid disrupting the trading loop.
func (l *Logger) Log(rec *core.ExecutionRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.enc.Encode(rec); err != nil {
		log.Printf("[ExecutionLogger] write error: %v", err)
	}
}

// Close flushes and closes the underlying file.
// Call via defer in main after the engine shuts down.
func (l *Logger) Close() error {
	return l.f.Close()
}

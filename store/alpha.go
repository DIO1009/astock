package store

import (
	"context"
	"time"
)

// AlphaRankRow is one stock's alpha ranking for a given date.
type AlphaRankRow struct {
	Date        time.Time
	Symbol      string
	Name        string
	Score       float64
	Rank        int
	Ret5d       float64
	Ret20d      float64
	Turnover    float64
	VolumeRatio float64
	MktCap      float64
	Price       float64
}

// UpsertAlphaRankings writes the full ranking list for one date.
// Existing rows for the same (date, symbol) are updated.
func (s *Store) UpsertAlphaRankings(ctx context.Context, rows []AlphaRankRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for _, r := range rows {
		_, err := tx.Exec(ctx, `
			INSERT INTO alpha_rankings
				(date, symbol, name, score, rank, ret5d, ret20d, turnover, volume_ratio, mkt_cap, price)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (date, symbol) DO UPDATE SET
				name         = EXCLUDED.name,
				score        = EXCLUDED.score,
				rank         = EXCLUDED.rank,
				ret5d        = EXCLUDED.ret5d,
				ret20d       = EXCLUDED.ret20d,
				turnover     = EXCLUDED.turnover,
				volume_ratio = EXCLUDED.volume_ratio,
				mkt_cap      = EXCLUDED.mkt_cap,
				price        = EXCLUDED.price,
				created_at   = NOW()
		`,
			r.Date, r.Symbol, r.Name, r.Score, r.Rank,
			r.Ret5d, r.Ret20d, r.Turnover, r.VolumeRatio, r.MktCap, r.Price,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetTopSymbols returns the top-n stock codes ranked by today's alpha score.
// Falls back to yesterday if today's data is not yet available.
func (s *Store) GetTopSymbols(ctx context.Context, n int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT symbol FROM alpha_rankings
		WHERE date = (SELECT MAX(date) FROM alpha_rankings)
		ORDER BY rank ASC
		LIMIT $1
	`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []string
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err != nil {
			return nil, err
		}
		symbols = append(symbols, sym)
	}
	return symbols, rows.Err()
}

// GetTopRankings returns the top-n full alpha ranking rows for the most recent date.
// Falls back to yesterday if today's data is not yet available.
func (s *Store) GetTopRankings(ctx context.Context, n int) ([]AlphaRankRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT date, symbol, name, score, rank, ret5d, ret20d, turnover, volume_ratio, mkt_cap, price
		FROM alpha_rankings
		WHERE date = (SELECT MAX(date) FROM alpha_rankings)
		ORDER BY rank ASC
		LIMIT $1
	`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AlphaRankRow
	for rows.Next() {
		var r AlphaRankRow
		if err := rows.Scan(&r.Date, &r.Symbol, &r.Name, &r.Score, &r.Rank,
			&r.Ret5d, &r.Ret20d, &r.Turnover, &r.VolumeRatio, &r.MktCap, &r.Price); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetLatestRankingDate returns the most recent date in alpha_rankings,
// or zero time if the table is empty.
func (s *Store) GetLatestRankingDate(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(date), '1970-01-01') FROM alpha_rankings`).Scan(&t)
	return t, err
}

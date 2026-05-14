package main

import (
	"encoding/json"
	"fmt"
	"os"

	"astock_trade/safety"
)

type safetyConfigFile struct {
	StreakHalfPositionAt int     `json:"streak_half_position_at"`
	StreakPositionScale  float64 `json:"streak_position_scale"`
	StreakFreezeAt       int     `json:"streak_freeze_at"`
	StreakFreezeTicks    int     `json:"streak_freeze_ticks"`
	BaseMaxTotalPct      float64 `json:"base_max_total_pct"`
	AbnormalLatencyMs    int64   `json:"abnormal_latency_ms"`
	AbnormalFillRatePct  float64 `json:"abnormal_fill_rate_pct"`
	AbnormalWindowTicks  int     `json:"abnormal_window_ticks"`
	AbnormalThreshold    int     `json:"abnormal_threshold"`
	StatusEveryNTicks    int     `json:"status_every_n_ticks"`
}

func loadSafetyConfig(path string, cfg safety.Config) (safety.Config, error) {
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	var fileCfg safetyConfigFile
	if err := json.Unmarshal(data, &fileCfg); err != nil {
		return cfg, err
	}

	if fileCfg.StreakHalfPositionAt < 0 ||
		fileCfg.StreakPositionScale < 0 ||
		fileCfg.StreakFreezeAt < 0 ||
		fileCfg.StreakFreezeTicks < 0 ||
		fileCfg.BaseMaxTotalPct < 0 ||
		fileCfg.AbnormalLatencyMs < 0 ||
		fileCfg.AbnormalFillRatePct < 0 ||
		fileCfg.AbnormalWindowTicks < 0 ||
		fileCfg.AbnormalThreshold < 0 ||
		fileCfg.StatusEveryNTicks < 0 {
		return cfg, fmt.Errorf("安全控制配置不能包含负数")
	}

	if fileCfg.StreakHalfPositionAt > 0 {
		cfg.StreakHalfPositionAt = fileCfg.StreakHalfPositionAt
	}
	if fileCfg.StreakPositionScale > 0 {
		cfg.StreakPositionScale = fileCfg.StreakPositionScale
	}
	if fileCfg.StreakFreezeAt > 0 {
		cfg.StreakFreezeAt = fileCfg.StreakFreezeAt
	}
	if fileCfg.StreakFreezeTicks > 0 {
		cfg.StreakFreezeTicks = fileCfg.StreakFreezeTicks
	}
	if fileCfg.BaseMaxTotalPct > 0 {
		cfg.BaseMaxTotalPct = fileCfg.BaseMaxTotalPct
	}
	if fileCfg.AbnormalLatencyMs > 0 {
		cfg.AbnormalLatencyMs = fileCfg.AbnormalLatencyMs
	}
	if fileCfg.AbnormalFillRatePct > 0 {
		cfg.AbnormalFillRatePct = fileCfg.AbnormalFillRatePct
	}
	if fileCfg.AbnormalWindowTicks > 0 {
		cfg.AbnormalWindowTicks = fileCfg.AbnormalWindowTicks
	}
	if fileCfg.AbnormalThreshold > 0 {
		cfg.AbnormalThreshold = fileCfg.AbnormalThreshold
	}
	if fileCfg.StatusEveryNTicks > 0 {
		cfg.StatusEveryNTicks = fileCfg.StatusEveryNTicks
	}

	return cfg, nil
}
package registry

// ClaudeModels is the default Anthropic catalogue (first-party API rates,
// USD per MTok, as of 2026-10). Cache writes are the 5-minute TTL rate
// (1.25x input). Confirm against https://www.anthropic.com/pricing before
// relying on cost figures; the Haiku 5.5 cache-read rate is an estimate
// (0.1x input), and Haiku 5.5 bills $0.50/$2.50 above 100K-token prompts,
// which Cost does not model.
func ClaudeModels() []Model {
	return []Model{
		{
			ID: "opus-5-5", Provider: "anthropic", ModelID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5",
			Price:   Pricing{InPerMTok: 4.00, OutPerMTok: 20.00, CacheReadPerMTok: 0.20, CacheWritePerMTok: 5.00},
			Enabled: true,
		},
		{
			ID: "sonnet-5-5", Provider: "anthropic", ModelID: "claude-sonnet-5-5", DisplayName: "Claude Sonnet 5.5",
			Price:   Pricing{InPerMTok: 2.00, OutPerMTok: 10.00, CacheReadPerMTok: 0.20, CacheWritePerMTok: 2.50},
			Enabled: true,
		},
		{
			ID: "haiku-5-5", Provider: "anthropic", ModelID: "claude-haiku-5-5", DisplayName: "Claude Haiku 5.5",
			Price:   Pricing{InPerMTok: 0.10, OutPerMTok: 0.50, CacheReadPerMTok: 0.01, CacheWritePerMTok: 0.125},
			Enabled: true,
		},
	}
}

// DefaultRoutes is the starting task → model mapping. Priority 0 is the
// primary tier, 1 the fallback; tune weights from eval results.
func DefaultRoutes() []Route {
	r := func(task, model string, prio int, maxTokens int64, effort string) Route {
		return Route{Task: task, ModelID: model, Weight: 1, Priority: prio, MaxTokens: maxTokens, Effort: effort, Enabled: true}
	}
	return []Route{
		r(TaskChat, "sonnet-5-5", 0, 16000, ""),
		r(TaskChat, "opus-5-5", 1, 16000, ""),
		r(TaskExplainSpike, "opus-5-5", 0, 16000, "high"),
		r(TaskExplainSpike, "sonnet-5-5", 1, 16000, ""),
		r(TaskSummarizeReport, "sonnet-5-5", 0, 4096, "low"),
		r(TaskSummarizeReport, "haiku-5-5", 1, 4096, ""),
		r(TaskLabelSuggest, "sonnet-5-5", 0, 4096, ""),
		r(TaskEvalJudge, "opus-5-5", 0, 4096, ""),
		r(TaskIntentRoute, "haiku-5-5", 0, 1024, "low"),
		r(TaskIntentRoute, "sonnet-5-5", 1, 1024, "low"),
		r(TaskMemoryMerge, "haiku-5-5", 0, 1024, "low"),
		r(TaskMemoryMerge, "sonnet-5-5", 1, 1024, "low"),
	}
}

package ctxmem

// DeriveHistoryBudget 从模型窗口推导本次可分配给历史的总额。
//
//	R = max(1024, 0.25 * W)   输出预留
//	S = system prompt + 工具 schema + 长期记忆（调用方实测）
//	M = max(512, 0.05 * W)    安全余量
//	H = W - R - S - M
//
// H 必须由调用方注入：同一个进程可能服务多个模型规格，ctxmem 不读配置。
func DeriveHistoryBudget(windowTokens, systemTokens int) Budget {
	if windowTokens <= 0 {
		return Budget{}
	}
	reserve := maxInt(1024, windowTokens/4)
	margin := maxInt(512, windowTokens/20)
	history := windowTokens - reserve - systemTokens - margin
	if history < 0 {
		history = 0
	}
	// Reserve 是已经从这个窗口里扣掉、不会再分配给任何一层的余量，
	// 它与 Total 互斥，避免调用方误以为 Sum() 之外还藏着一笔可用额度。
	return Budget{Total: history, Reserve: margin}
}

// DefaultQuotas 返回默认的四层配额。
//
// 比例来自规格：Working 35%–75%，Episodic 5%–20%，Semantic 64–10%，
// Retrieval 0–30%。Min 之和可能超过 h（小窗口），由 Allocate 等比压缩。
func DefaultQuotas(h int, demand [4]int) [4]LayerQuota {
	semanticMin := 64
	if h < semanticMin {
		semanticMin = h
	}
	// 小窗口下 10% 会小于保底值；Max 必须不小于 Min，否则
	// Allocate 会把 Semantic 的配额算成负数。
	semanticMax := maxInt(h*10/100, semanticMin)
	return [4]LayerQuota{
		{
			Min:    h * 35 / 100,
			Max:    h * 75 / 100,
			Demand: func() int { return demand[LayerWorking] },
		},
		{
			Min:    h * 5 / 100,
			Max:    h * 20 / 100,
			Demand: func() int { return demand[LayerEpisodic] },
		},
		{
			Min:    semanticMin,
			Max:    semanticMax,
			Demand: func() int { return demand[LayerSemantic] },
		},
		{
			Min:    0,
			Max:    h * 30 / 100,
			Demand: func() int { return demand[LayerRetrieval] },
		},
	}
}

// Allocate 按「保底 → 按需竞争 → 两轮回收」切分预算。
//
// 固定比例切分会让 Semantic 这种只要几百 token 的层白白占额度，
// 也让未触发的 Retrieval 层长期闲置 25% 预算。
func Allocate(h int, quotas [4]LayerQuota) Budget {
	if h <= 0 {
		return Budget{}
	}

	var need [4]int
	minSum := 0
	for i, quota := range quotas {
		demand := quota.Max
		if quota.Demand != nil {
			demand = quota.Demand()
		}
		need[i] = minInt(maxInt(demand, 0), maxInt(quota.Max, 0))
		minSum += maxInt(quota.Min, 0)
	}

	if minSum > h {
		return scaleDown(h, quotas)
	}

	var got [4]int
	assigned := 0
	for i, quota := range quotas {
		got[i] = maxInt(quota.Min, 0)
		assigned += got[i]
	}

	rest := h - assigned
	for pass := 0; pass < 2 && rest > 0; pass++ {
		weight := 0
		for i := range quotas {
			weight += maxInt(need[i]-got[i], 0)
		}
		if weight == 0 {
			break
		}
		for i := range quotas {
			want := maxInt(need[i]-got[i], 0)
			if want == 0 {
				continue
			}
			give := minInt(rest*want/weight, maxInt(quotas[i].Max-got[i], 0))
			got[i] += give
			assigned += give
		}
		rest = h - assigned
	}

	// 所有层都到 Max 仍有余量，说明本次内容很少；多留一条原文永远不亏。
	got[LayerWorking] += rest

	return budgetFrom(got, h)
}

// scaleDown 在保底总额超过 H 时等比压缩。
//
// 余数统一给 Working：它是唯一「少给一条就少一轮对话」的层，
// 而 Semantic/Episodic 在极小预算下本来就无法保证完整。
func scaleDown(h int, quotas [4]LayerQuota) Budget {
	minSum := 0
	for _, quota := range quotas {
		minSum += maxInt(quota.Min, 0)
	}
	if minSum <= 0 {
		return Budget{Total: h, Working: h}
	}

	var got [4]int
	assigned := 0
	for i, quota := range quotas {
		got[i] = maxInt(quota.Min, 0) * h / minSum
		assigned += got[i]
	}
	got[LayerWorking] += h - assigned
	return budgetFrom(got, h)
}

func budgetFrom(got [4]int, h int) Budget {
	return Budget{
		Total:     h,
		Working:   got[LayerWorking],
		Episodic:  got[LayerEpisodic],
		Semantic:  got[LayerSemantic],
		Retrieval: got[LayerRetrieval],
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

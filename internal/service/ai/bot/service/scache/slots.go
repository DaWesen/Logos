package scache

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SlotSet 是从 query 中抽出的高置信结构化要点。
//
// 它的职责不是「理解问题」，而是用纯本地的微秒级规则，把向量分不开的
// 情况挡掉：否定翻转、数字漂移、时间指代、方向互换。
type SlotSet struct {
	Negated  bool     `json:"neg"`
	Intent   string   `json:"intent"`
	Entities []string `json:"entities"`
	Numbers  []string `json:"numbers"`
	TimeRef  string   `json:"time"`
	Compare  string   `json:"cmp"`
	// Route 记录「A 到 B」的方向。实体集合相同但方向相反的两个问题
	// （北京到上海 / 上海到北京）答案完全不同，只靠集合无法区分。
	Route string `json:"route"`
}

// Empty 表示没有抽到任何可用于门控的信息。
func (s SlotSet) Empty() bool {
	return !s.Negated && s.Intent == "" && len(s.Entities) == 0 &&
		len(s.Numbers) == 0 && s.TimeRef == "" && s.Compare == "" && s.Route == ""
}

// Compatible 判断两个槽位集合是否允许复用答案。
//
// 采用保守否决：不确定就返回 false。注意「一方为空则放行」的不对称规则——
// 抽取器必然有漏抽，硬否决会把大量正常改写误杀成 miss；而漏抽的代价
// 只是回退到向量 + 判官，方向是安全的。
func (a SlotSet) Compatible(b SlotSet) bool {
	if a.Negated != b.Negated {
		return false
	}
	if a.Compare != "" && b.Compare != "" && a.Compare != b.Compare {
		return false
	}
	if a.TimeRef != "" && b.TimeRef != "" && a.TimeRef != b.TimeRef {
		return false
	}
	if (len(a.Numbers) > 0 || len(b.Numbers) > 0) && !sameStringSet(a.Numbers, b.Numbers) {
		return false
	}
	if len(a.Entities) > 0 && len(b.Entities) > 0 && !sameStringSet(a.Entities, b.Entities) {
		return false
	}
	if a.Route != "" && b.Route != "" && a.Route != b.Route {
		return false
	}
	return true
}

var (
	numberPattern = regexp.MustCompile(`\d+(?:\.\d+)?\s*(?:元|块|万|亿|%|天|小时|个|次|年|月|日|岁|人|折)?`)
	quotedPattern = regexp.MustCompile(`["“”'‘’「」『』【】]([^"“”'‘’「」『』【】]{1,20})["“”'‘’「」『』【】]`)
	routePattern  = regexp.MustCompile(`(?:从)?([\p{Han}A-Za-z0-9]{1,8})(?:到|去|飞往|至)([\p{Han}A-Za-z0-9]{1,8})`)
)

var negativeWords = []string{
	"不", "别", "勿", "未", "没", "禁止", "关闭", "取消", "停用", "退订",
	"禁用", "无法", "不能", "不支持", "不允许",
}

var timeWords = []struct {
	word string
	tag  string
}{
	{"今天", "today"}, {"今日", "today"},
	{"明天", "tomorrow"}, {"明日", "tomorrow"},
	{"昨天", "yesterday"}, {"昨日", "yesterday"},
	{"本周", "week"}, {"这周", "week"},
	{"本月", "month"}, {"这个月", "month"},
	{"今年", "year"},
	{"现在", "now"}, {"当前", "now"}, {"最新", "now"}, {"最近", "now"},
}

// Extract 抽取槽位。纯本地、无分配之外的外部依赖。
func Extract(query string) SlotSet {
	lower := strings.ToLower(query)
	var s SlotSet

	for _, w := range negativeWords {
		if strings.Contains(lower, w) {
			s.Negated = true
			break
		}
	}

	for _, tw := range timeWords {
		if strings.Contains(lower, tw.word) {
			s.TimeRef = tw.tag
			break
		}
	}

	switch {
	case containsAny(lower, "大于", "超过", "高于", "多于"):
		s.Compare = "gt"
	case containsAny(lower, "小于", "低于", "不足", "少于"):
		s.Compare = "lt"
	}

	switch {
	case containsAny(lower, "怎么", "如何", "怎样", "步骤"):
		s.Intent = "howto"
	case containsAny(lower, "多少钱", "价格", "费用", "收费", "报价"):
		s.Intent = "price"
	case containsAny(lower, "能不能", "可以吗", "是否支持", "支持吗"):
		s.Intent = "feasibility"
	}

	for _, m := range numberPattern.FindAllString(lower, -1) {
		s.Numbers = append(s.Numbers, strings.ReplaceAll(m, " ", ""))
	}
	s.Numbers = sortedUnique(s.Numbers)

	for _, m := range quotedPattern.FindAllStringSubmatch(query, -1) {
		if v := strings.TrimSpace(m[1]); v != "" {
			s.Entities = append(s.Entities, strings.ToLower(v))
		}
	}

	for _, m := range routePattern.FindAllStringSubmatch(query, -1) {
		// 正则只能截到「到」后面的 8 个字，问句尾巴（怎么走/要多久）
		// 会被一起吃进来。先去掉这些非地名后缀，再做端点校验。
		from, to := strings.TrimSpace(m[1]), trimRouteTail(strings.TrimSpace(m[2]))
		if !isRouteEndpoint(from) || !isRouteEndpoint(to) {
			continue
		}
		s.Route = strings.ToLower(from) + "→" + strings.ToLower(to)
		s.Entities = append(s.Entities, strings.ToLower(from), strings.ToLower(to))
		break
	}

	s.Entities = sortedUnique(s.Entities)
	return s
}

// TooShortOrAnaphoric 判断 query 是否脱离上下文后不可判定。
// 只对短句生效：长句里的「这个」通常有足够的限定语，不应一刀切。
func TooShortOrAnaphoric(normalized string) bool {
	if utf8.RuneCountInString(normalized) >= 8 {
		return false
	}
	return containsAny(normalized,
		"这个", "那个", "它", "上面", "刚才", "第二个", "然后", "继续", "还有呢", "再呢",
	)
}

func isRouteEndpoint(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > 8 {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	// 问句里的「怎么到上海」不是路线，抽不出起点时直接放弃方向槽
	return !containsAny(s, "怎么", "如何", "怎样", "什么", "哪里", "吗", "呢")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

var routeTailWords = []string{
	"怎么走", "怎么去", "如何去", "要多久", "多长时间", "多远", "多久",
	"怎么", "如何", "怎样", "什么", "哪里", "吗", "呢",
}

func trimRouteTail(s string) string {
	for {
		trimmed := false
		for _, suffix := range routeTailWords {
			if strings.HasSuffix(s, suffix) && utf8.RuneCountInString(s) > utf8.RuneCountInString(suffix) {
				s = strings.TrimSuffix(s, suffix)
				trimmed = true
			}
		}
		if !trimmed {
			return strings.TrimSpace(s)
		}
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

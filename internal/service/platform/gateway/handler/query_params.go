package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"Logos/internal/service/platform/gateway/model"
	pb "Logos/proto_gen/search"

	"github.com/gin-gonic/gin"
)

// 查询参数里除了分页，还有一类「数值型过滤条件」：时间范围、枚举、深度。
// 它们的共同点是「解析失败时有一个看起来合理的零值」——0 表示不限、0 表示
// 未指定、0 表示默认深度。于是 `?start_time=abc` 会被静默当成「不限时间」，
// 接口返回 200 和一整页数据，调用方以为过滤生效了，实际拿到的是全量。
//
// 这类参数必须和分页一样对待：非法值直接 400，不猜测调用方意图。

// parseInt64Query 解析非负 int64 查询参数。
// 空字符串视作「没传」，回落 default；非数字、负数、溢出都返回 400。
func parseInt64Query(c *gin.Context, key string, defaultValue int64) (int64, bool) {
	raw := c.Query(key)
	if raw == "" {
		return defaultValue, true
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		c.JSON(http.StatusBadRequest, model.BadRequest(key+" 必须是非负整数"))
		return 0, false
	}
	return parsed, true
}

// parseBoundedIntQuery 解析落在 [minValue, maxValue] 区间内的整数查询参数。
// 用于枚举与深度这类「合法取值有限」的参数：超界值不是笔误就是客户端版本
// 不匹配，继续往下传只会让下游按另一个语义执行（例如 level=9 被当成 0）。
func parseBoundedIntQuery(c *gin.Context, key string, defaultValue, minValue, maxValue int) (int, bool) {
	raw := c.Query(key)
	if raw == "" {
		return defaultValue, true
	}
	return parseBoundedInt(c, key, raw, minValue, maxValue)
}

// parseBoundedIntPath 解析路径参数里的整数（例如索引类型 /index/:type）。
// 路径参数没有「没传」这种状态，缺失或非法都必须 400：旧实现把解析错误
// 丢给 `_`，`/index/abc` 会退化成 0 号枚举，直接对错误的对象执行建表/删表。
func parseBoundedIntPath(c *gin.Context, key string, minValue, maxValue int) (int, bool) {
	return parseBoundedInt(c, key, c.Param(key), minValue, maxValue)
}

func parseBoundedInt(c *gin.Context, key, raw string, minValue, maxValue int) (int, bool) {
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < minValue || parsed > maxValue {
		c.JSON(http.StatusBadRequest, model.BadRequest(
			fmt.Sprintf("%s 必须是 %d..%d 之间的整数", key, minValue, maxValue)))
		return 0, false
	}
	return parsed, true
}

// parseTimeRange 解析 start_time / end_time（毫秒时间戳）。
// 两个参数独立可选，0 表示该侧不限；但都给出时 start 不能晚于 end，
// 否则 SQL 会得到 `timestamp >= 10 AND timestamp <= 5`，永远返回空集，
// 调用方只能看到「没有数据」，看不出是自己把区间写反了。
func parseTimeRange(c *gin.Context) (start, end int64, ok bool) {
	start, ok = parseInt64Query(c, "start_time", 0)
	if !ok {
		return 0, 0, false
	}
	end, ok = parseInt64Query(c, "end_time", 0)
	if !ok {
		return 0, 0, false
	}
	if start > 0 && end > 0 && start > end {
		c.JSON(http.StatusBadRequest, model.BadRequest("start_time 不能晚于 end_time"))
		return 0, 0, false
	}
	return start, end, true
}

// parseOptionalBoolQuery 解析三态布尔：未传 = nil（不过滤），
// "true"/"false" = 显式过滤，其他值 = 400。
//
// 旧写法是 `if c.Query("resolved") == "true" {...} else if == "false" {...}`，
// `?resolved=yes` 两条分支都不命中，等于悄悄取消了过滤条件。
func parseOptionalBoolQuery(c *gin.Context, key string) (*bool, bool) {
	raw := c.Query(key)
	if raw == "" {
		return nil, true
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(key+" 必须是 true 或 false"))
		return nil, false
	}
	return &parsed, true
}

// parseOptionalBoolCompatQuery 解析「缺省为 false」的开关参数，
// 同时兼容前端历史上用过的 1/0 写法。
//
// 与 parseOptionalBoolQuery 的差别在默认值语义：登出接口的 `all` 不传就是
// 「只登出当前设备」，因此没有「不过滤」这个第三态，调用方只需要 bool。
// 非法值必须 400：旧写法 `c.Query("all") == "true" || c.Query("all") == "1"`
// 会让 `?all=yes` 静默变成 false —— 用户以为退出了所有设备，实际只退出了
// 当前这台。这类「安全语义刚好反了」的静默降级比报错危险得多。
func parseOptionalBoolCompatQuery(c *gin.Context, key string) (bool, bool) {
	raw := strings.ToLower(c.Query(key))
	switch raw {
	case "", "false", "0":
		return false, true
	case "true", "1":
		return true, true
	default:
		c.JSON(http.StatusBadRequest, model.BadRequest(key+" 必须是 true 或 false"))
		return false, false
	}
}

// parseEnumQuery 解析「字符串枚举」查询参数：取值必须命中白名单，
// 空字符串表示没传，回落 defaultValue（通常是 UNSPECIFIED = 不过滤）。
//
// 旧写法是一串 if/else if：
//
//	if t := c.Query("type"); t == "builtin" { ... } else if t == "custom" { ... } else { UNSPECIFIED }
//
// 拼错（bulitin）、大小写不一致（Builtin）或误传数字（1）都会掉进最后的
// else，被当成「不过滤」，接口 200 返回全部 Bot。调用方以为筛了「内置
// Bot」，实际拿到全部——过滤条件静默失效，和返回全量数据一样难排查。
//
// 这里要求调用方给出明确的枚举名而不是数字：字符串枚举的契约是
// 「可读、可校验」，允许数字等于把 proto 的数值编号泄漏成公开 API，
// 之后调整枚举顺序就会悄悄改变语义。
func parseEnumQuery(c *gin.Context, key string, defaultValue int32, allowed map[string]int32) (int32, bool) {
	raw := c.Query(key)
	if raw == "" {
		return defaultValue, true
	}
	value, ok := allowed[raw]
	if !ok {
		c.JSON(http.StatusBadRequest, model.BadRequest(
			fmt.Sprintf("%s 必须是以下之一: %s", key, enumNames(allowed))))
		return 0, false
	}
	return value, true
}

func enumNames(allowed map[string]int32) string {
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// maxEnumValue 从 protoc 生成的 `<Enum>_name` map 里取最大合法值，
// 避免在 handler 里写死 `8` / `4` 这类魔法数字：proto 新增枚举后，
// 校验上限会自动跟上，不会出现「新枚举被网关拒掉」的隐性回归。
func maxEnumValue(names map[int32]string) int {
	maxValue := 0
	for value := range names {
		if int(value) > maxValue {
			maxValue = int(value)
		}
	}
	return maxValue
}

// 索引类型在两个位置出现，语义并不相同：
//   POST /search/indices        → 建哪个索引，由查询参数 ?type= 决定，缺省 ENTITY
//   DELETE /search/indices/:id  → 删哪个索引，由路径参数 :id 决定，必须显式给出
//
// 之前两个 handler 共用了一个读 c.Param("type") 的函数：建索引接口没有 :type
// 路由参数，读到空串后一律 400；删除/刷新接口的路由参数叫 :id，读到空串后
// 又退回 0 号枚举 ENTITY——`DELETE /indices/4` 实际删掉的是实体索引。
// 参数名错位比「解析失败」更隐蔽：接口看起来在工作，只是操作了另一个对象。
func parseIndexTypeQuery(c *gin.Context) (pb.IndexType, bool) {
	parsed, ok := parseBoundedIntQuery(c, "type", int(pb.IndexType_ENTITY), 0, maxEnumValue(pb.IndexType_name))
	if !ok {
		return 0, false
	}
	return pb.IndexType(parsed), true
}

// parseIndexTypePath 解析 /search/indices/:id 里的索引类型。
// 索引类型是 0 基枚举（ENTITY=0 合法），因此不能用「0 表示没传」那套判断；
// 路径参数缺失/非法必须 400，绝不能回落成 0。
func parseIndexTypePath(c *gin.Context) (pb.IndexType, bool) {
	parsed, ok := parseBoundedIntPath(c, "id", 0, maxEnumValue(pb.IndexType_name))
	if !ok {
		return 0, false
	}
	return pb.IndexType(parsed), true
}

package auth

import (
	"context"
	"strconv"
	"time"

	"Logos/pkg/cache"
	pkgJwt "Logos/pkg/jwt"
	"Logos/pkg/logger"
)

const (
	// revokedJTIPrefix 单令牌黑名单：key 带剩余 TTL，令牌自然过期后由 Redis 自动回收
	revokedJTIPrefix = "auth:revoked:jti:"
	// revokedUserPrefix 用户级撤销水位线：签发时间早于水位线的令牌全部失效
	revokedUserPrefix = "auth:revoked:user:"
)

// Revoker 维护 JWT 的撤销状态，解决"无状态令牌签发后无法作废"的问题：
//
//   - 单令牌撤销（登出当前设备）：按 jti 拉黑，TTL = 令牌剩余有效期；
//   - 用户级撤销（改密、登出所有设备）：写入"撤销水位线"，判断 iat 是否早于水位线。
//
// 为什么不给每个用户维护 token 列表：一次登出全部设备要为该用户所有历史令牌建索引，
// 写入放大且需要清理；水位线是一个 key、一次写入，判断也不需要扫描。
//
// 故障策略：Redis 不可用时按 fail-open 放行并告警。撤销查询在认证热路径上，
// fail-closed 会让一次缓存抖动直接变成全站 401；这里用"可用性优先 + 告警"换取，
// 单令牌黑名单本身也有过期时间，缓存恢复后立即重新生效。
type Revoker struct {
	cache cache.Cache
}

func NewRevoker(c cache.Cache) *Revoker {
	return &Revoker{cache: c}
}

// RevokeToken 拉黑单个令牌。ttl 传令牌剩余有效期即可（<=0 时给最小 1 秒，避免 Set 永久键）。
func (r *Revoker) RevokeToken(ctx context.Context, jti string, ttl time.Duration) error {
	if r == nil || r.cache == nil || jti == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Second
	}
	return r.cache.Set(ctx, revokedJTIPrefix+jti, "1", ttl)
}

// RevokeAll 设置用户撤销水位线：此刻之前签发的令牌全部失效。
// ttl 建议为访问令牌有效期 + 余量，保证最长寿命的旧令牌也覆盖到。
func (r *Revoker) RevokeAll(ctx context.Context, userID string, ttl time.Duration) error {
	if r == nil || r.cache == nil || userID == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return r.cache.Set(ctx, revokedUserPrefix+userID, strconv.FormatInt(time.Now().Unix(), 10), ttl)
}

// RevocationState 撤销查询的三态结果。
//
// 之所以不继续用 bool：bool 把「确认未撤销」与「查询失败」压成同一个 false，
// 调用方无法区分「令牌确实有效」和「Redis 挂了只能放行」。网关的定期复核
// 恰恰需要这个区分——前者可以安静地继续，后者必须打点告警，
// 否则撤销链路整体失效时监控上一切正常。
type RevocationState int

const (
	// RevocationClear 确认未撤销（缓存返回了明确结果）。
	RevocationClear RevocationState = iota
	// RevocationRevoked 确认已撤销（单令牌黑名单命中或早于用户水位线）。
	RevocationRevoked
	// RevocationUnknown 查询失败，无法判断；调用方按 fail-open 放行但必须告警。
	RevocationUnknown
)

// String 返回指标标签用的稳定字符串。
func (s RevocationState) String() string {
	switch s {
	case RevocationRevoked:
		return "revoked"
	case RevocationUnknown:
		return "unknown"
	default:
		return "clear"
	}
}

// Check 查询令牌的撤销状态，并返回底层错误。
//
// 与 IsRevoked 的关系：IsRevoked 是「只关心是否拒绝」的便捷封装；
// Check 给需要区分故障的调用方（网关长连接复核）使用。
// 查询顺序与优先级：单令牌黑名单优先于用户水位线——撤销单个令牌是更明确的
// 业务动作，命中即返回，省掉后续一次 Exists + Get。
func (r *Revoker) Check(ctx context.Context, claims *pkgJwt.Claims) (RevocationState, error) {
	if r == nil || r.cache == nil {
		return RevocationClear, nil
	}
	if claims == nil {
		// 无声明无法判断撤销状态，交给上层的声明校验处理（这里不越权拒绝）。
		return RevocationClear, nil
	}

	if claims.ID != "" {
		revoked, err := r.cache.Exists(ctx, revokedJTIPrefix+claims.ID)
		if err != nil {
			return RevocationUnknown, err
		}
		if revoked {
			return RevocationRevoked, nil
		}
	}

	if claims.IssuedAt == nil || claims.UserID == "" {
		return RevocationClear, nil
	}
	// 先 Exists 再用 Get：缓存实现里 missing key 与故障都表现为 error，分两步才能区分
	exists, err := r.cache.Exists(ctx, revokedUserPrefix+claims.UserID)
	if err != nil {
		return RevocationUnknown, err
	}
	if !exists {
		return RevocationClear, nil
	}
	raw, err := r.cache.Get(ctx, revokedUserPrefix+claims.UserID)
	if err != nil {
		return RevocationUnknown, err
	}
	watermark, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		logger.Warn("用户撤销水位线格式异常", logger.StringField("value", raw))
		return RevocationUnknown, err
	}
	// 用 < 而不是 <=：撤销后同一秒内重新登录拿到的令牌 iat 与水位线相等，不应被误杀
	if claims.IssuedAt.Unix() < watermark {
		return RevocationRevoked, nil
	}
	return RevocationClear, nil
}

// IsRevoked 判断令牌是否已被撤销。返回 true 表示应立即拒绝。
// 缓存异常时 fail-open 返回 false，并记录告警便于发现缓存故障。
func (r *Revoker) IsRevoked(ctx context.Context, claims *pkgJwt.Claims) bool {
	state, err := r.Check(ctx, claims)
	if err != nil {
		logger.Warn("查询令牌撤销状态失败，按 fail-open 处理", logger.ErrorField(err))
	}
	return state == RevocationRevoked
}

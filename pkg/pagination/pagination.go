// Package pagination 统一分页参数的边界。
//
// 分页是外部输入，不能假设调用方一定传 page>=1、pageSize 在合理区间：
// pageSize=0 可能让数据库省略 LIMIT，负 page 会生成负 OFFSET。
// 在服务边界归一化，DAO 只关心合法的 offset/limit。
package pagination

import "errors"

const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100

	// MaxResultWindow 是 Elasticsearch 默认 index.max_result_window。
	// from+size 超过该值 ES 会直接返回 "Result window is too large"，
	// 与其把后端原始错误透给客户端，不如在服务边界提前拒绝。
	MaxResultWindow = 10000
)

// ErrPageTooDeep 请求的页超出后端可承受的窗口（ES 深分页限制）。
var ErrPageTooDeep = errors.New("page too deep")

// Normalize 把分页参数收敛到安全范围：
// page 至少为 1；pageSize 默认 20，最大 100。
func Normalize(page, pageSize int) (int, int) {
	return NormalizeWithDefault(page, pageSize, DefaultPageSize)
}

// NormalizeWithDefault 与 Normalize 相同，但允许调用方指定自己的默认页大小
// （例如好友列表默认 50 条）。上限仍然是 MaxPageSize。
func NormalizeWithDefault(page, pageSize, defaultPageSize int) (int, int) {
	if defaultPageSize < 1 {
		defaultPageSize = DefaultPageSize
	}
	if defaultPageSize > MaxPageSize {
		defaultPageSize = MaxPageSize
	}
	if page < DefaultPage {
		page = DefaultPage
	}
	switch {
	case pageSize < 1:
		pageSize = defaultPageSize
	case pageSize > MaxPageSize:
		pageSize = MaxPageSize
	}
	return page, pageSize
}

// Offset 返回归一化后的 SQL offset 和 pageSize。
func Offset(page, pageSize int) (int, int) {
	page, pageSize = Normalize(page, pageSize)
	return (page - 1) * pageSize, pageSize
}

// Window 在 Offset 的基础上检查是否越过 maxWindow，避免把非法深页交给检索后端。
// maxWindow<=0 表示不限制。
func Window(page, pageSize, maxWindow int) (int, int, error) {
	offset, size := Offset(page, pageSize)
	if maxWindow > 0 && offset+size > maxWindow {
		return 0, 0, ErrPageTooDeep
	}
	return offset, size, nil
}

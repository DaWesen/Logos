package pagination

import (
	"errors"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		name                   string
		page, pageSize         int
		wantPage, wantPageSize int
	}{
		{"零值回落", 0, 0, 1, 20},
		{"负数回落", -3, -10, 1, 20},
		{"超大页大小截断", 2, 500, 2, 100},
		{"合法值保持", 3, 50, 3, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPage, gotSize := Normalize(c.page, c.pageSize)
			if gotPage != c.wantPage || gotSize != c.wantPageSize {
				t.Fatalf("Normalize(%d,%d) = (%d,%d), want (%d,%d)",
					c.page, c.pageSize, gotPage, gotSize, c.wantPage, c.wantPageSize)
			}
		})
	}
}

func TestOffset(t *testing.T) {
	cases := []struct {
		name                 string
		page, pageSize       int
		wantOffset, wantSize int
	}{
		{"零值", 0, 0, 0, 20},
		{"第一页", 1, 20, 0, 20},
		{"深页", 3, 50, 100, 50},
		{"超大页大小", 2, 500, 100, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotOffset, gotSize := Offset(c.page, c.pageSize)
			if gotOffset != c.wantOffset || gotSize != c.wantSize {
				t.Fatalf("Offset(%d,%d) = (%d,%d), want (%d,%d)",
					c.page, c.pageSize, gotOffset, gotSize, c.wantOffset, c.wantSize)
			}
		})
	}
}

func TestNormalizeWithDefault(t *testing.T) {
	if gotPage, gotSize := NormalizeWithDefault(0, 0, 50); gotPage != 1 || gotSize != 50 {
		t.Fatalf("NormalizeWithDefault(0,0,50) = (%d,%d), want (1,50)", gotPage, gotSize)
	}
	// 默认值本身也要受上限约束，避免调用方写死一个过大的默认页。
	if gotPage, gotSize := NormalizeWithDefault(2, 0, 5000); gotPage != 2 || gotSize != MaxPageSize {
		t.Fatalf("NormalizeWithDefault(2,0,5000) = (%d,%d), want (2,%d)", gotPage, gotSize, MaxPageSize)
	}
}

func TestWindow(t *testing.T) {
	if from, size, err := Window(1, 20, MaxResultWindow); err != nil || from != 0 || size != 20 {
		t.Fatalf("Window(1,20) = (%d,%d,%v), want (0,20,nil)", from, size, err)
	}
	// 最后一页刚好贴住窗口上限时仍然允许。
	lastPage := MaxResultWindow / MaxPageSize
	if _, _, err := Window(lastPage, MaxPageSize, MaxResultWindow); err != nil {
		t.Fatalf("贴住窗口上限的页不应被拒绝: %v", err)
	}
	if _, _, err := Window(lastPage+1, MaxPageSize, MaxResultWindow); !errors.Is(err, ErrPageTooDeep) {
		t.Fatalf("越过窗口上限应返回 ErrPageTooDeep, got %v", err)
	}
}

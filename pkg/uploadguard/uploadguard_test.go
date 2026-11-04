package uploadguard

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadAll_EnforcesLimit(t *testing.T) {
	if _, err := ReadAll(strings.NewReader("12345"), 5, 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("declared 超限应提前失败，实际 %v", err)
	}

	if _, err := ReadAll(strings.NewReader("12345"), 0, 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("实际读取超限应失败，实际 %v", err)
	}

	data, err := ReadAll(strings.NewReader("1234"), 0, 4)
	if err != nil {
		t.Fatalf("刚好等于上限应成功: %v", err)
	}
	if string(data) != "1234" {
		t.Fatalf("data = %q", data)
	}
}

func TestCopy_EnforcesLimit(t *testing.T) {
	var buf bytes.Buffer
	n, err := Copy(&buf, strings.NewReader("123456"), 4)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("copy 超限应失败，实际 %v", err)
	}
	if n <= 4 {
		t.Fatalf("n = %d，应读到上限之后才能判断超限", n)
	}

	buf.Reset()
	n, err = Copy(&buf, strings.NewReader("1234"), 4)
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestSanitizeExt(t *testing.T) {
	cases := map[string]string{
		"a.PNG":                        ".png",
		"a.tar.gz":                     ".gz",
		"a.<script>":                   "",
		"a." + strings.Repeat("x", 20): "",
		"a":                            "",
		"a.中文":                         "",
		"a.123":                        ".123",
	}
	for name, want := range cases {
		if got := SanitizeExt(name); got != want {
			t.Errorf("SanitizeExt(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestImageContentType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if ct, err := ImageContentType(png); err != nil || ct != "image/png" {
		t.Fatalf("png 识别失败: ct=%q err=%v", ct, err)
	}

	html := []byte("<!doctype html><html><body>x</body></html>")
	if _, err := ImageContentType(html); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("HTML 伪装图片应拒绝，实际 %v", err)
	}
}

func TestLimitBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("12345"))
	req.ContentLength = 5
	rec := httptest.NewRecorder()
	if LimitBody(rec, req, 4) {
		t.Fatal("Content-Length 超限应直接拒绝")
	}

	req = httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("12345"))
	req.ContentLength = -1 // 模拟 chunked
	rec = httptest.NewRecorder()
	if !LimitBody(rec, req, 4) {
		t.Fatal("未知长度应允许进入，由读取阶段兜底")
	}
	if _, err := io.ReadAll(req.Body); !IsTooLarge(err) {
		t.Fatalf("读取超限应返回大小错误，实际 %v", err)
	}
}

func TestIsTooLarge(t *testing.T) {
	if IsTooLarge(nil) {
		t.Fatal("nil 不应算超限")
	}
	if !IsTooLarge(&http.MaxBytesError{Limit: 10}) {
		t.Fatal("MaxBytesError 应算超限")
	}
	if !IsTooLarge(ErrTooLarge) {
		t.Fatal("ErrTooLarge 应算超限")
	}
}

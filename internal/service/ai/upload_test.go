package ai

import "testing"

func TestSafeProxyContentType(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantType     string
		wantDownload bool
	}{
		{"jpeg inline", "image/jpeg", "image/jpeg", false},
		{"png with charset", "image/png; charset=binary", "image/png", false},
		{"mp4 inline", "video/mp4", "video/mp4", false},
		{"pdf inline", "application/pdf", "application/pdf", false},
		{"html forced download", "text/html", "application/octet-stream", true},
		{"html with charset forced download", "text/html; charset=utf-8", "application/octet-stream", true},
		{"svg forced download", "image/svg+xml", "application/octet-stream", true},
		{"xhtml forced download", "application/xhtml+xml", "application/octet-stream", true},
		{"javascript forced download", "application/javascript", "application/octet-stream", true},
		{"empty forced download", "", "application/octet-stream", true},
		{"unknown forced download", "application/x-sh", "application/octet-stream", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotDownload := SafeProxyContentType(tc.raw)
			if gotType != tc.wantType || gotDownload != tc.wantDownload {
				t.Fatalf("SafeProxyContentType(%q) = (%q, %v), want (%q, %v)",
					tc.raw, gotType, gotDownload, tc.wantType, tc.wantDownload)
			}
		})
	}
}

func TestValidateObjectKey(t *testing.T) {
	valid := []string{
		"qq-media/2026/11/04/0b7c2f9e-1c3d-4a5b-8e9f-0123456789ab.jpg",
		"avatars/user-1.png",
		"2026/11/04/report.pdf",
	}
	for _, key := range valid {
		if err := ValidateObjectKey(key); err != nil {
			t.Fatalf("合法对象键被拒绝 %q: %v", key, err)
		}
	}

	invalid := []string{
		"",
		"/absolute/path.jpg",
		"a//b.jpg",
		"../secret",
		"a/../../b",
		"./a",
		`a\b.jpg`,
		"a/\x00/b",
	}
	for _, key := range invalid {
		if err := ValidateObjectKey(key); err == nil {
			t.Fatalf("非法对象键未被拒绝 %q", key)
		}
	}
}

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				_ = os.Chdir(dir)
				_ = os.Setenv("LOGOS_CONFIG_PATH", filepath.Join(dir, "config", "testdata", "config.yaml"))
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	os.Exit(m.Run())
}

func TestParseObjectArray_ExtractsWrappedJSON(t *testing.T) {
	svc := &extractionServiceImpl{}
	raw := "```json\n[{\"text\":\"Logos\",\"type\":\"PRODUCT\",\"confidence\":0.9}]\n```"

	got, err := svc.parseObjectArray(context.Background(), raw, nil,
		`[{"text":"实体文本","type":"PERSON","confidence":0.9}]`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 1 || got[0]["text"] != "Logos" {
		t.Fatalf("解析结果 = %#v, want Logos", got)
	}
}

func TestParseObjectArray_InvalidOutputReturnsError(t *testing.T) {
	svc := &extractionServiceImpl{}

	_, err := svc.parseObjectArray(context.Background(), "not json", nil,
		`[{"text":"实体文本","type":"PERSON","confidence":0.9}]`)
	if err == nil {
		t.Fatal("模型输出非法且无法修复时应返回错误，而不是伪装成空结果")
	}
}

func TestParseStringArray_ExtractsWrappedJSON(t *testing.T) {
	svc := &extractionServiceImpl{}

	got, err := svc.parseStringArray(context.Background(),
		"结果如下：[\"向量检索\",\"RRF\"]", nil, `["关键词1","关键词2"]`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 2 || got[0] != "向量检索" {
		t.Fatalf("解析结果 = %#v, want [向量检索 RRF]", got)
	}
}

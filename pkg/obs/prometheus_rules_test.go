package obs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 告警规则是部署产物，写错了只有等 Prometheus 加载失败才会发现。
// 这里做最小结构校验：每条规则必须有 alert 和 expr，且 expr 不能为空。
func TestPrometheusRules_AreWellFormed(t *testing.T) {
	path := filepath.Join("..", "..", "deployment", "examples", "prometheus-rules.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取规则文件失败: %v", err)
	}

	groups, err := parsePrometheusRuleGroups(data)
	if err != nil {
		t.Fatalf("规则文件不是合法 YAML: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("规则文件没有任何 group")
	}

	alerts := 0
	for _, g := range groups {
		for _, r := range g.Rules {
			alerts++
			if strings.TrimSpace(r.Alert) == "" {
				t.Fatalf("group %s 存在缺少 alert 名称的规则", g.Name)
			}
			if strings.TrimSpace(r.Expr) == "" {
				t.Fatalf("告警 %s 缺少 expr", r.Alert)
			}
			if r.Labels.Severity == "" {
				t.Fatalf("告警 %s 缺少 severity 标签", r.Alert)
			}
		}
	}
	if alerts == 0 {
		t.Fatal("规则文件没有任何告警")
	}
}

// QQ 媒体中转失败率告警的分母必须按 stage 分组，否则 summary 里的
// {{ $labels.stage }} 会为空，告警信息无法定位是下载还是上传出问题。
func TestPrometheusRules_QQMediaAlertGroupsByStage(t *testing.T) {
	path := filepath.Join("..", "..", "deployment", "examples", "prometheus-rules.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取规则文件失败: %v", err)
	}

	groups, err := parsePrometheusRuleGroups(data)
	if err != nil {
		t.Fatalf("规则文件不是合法 YAML: %v", err)
	}

	for _, g := range groups {
		for _, r := range g.Rules {
			if r.Alert != "QQMediaRelayFailing" {
				continue
			}
			if !strings.Contains(r.Expr, "sum by (stage)") {
				t.Fatalf("QQ 媒体告警缺少 stage 分组: %s", r.Expr)
			}
			return
		}
	}
	t.Fatal("未找到 QQMediaRelayFailing 告警")
}

// 快速路径补偿失败必须是 critical：这条链路失败时用户消息不会再有任何回复。
// 同时表达式必须带 reason 分组，否则无法区分「Bot 超时」和「Kafka 发不出去」。
func TestPrometheusRules_QQFastPathRecoveryAlertIsCritical(t *testing.T) {
	path := filepath.Join("..", "..", "deployment", "examples", "prometheus-rules.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取规则文件失败: %v", err)
	}

	groups, err := parsePrometheusRuleGroups(data)
	if err != nil {
		t.Fatalf("规则文件不是合法 YAML: %v", err)
	}

	for _, g := range groups {
		for _, r := range g.Rules {
			if r.Alert != "QQFastPathRecoveryFailing" {
				continue
			}
			if r.Labels.Severity != "critical" {
				t.Fatalf("快速路径补偿失败应为 critical，实际 %q", r.Labels.Severity)
			}
			if !strings.Contains(r.Expr, "logos_qqbridge_fastpath_count_total") {
				t.Fatalf("告警表达式引用了错误的指标: %s", r.Expr)
			}
			if !strings.Contains(r.Expr, "sum by (reason)") {
				t.Fatalf("快速路径告警缺少 reason 分组: %s", r.Expr)
			}
			return
		}
	}
	t.Fatal("未找到 QQFastPathRecoveryFailing 告警")
}

// 容量类告警必须与故障类告警分开：它们引用同一个指标的不同 result，
// 但处理动作是「扩容」而不是「修 Bug」。这里校验 busy/queue 两个标签确实存在。
func TestPrometheusRules_QQCapacityAlertsUseDedicatedLabels(t *testing.T) {
	path := filepath.Join("..", "..", "deployment", "examples", "prometheus-rules.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取规则文件失败: %v", err)
	}

	groups, err := parsePrometheusRuleGroups(data)
	if err != nil {
		t.Fatalf("规则文件不是合法 YAML: %v", err)
	}

	wanted := map[string]string{
		"QQFastPathSaturated":   `result="busy"`,
		"QQMediaQueueSaturated": `stage="queue"`,
	}
	found := map[string]bool{}
	for _, g := range groups {
		for _, r := range g.Rules {
			need, ok := wanted[r.Alert]
			if !ok {
				continue
			}
			if !strings.Contains(r.Expr, need) {
				t.Fatalf("告警 %s 应引用 %s，实际: %s", r.Alert, need, r.Expr)
			}
			found[r.Alert] = true
		}
	}
	for alert := range wanted {
		if !found[alert] {
			t.Fatalf("未找到告警 %s", alert)
		}
	}
}

type prometheusRuleGroup struct {
	Name  string `yaml:"name"`
	Rules []struct {
		Alert  string `yaml:"alert"`
		Expr   string `yaml:"expr"`
		For    string `yaml:"for"`
		Labels struct {
			Severity string `yaml:"severity"`
		} `yaml:"labels"`
	} `yaml:"rules"`
}

// 规则文件是 PrometheusRule 自定义资源，groups 在 spec 下而不是顶层。
// 直接按顶层 groups 解析会得到空列表，测试反而「通过」得毫无意义。
func parsePrometheusRuleGroups(data []byte) ([]prometheusRuleGroup, error) {
	var doc struct {
		Spec struct {
			Groups []prometheusRuleGroup `yaml:"groups"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Spec.Groups, nil
}

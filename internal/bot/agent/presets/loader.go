package presets

import (
	"Logos/internal/bot/agent"
	"Logos/pkg/logger"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type BotPreset struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	SystemPrompt string `yaml:"system_prompt"`
	Enabled      bool   `yaml:"enabled"`
}

type PresetsConfig struct {
	Bots []BotPreset `yaml:"bots"`
}

var DefaultPresetsYAMLPath = filepath.Join("internal", "bot", "agent", "presets", "prompt", "presets.yaml")

func LoadPresetsFromYAML(mgr *agent.AgentManager, filePath string) error {
	if filePath == "" {
		filePath = DefaultPresetsYAMLPath
	}

	logger.Info("从 YAML 加载 Bot 预设", logger.StringField("path", filePath))

	data, err := os.ReadFile(filePath)
	if err != nil {
		logger.Warn("读取 YAML 文件失败，使用硬编码预设", logger.ErrorField(err))
		return err
	}

	presets, err := parsePresets(data)
	if err != nil {
		// 只返回错误，由 RegisterAllPresets 决定回退。
		// 旧实现在这里直接 return RegisterAllPresets(mgr)，而 RegisterAllPresets
		// 又会调用 LoadPresetsFromYAML(默认路径)——同一个文件再次解析失败 →
		// 无限递归，进程以 stack overflow 崩溃，而不是「回退到硬编码预设」。
		logger.Warn("解析 YAML 失败，交由上层回退到硬编码预设", logger.ErrorField(err))
		return err
	}

	registered, failed := 0, 0
	for _, bot := range presets {
		_, err := mgr.RegisterPresetAgent(&agent.AgentConfig{
			ID:           "preset_" + bot.Name,
			Name:         bot.Name,
			Description:  bot.Description,
			SystemPrompt: bot.SystemPrompt,
		})
		if err != nil {
			failed++
			logger.Error("从 YAML 注册 Bot 失败", logger.StringField("name", bot.Name), logger.ErrorField(err))
			continue
		}

		registered++
		logger.Info("从 YAML 注册 Bot 预设成功", logger.StringField("name", bot.Name))
	}

	// 一条都没注册成功时把错误抛回去，让上层走硬编码兜底；
	// 否则应用会「启动成功但没有任何预设 Bot」，问题被日志淹掉。
	if registered == 0 && failed > 0 {
		return fmt.Errorf("YAML 预设全部注册失败（%d 个），回退到硬编码预设", failed)
	}

	logger.Info("从 YAML 加载 Bot 预设完成", logger.IntField("registered", registered))
	return nil
}

// parsePresets 解析并清洗预设列表：
//   - 跳过未启用项（YAML 里显式 enabled: true 才生效）；
//   - 丢弃空名（空名会塌缩成同一个 ID "preset_"，互相覆盖）；
//   - 同名只保留首个，避免后项静默覆盖前项。
func parsePresets(data []byte) ([]BotPreset, error) {
	var config PresetsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析预设 YAML 失败: %w", err)
	}

	seen := make(map[string]struct{}, len(config.Bots))
	out := make([]BotPreset, 0, len(config.Bots))

	for _, bot := range config.Bots {
		if !bot.Enabled {
			continue
		}
		bot.Name = strings.TrimSpace(bot.Name)
		if bot.Name == "" {
			continue
		}
		if _, dup := seen[bot.Name]; dup {
			continue
		}
		seen[bot.Name] = struct{}{}
		out = append(out, bot)
	}

	return out, nil
}

package presets

import (
	"Logos/internal/bot/agent"
	"Logos/pkg/logger"
	"os"
	"path/filepath"

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

var DefaultPresetsYAMLPath = filepath.Join("internal", "bot", "agent", "presets", "promot", "presets.yaml")

func LoadPresetsFromYAML(mgr *agent.AgentManager, filePath string) error {
	if filePath == "" {
		filePath = DefaultPresetsYAMLPath
	}

	logger.Info("从 YAML 加载 Bot 预设", logger.StringField("path", filePath))

	data, err := os.ReadFile(filePath)
	if err != nil {
		logger.Warn("读取 YAML 文件失败，回退到硬编码预设", logger.ErrorField(err))
		return RegisterAllPresets(mgr)
	}

	var config PresetsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		logger.Warn("解析 YAML 失败，回退到硬编码预设", logger.ErrorField(err))
		return RegisterAllPresets(mgr)
	}

	for _, bot := range config.Bots {
		if !bot.Enabled {
			continue
		}

		_, err := mgr.RegisterPresetAgent(&agent.AgentConfig{
			ID:           "preset_" + bot.Name,
			Name:         bot.Name,
			Description:  bot.Description,
			SystemPrompt: bot.SystemPrompt,
		})
		if err != nil {
			logger.Error("从 YAML 注册 Bot 失败", logger.StringField("name", bot.Name), logger.ErrorField(err))
			continue
		}

		logger.Info("从 YAML 注册 Bot 预设成功", logger.StringField("name", bot.Name))
	}

	logger.Info("从 YAML 加载 Bot 预设完成")
	return nil
}

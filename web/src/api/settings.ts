const SETTINGS_KEY = 'logos_ai_settings'

export interface AIModelConfig {
  provider: string
  model: string
  apiKey: string
  baseUrl: string
  temperature: number
}

export interface AISettings {
  summary: AIModelConfig
  reply: AIModelConfig
  todo: AIModelConfig
  translation: AIModelConfig
  defaultMessageCount: number
}

const defaultModel: AIModelConfig = {
  provider: 'openai',
  model: 'gpt-4o-mini',
  apiKey: '',
  baseUrl: 'https://api.openai.com/v1',
  temperature: 0.7,
}

const defaultSettings: AISettings = {
  summary: { ...defaultModel },
  reply: { ...defaultModel },
  todo: { ...defaultModel },
  translation: { ...defaultModel },
  defaultMessageCount: 50,
}

export function getAISettings(): AISettings {
  try {
    const stored = localStorage.getItem(SETTINGS_KEY)
    if (stored) {
      const parsed = JSON.parse(stored)
      return {
        summary: { ...defaultModel, ...parsed.summary },
        reply: { ...defaultModel, ...parsed.reply },
        todo: { ...defaultModel, ...parsed.todo },
        translation: { ...defaultModel, ...parsed.translation },
        defaultMessageCount: parsed.defaultMessageCount || 50,
      }
    }
  } catch {}
  return defaultSettings
}

export function saveAISettings(settings: AISettings): void {
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings))
}

export const AI_PROVIDERS = [
  { value: 'openai', label: 'OpenAI', defaultUrl: 'https://api.openai.com/v1', defaultModel: 'gpt-4o-mini' },
  { value: 'claude', label: 'Anthropic Claude', defaultUrl: 'https://api.anthropic.com/v1', defaultModel: 'claude-3-5-sonnet-20241022' },
  { value: 'qianfan', label: '百度千帆', defaultUrl: 'https://aip.baidubce.com/rpc/2.0/ai_custom/v1', defaultModel: 'ernie-4.0-8k' },
  { value: 'deepseek', label: 'DeepSeek', defaultUrl: 'https://api.deepseek.com/v1', defaultModel: 'deepseek-chat' },
  { value: 'custom', label: '自定义', defaultUrl: '', defaultModel: '' },
]

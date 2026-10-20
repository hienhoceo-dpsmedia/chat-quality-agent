export const MASKED_AI_KEY = '••••••••'

export interface AISettingsForm {
  provider: string
  model: string
  apiKey: string
  baseUrl: string
  batchMode: boolean
  batchSize: number
  timeoutSeconds?: number
}

// Shared by save, test and discovery: tests use exactly what is in the form.
export function aiSettingsPayload(form: AISettingsForm, useCustomBaseUrl: boolean) {
  const custom = form.provider === 'custom'
  const key = (form.apiKey || '').trim()
  return {
    provider: form.provider,
    model: (form.model || '').trim(),
    base_url: custom || useCustomBaseUrl ? (form.baseUrl || '').trim() : '',
    api_key: key === MASKED_AI_KEY ? '' : key,
    clear_api_key: custom && key === '',
    timeout_seconds: custom ? Number(form.timeoutSeconds ?? 120) : undefined,
    batch_mode: form.batchMode ? 'true' : 'false',
    batch_size: String(form.batchSize),
  }
}

export function validateAISettings(form: AISettingsForm, useCustomBaseUrl: boolean, hasSavedKey: boolean, requireModel = true): string {
  const payload = aiSettingsPayload(form, useCustomBaseUrl)
  if (form.provider === 'custom' && requireModel && !payload.model) {
    return 'Vui lòng nhập model cho Custom AI'
  }
  if (form.provider === 'custom' && (!Number.isInteger(payload.timeout_seconds) || payload.timeout_seconds! < 5 || payload.timeout_seconds! > 600)) {
    return 'Timeout phải là số nguyên từ 5 đến 600 giây'
  }
  if (form.provider === 'custom' || useCustomBaseUrl) {
    try {
      const url = new URL(payload.base_url)
      if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || payload.base_url.includes('#')) {
        throw new Error('invalid URL')
      }
    } catch {
      return 'Endpoint phải là URL HTTP(S) hợp lệ, không chứa tài khoản hoặc fragment'
    }
  }
  if (form.provider !== 'custom' && !payload.api_key && !hasSavedKey) {
    return 'Vui lòng nhập API key'
  }
  return ''
}

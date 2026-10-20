import { describe, expect, it } from 'vitest'
import { aiSettingsPayload, validateAISettings, MASKED_AI_KEY, type AISettingsForm } from './aiSettings'

const custom: AISettingsForm = {
  provider: 'custom', model: 'org/model:latest', apiKey: '',
  baseUrl: 'https://example.test/v1/chat/completions', batchMode: true, batchSize: 5,
}

describe('Custom AI settings form', () => {
  it('preserves a complete endpoint and arbitrary model; explicitly clears the optional key', () => {
    expect(validateAISettings(custom, false, false)).toBe('')
    expect(aiSettingsPayload(custom, false)).toMatchObject({
      provider: 'custom', model: 'org/model:latest', base_url: custom.baseUrl,
      api_key: '', clear_api_key: true,
    })
  })

  it('sends a new bearer key but never sends the masked display value', () => {
    expect(aiSettingsPayload({ ...custom, apiKey: ' new-key ' }, true)).toMatchObject({ api_key: 'new-key', clear_api_key: false })
    expect(aiSettingsPayload({ ...custom, apiKey: MASKED_AI_KEY }, true)).toMatchObject({ api_key: '', clear_api_key: false })
  })

  it('requires a model to save/test, but allows discovering before a model is chosen', () => {
    expect(validateAISettings({ ...custom, model: ' ' }, true, false)).toContain('model')
    expect(validateAISettings({ ...custom, model: '' }, true, false, false)).toBe('')
  })

  it('defaults custom timeout to 120 seconds and preserves an explicit timeout', () => {
    expect(aiSettingsPayload(custom, true).timeout_seconds).toBe(120)
    expect(aiSettingsPayload({ ...custom, timeoutSeconds: 300 }, true).timeout_seconds).toBe(300)
    expect(validateAISettings({ ...custom, timeoutSeconds: 600 }, true, false)).toBe('')
  })

  it.each([0, 4, 601, 5.5, NaN])('rejects custom timeout %s', (timeoutSeconds) => {
    expect(validateAISettings({ ...custom, timeoutSeconds }, true, false)).toContain('Timeout')
  })

  it.each(['', 'ftp://example.test/v1', 'https://u:p@example.test/v1', 'https://example.test/v1#fragment'])('rejects invalid custom endpoint %s', (baseUrl) => {
    expect(validateAISettings({ ...custom, baseUrl }, true, false)).toContain('HTTP(S)')
  })

  it('keeps builtin key requirements and does not send a hidden custom endpoint', () => {
    const openai = { ...custom, provider: 'openai' }
    expect(validateAISettings(openai, false, false)).toContain('API key')
    expect(validateAISettings(openai, false, true)).toBe('')
    expect(aiSettingsPayload(openai, false)).toMatchObject({ base_url: '', clear_api_key: false })
  })
})

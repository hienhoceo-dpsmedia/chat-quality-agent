import { effectScope, reactive } from 'vue'
import { describe, expect, it } from 'vitest'
import { useAIModelCatalog } from './aiModelCatalog'

describe('AI model discovery target isolation', () => {
  it('ignores initial-fetch responses after switching provider or endpoint', async () => {
    const scope = effectScope()
    const target = reactive({ provider: 'claude', endpoint: '' })
    const catalog = scope.run(() => useAIModelCatalog(() => JSON.stringify(target)))!
    let finish!: (data: { models: { id: string; title: string }[] }) => void
    const pending = catalog.loadCatalog(() => new Promise(resolve => { finish = resolve }))
    target.provider = 'custom'
    target.endpoint = 'https://new.example/v1'
    await catalog.loadCatalog(async () => ({ models: [{ id: 'new-model', title: 'New' }], source: 'provider' }))
    finish({ models: [{ id: 'old-model', title: 'Old' }] })
    await pending
    expect(catalog.fetchedModels.value.map(m => m.id)).toEqual(['new-model'])
    scope.stop()
  })

  it('does not let a stale failure erase a newer model list', async () => {
    const scope = effectScope()
    const target = reactive({ endpoint: 'https://old.example' })
    const catalog = scope.run(() => useAIModelCatalog(() => target.endpoint))!
    let fail!: (error: Error) => void
    const pending = catalog.loadCatalog(() => new Promise((_, reject) => { fail = reject }))
    target.endpoint = 'https://new.example'
    await catalog.loadCatalog(async () => ({ models: [{ id: 'new-model', title: 'New' }], source: 'provider' }))
    fail(new Error('old endpoint unavailable'))
    await pending
    expect(catalog.fetchedModels.value[0]?.id).toBe('new-model')
    expect(catalog.modelsSource.value).toBe('provider')
    scope.stop()
  })

  it('clears discovered models immediately when the key is replaced or removed, without changing typed model', async () => {
    const scope = effectScope()
    const form = reactive({ key: 'key-A', model: 'typed-model' })
    const catalog = scope.run(() => useAIModelCatalog(() => form.key))!
    for (const nextKey of ['key-B', '']) {
      await catalog.loadCatalog(async () => ({ models: [{ id: 'restricted-model', title: 'Restricted' }], source: 'provider' }))
      form.key = nextKey
      expect(catalog.fetchedModels.value).toEqual([])
      expect(catalog.modelsSource.value).toBe('')
      expect(form.model).toBe('typed-model')
    }
    scope.stop()
  })
})

import { ref, watch } from 'vue'

interface ModelCatalog {
  models?: { id: string; title: string }[]
  source?: string
}

// Invalidate on provider/endpoint/credential changes, including in-flight requests.
export function useAIModelCatalog(target: () => string) {
  const fetchedModels = ref<{ id: string; title: string }[]>([])
  const modelsSource = ref('')
  let generation = 0
  watch(target, () => {
    generation++
    fetchedModels.value = []
    modelsSource.value = ''
  }, { flush: 'sync' })

  async function loadCatalog(request: () => Promise<ModelCatalog>) {
    const current = ++generation
    const identity = target()
    const isCurrent = () => current === generation && identity === target()
    try {
      const data = await request()
      if (!isCurrent()) return
      fetchedModels.value = data.models || []
      modelsSource.value = data.source || ''
      return data
    } catch (error) {
      if (!isCurrent()) return
      fetchedModels.value = []
      modelsSource.value = 'static'
      throw error
    }
  }

  return { fetchedModels, modelsSource, loadCatalog }
}

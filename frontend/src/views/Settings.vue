<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-6">{{ $t('settings') }}</h1>

    <v-row>
      <v-col cols="12" md="3">
        <v-card class="pa-2">
          <v-list density="compact" nav>
            <v-list-item
              v-for="tab in tabs"
              :key="tab.value"
              :active="activeTab === tab.value"
              :prepend-icon="tab.icon"
              :title="$t(tab.label)"
              rounded="lg"
              color="primary"
              @click="activeTab = tab.value"
            />
          </v-list>
        </v-card>
      </v-col>

      <v-col cols="12" md="9">
        <!-- AI Config -->
        <v-card v-if="activeTab === 'ai'" class="pa-6">
          <div class="text-subtitle-1 font-weight-bold mb-4">
            <v-icon start size="small">mdi-robot</v-icon>
            {{ $t('ai_config') }}
          </div>

          <v-select
            v-model="aiSettings.provider"
            :label="$t('ai_provider')"
            :items="providerOptions"
            class="mb-3"
            @update:model-value="onProviderChange"
          />

          <div class="d-flex align-start ga-2 mb-3">
            <v-combobox
              v-if="aiSettings.provider === 'custom'"
              v-model="aiSettings.model"
              label="Model ID"
              :items="modelOptions"
              :return-object="false"
              hint="Nhập đúng tên model trên máy chủ, hoặc chọn từ danh sách khi có hỗ trợ /models"
              persistent-hint
              class="flex-grow-1"
            />
            <v-select
              v-else
              v-model="aiSettings.model"
              :label="$t('ai_model')"
              :items="modelOptions"
              hide-details="auto"
              class="flex-grow-1"
            />
            <v-btn
              icon="mdi-refresh"
              variant="text"
              size="small"
              class="mt-2"
              :loading="refreshingModels"
              :title="$t('refresh_model_list')"
              @click="refreshModelList"
            />
          </div>
          <div v-if="modelsSource === 'static' && aiSettings.provider !== 'custom'" class="text-caption text-grey mb-3">
            {{ $t('model_list_static') }}
          </div>

          <v-text-field
            v-model="aiSettings.apiKey"
            :label="aiSettings.provider === 'custom' ? 'API key (tùy chọn)' : $t('api_key')"
            :hint="aiSettings.provider === 'custom' ? 'Để trống nếu máy chủ không yêu cầu xác thực. Xóa nội dung ô này để bỏ key đã lưu.' : undefined"
            :persistent-hint="aiSettings.provider === 'custom'"
            :clearable="aiSettings.provider === 'custom'"
            :type="showKey ? 'text' : 'password'"
            :append-inner-icon="showKey ? 'mdi-eye-off' : 'mdi-eye'"
            @click:append-inner="showKey = !showKey"
            class="mb-3"
          />

          <v-switch
            v-if="aiSettings.provider !== 'custom'"
            v-model="useCustomBaseUrl"
            label="Tùy chỉnh API URL"
            color="primary"
            density="compact"
            hide-details
            class="mt-1 mb-2"
            @update:model-value="onEndpointChange"
          />
          <div v-if="aiSettings.provider === 'custom'" class="text-caption text-grey mb-2">Tương thích OpenAI Chat Completions. Có thể dán endpoint đầy đủ; ứng dụng không nối lặp /chat/completions.</div>
          <div v-else-if="!useCustomBaseUrl" class="text-caption text-grey mb-2">Bật khi cần dùng proxy (OpenRouter, LiteLLM, CLIProxy) hoặc máy chủ tự dựng</div>
          <div v-else class="text-caption text-grey mb-2">Làm mới model và kiểm tra kết nối dùng cấu hình đang nhập, không cần lưu trước.</div>

          <v-text-field
            v-if="useCustomBaseUrl || aiSettings.provider === 'custom'"
            v-model="aiSettings.baseUrl"
            :label="aiSettings.provider === 'custom' ? 'Endpoint / Base URL' : 'Base URL'"
            :placeholder="baseUrlPlaceholder"
            :hint="aiSettings.provider === 'custom' ? 'Ví dụ: https://may-chu-cua-ban/v1/chat/completions hoặc https://may-chu-cua-ban/v1' : 'Để trống để dùng mặc định'"
            persistent-hint
            clearable
            :rules="[
              v => !!v || 'Vui lòng nhập URL hoặc tắt tùy chỉnh',
              v => !v || v.startsWith('http://') || v.startsWith('https://') || 'URL phải bắt đầu bằng http:// hoặc https://',
            ]"
            class="mb-3"
            @update:model-value="onEndpointChange"
          />

          <v-text-field
            v-if="aiSettings.provider === 'custom'"
            v-model.number="aiSettings.timeoutSeconds"
            label="Thời gian chờ / Timeout (giây)"
            type="number"
            min="5"
            max="600"
            step="1"
            hint="Từ 5 đến 600 giây. Áp dụng cho kiểm tra kết nối và mỗi lượt phân tích AI, kể cả batch."
            persistent-hint
            class="mb-3"
          />

          <div class="d-flex ga-2 flex-wrap">
            <v-btn color="primary" :loading="savingAI" @click="saveAI">{{ $t('save_settings') }}</v-btn>
            <v-btn variant="outlined" :loading="testingKey" @click="testKey">Kiểm tra kết nối</v-btn>
          </div>
          <v-alert v-if="aiTestResult" class="mt-4" :type="aiTestResult.ok ? 'success' : 'error'" variant="tonal" density="compact">
            {{ aiTestResult.message }}
          </v-alert>
        </v-card>

        <!-- General -->
        <!-- Analysis Settings -->
        <v-card v-if="activeTab === 'analysis'" class="pa-6">
          <div class="text-subtitle-1 font-weight-bold mb-4">
            <v-icon start size="small">mdi-chart-bar</v-icon>
            Cài đặt phân tích
          </div>

          <div class="text-subtitle-2 mb-2">Chế độ Batch (tối ưu chi phí)</div>
          <v-switch
            v-model="aiSettings.batchMode"
            label="Bật chế độ Batch"
            hint="Gom nhiều cuộc chat vào 1 lần gọi AI. Tiết kiệm token nhưng có thể giảm độ chính xác."
            persistent-hint
            density="compact"
            color="primary"
            class="mb-3"
          />
          <v-select
            v-if="aiSettings.batchMode"
            v-model="aiSettings.batchSize"
            label="Số cuộc chat / batch"
            :items="[3, 5, 10, 15, 20, 30]"
            density="compact"
            class="mb-4"
            style="max-width: 200px"
          />

          <v-btn color="primary" :loading="savingAnalysis" @click="saveAnalysis">Lưu cài đặt</v-btn>
        </v-card>

        <!-- Storage -->
        <v-card v-if="activeTab === 'storage'" class="pa-6">
          <div class="text-subtitle-1 font-weight-bold mb-1">
            <v-icon start size="small">mdi-folder-multiple-image</v-icon>
            {{ $t('storage_settings') }}
          </div>
          <div class="text-body-2 text-grey mb-5">{{ $t('storage_intro') }}</div>

          <v-alert
            :type="storage.backend === 's3' ? 'success' : 'info'"
            variant="tonal"
            density="comfortable"
            class="mb-5"
          >
            <span v-if="storage.backend === 's3'">
              {{ $t('storage_now_s3') }} <strong>{{ storage.bucket }}</strong> · {{ storageHost }}
            </span>
            <span v-else>
              {{ $t('storage_now_local') }}<template v-if="storageLocalUsage"> — {{ storageLocalUsage }}</template>
            </span>
          </v-alert>

          <v-switch
            v-model="dungS3"
            color="primary"
            density="compact"
            hide-details
            class="mb-2"
            :label="$t('storage_use_s3')"
            @update:model-value="onToggleS3"
          />

          <v-expand-transition>
            <div v-if="dungS3">
              <v-divider class="my-4" />

              <v-text-field
                v-model="storage.endpoint"
                :label="$t('storage_endpoint')"
                placeholder="https://s3.nha-cung-cap.vn"
                density="comfortable"
                class="mb-3"
                hide-details="auto"
                @update:model-value="testResult = null"
              />

              <v-row dense>
                <v-col cols="12" sm="7">
                  <v-text-field
                    v-model="storage.bucket"
                    :label="$t('storage_bucket')"
                    density="comfortable"
                    hide-details="auto"
                    @update:model-value="testResult = null"
                  />
                </v-col>
                <v-col cols="12" sm="5">
                  <v-text-field
                    v-model="storage.region"
                    :label="$t('storage_region')"
                    density="comfortable"
                    hide-details="auto"
                    @update:model-value="testResult = null"
                  />
                </v-col>
              </v-row>

              <v-text-field
                v-model="storage.access_key"
                label="Access Key"
                density="comfortable"
                class="mt-3"
                hide-details="auto"
                @update:model-value="testResult = null"
              />

              <v-text-field
                v-model="storage.secret_key"
                label="Secret Key"
                type="password"
                density="comfortable"
                class="mt-3"
                hide-details="auto"
                :placeholder="storage.secret_key_da_luu ? '••••••••' : ''"
                :hint="storage.secret_key_da_luu ? $t('storage_secret_saved') : ''"
                persistent-hint
                @update:model-value="testResult = null"
              />

              <v-expansion-panels variant="accordion" class="mt-4">
                <v-expansion-panel elevation="0">
                  <v-expansion-panel-title class="text-body-2">{{ $t('advanced_options') }}</v-expansion-panel-title>
                  <v-expansion-panel-text>
                    <v-text-field
                      v-model="storage.prefix"
                      :label="$t('storage_prefix')"
                      density="comfortable"
                      hide-details="auto"
                      @update:model-value="testResult = null"
                    />
                    <v-switch
                      v-model="storage.force_path_style"
                      color="primary"
                      density="compact"
                      hide-details
                      class="mt-2"
                      :label="$t('storage_path_style')"
                      @update:model-value="testResult = null"
                    />
                  </v-expansion-panel-text>
                </v-expansion-panel>
              </v-expansion-panels>

              <v-alert
                v-if="testResult"
                :type="testResult.ok ? 'success' : 'error'"
                variant="tonal"
                density="comfortable"
                class="mt-4"
              >
                {{ testResult.message }}
              </v-alert>

              <div class="d-flex flex-wrap align-center ga-3 mt-5">
                <v-btn variant="outlined" :loading="testingStorage" prepend-icon="mdi-lan-connect" @click="testStorage">
                  {{ $t('storage_test') }}
                </v-btn>
                <v-btn color="primary" :disabled="!testResult?.ok" :loading="savingStorage" @click="saveStorage">
                  {{ $t('save_settings') }}
                </v-btn>
                <span v-if="!testResult?.ok" class="text-caption text-grey">{{ $t('storage_must_test') }}</span>
              </div>

              <v-divider class="my-6" />
              <div class="text-body-2 font-weight-medium mb-1">{{ $t('storage_old_files_title') }}</div>
              <div class="text-body-2 text-grey mb-3">
                {{ $t('storage_old_files_desc') }}
                <template v-if="storageUsageParts"> {{ $t('storage_old_files_now', storageUsageParts) }}</template>
              </div>
              <v-btn
                variant="text"
                color="primary"
                size="small"
                prepend-icon="mdi-book-open-variant"
                href="https://tanviet12.github.io/chat-quality-agent/guide/s3-storage.html"
                target="_blank"
              >
                {{ $t('storage_guide') }}
              </v-btn>
            </div>
          </v-expand-transition>

          <div v-if="!dungS3 && storage.backend === 's3'" class="mt-4">
            <v-btn color="primary" :loading="savingStorage" @click="saveStorage">{{ $t('save_settings') }}</v-btn>
          </div>
        </v-card>

        <!-- General -->
        <v-card v-if="activeTab === 'general'" class="pa-6">
          <div class="text-subtitle-1 font-weight-bold mb-4">
            <v-icon start size="small">mdi-cog</v-icon>
            {{ $t('general') }}
          </div>

          <v-text-field v-model="generalSettings.companyName" :label="$t('company_name')" class="mb-3" />
          <v-select
            v-model="generalSettings.timezone"
            :label="$t('timezone')"
            :items="['Asia/Ho_Chi_Minh', 'Asia/Bangkok', 'UTC', 'America/New_York']"
            class="mb-3"
          />
          <v-select
            v-model="generalSettings.language"
            :label="$t('language')"
            :items="[{ title: 'Tiếng Việt', value: 'vi' }, { title: 'English', value: 'en' }]"
            class="mb-3"
          />

          <v-text-field
            v-model.number="generalSettings.exchangeRate"
            :label="$t('exchange_rate_vnd')"
            type="number"
            suffix="VND = 1 USD"
            class="mb-3"
          />

          <v-text-field
            v-model="generalSettings.appUrl"
            label="URL ứng dụng"
            placeholder="https://cqa.yourdomain.com"
            hint="Cấu hình URL để hệ thống gửi link chính xác qua Telegram và Email"
            persistent-hint
            :rules="appUrlRules"
            class="mb-3"
          />

          <v-btn color="primary" :loading="savingGeneral" @click="saveGeneral">{{ $t('save_settings') }}</v-btn>
        </v-card>
      </v-col>
    </v-row>

    <v-dialog v-model="confirmTatS3" max-width="560">
      <v-card>
        <v-card-title class="text-h6">{{ $t('storage_off_title') }}</v-card-title>
        <v-card-text>
          <v-alert type="warning" variant="tonal" density="comfortable" class="mb-4">
            {{ $t('storage_off_warn') }}
          </v-alert>
          <div class="text-body-2 mb-2">{{ $t('storage_off_keep') }}</div>
          <v-sheet color="grey-lighten-4" rounded class="pa-3 mb-2" style="overflow-x: auto">
            <code class="text-caption text-no-wrap">
              docker exec cqa-app /app/cqa-server migrate-files -down -apply
            </code>
          </v-sheet>
          <div class="text-body-2 text-grey">{{ $t('storage_off_keys') }}</div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="huyTatS3">{{ $t('cancel') }}</v-btn>
          <v-btn color="warning" variant="flat" @click="xacNhanTatS3">{{ $t('storage_off_confirm') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { aiSettingsPayload, validateAISettings, MASKED_AI_KEY } from '../lib/aiSettings'
import { useAIModelCatalog } from '../composables/aiModelCatalog'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const activeTab = ref('ai')
const showKey = ref(false)
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const savingAI = ref(false)
const testingKey = ref(false)
const aiTestResult = ref<{ ok: boolean; message: string } | null>(null)
const savingGeneral = ref(false)
const savingStorage = ref(false)
const testingStorage = ref(false)

// Nơi cất file đính kèm, cấu hình riêng cho từng công ty.
const storage = reactive({
  backend: 'local',
  local_bytes: 0,
  local_files: 0,
  local_partial: false,
  endpoint: '',
  bucket: '',
  region: '',
  access_key: '',
  secret_key: '',
  prefix: '',
  force_path_style: false,
  secret_key_da_luu: false,
})
const dungS3 = ref(false)
const confirmTatS3 = ref(false)
const testResult = ref<{ ok: boolean; message: string } | null>(null)

// Tắt S3 là ảnh đã lưu trên đó thôi hiển thị, nên hỏi lại trước khi cho tắt.
// Chỉ hỏi khi công ty đang thật sự chạy trên S3, còn bật lên rồi đổi ý ngay
// thì không có gì để mất.
function onToggleS3(val: boolean | null) {
  testResult.value = null
  if (val === false && storage.backend === 's3') {
    confirmTatS3.value = true
  }
}

function huyTatS3() {
  confirmTatS3.value = false
  dungS3.value = true
}

async function xacNhanTatS3() {
  confirmTatS3.value = false
  await saveStorage()
}
const storageHost = computed(() => storage.endpoint.replace(/^https?:\/\//, '').replace(/\/$/, ''))

const tabs = [
  { label: 'ai_config', value: 'ai', icon: 'mdi-robot' },
  { label: 'analysis_settings', value: 'analysis', icon: 'mdi-chart-bar' },
  { label: 'storage_settings', value: 'storage', icon: 'mdi-folder-multiple-image' },
  { label: 'general', value: 'general', icon: 'mdi-cog' },
]

// Danh sách đối chiếu ngày 2026-09-15. Model thế hệ cũ vẫn giữ lại để ai đang
// dùng không bị mất lựa chọn, nhưng ghi rõ là cũ.
const claudeModels = [
  { title: 'Claude Sonnet 5 (Khuyến nghị)', value: 'claude-sonnet-5' },
  { title: 'Claude Haiku 4.5 (Nhanh & rẻ)', value: 'claude-haiku-4-5' },
  { title: 'Claude Opus 5 (Mạnh nhất)', value: 'claude-opus-5' },
  { title: 'Claude Sonnet 4.6 (Thế hệ cũ)', value: 'claude-sonnet-4-6' },
  { title: 'Claude Opus 4.6 (Thế hệ cũ)', value: 'claude-opus-4-6' },
]
const providerOptions = [
  { title: 'Claude (Anthropic)', value: 'claude' },
  { title: 'Gemini (Google)', value: 'gemini' },
  { title: 'ChatGPT (OpenAI)', value: 'openai' },
  { title: 'Grok (xAI)', value: 'xai' },
  { title: 'Custom AI — OpenAI-compatible', value: 'custom' },
]
const openaiModels = [
  { title: 'GPT-5 (Khuyến nghị)', value: 'gpt-5' },
  { title: 'GPT-5 mini (Nhanh & rẻ)', value: 'gpt-5-mini' },
  { title: 'o3 (Suy luận sâu)', value: 'o3' },
]
const xaiModels = [
  { title: 'Grok 4 (Khuyến nghị)', value: 'grok-4' },
  { title: 'Grok 3 (Thế hệ cũ)', value: 'grok-3' },
]
const geminiModels = [
  { title: 'Gemini 3.8 Flash (Khuyến nghị)', value: 'gemini-3.8-flash' },
  { title: 'Gemini 3.1 Flash Lite (Nhanh & rẻ nhất)', value: 'gemini-3.1-flash-lite' },
  { title: 'Gemini 3.5 Flash Lite (Rẻ)', value: 'gemini-3.5-flash-lite' },
  { title: 'Gemini 3.5 Flash (Mạnh hơn)', value: 'gemini-3.5-flash' },
  { title: 'Gemini 2.5 Pro (Thế hệ cũ)', value: 'gemini-2.5-pro' },
  { title: 'Gemini 2.5 Flash (Thế hệ cũ)', value: 'gemini-2.5-flash' },
]

const useCustomBaseUrl = ref(false)
const hasSavedKey = ref(false)
const aiSettings = reactive({ provider: 'claude', model: 'claude-sonnet-5', apiKey: '', baseUrl: '', batchMode: true, batchSize: 5, timeoutSeconds: 120 })
watch(() => [aiSettings.provider, aiSettings.model, aiSettings.apiKey, aiSettings.baseUrl, aiSettings.timeoutSeconds, useCustomBaseUrl.value], () => {
  aiTestResult.value = null
})
const generalSettings = reactive({ companyName: '', timezone: 'Asia/Ho_Chi_Minh', language: 'vi', exchangeRate: 26000, appUrl: '' })

const appUrlRules = [
  (v: string) => !v || /^https?:\/\/.+/.test(v) || 'URL phải bắt đầu bằng http:// hoặc https://',
  (v: string) => !v || !v.endsWith('/') || 'URL không nên có dấu / ở cuối',
]

const baseUrlPlaceholder = computed(() => {
  switch (aiSettings.provider) {
    case 'custom': return 'https://may-chu-cua-ban/v1/chat/completions'
    case 'gemini': return 'https://generativelanguage.googleapis.com'
    case 'openai': return 'https://api.openai.com/v1'
    case 'xai': return 'https://api.x.ai/v1'
    default: return 'https://api.anthropic.com'
  }
})

function fallbackModels(provider: string) {
  switch (provider) {
    case 'custom': return []
    case 'gemini': return geminiModels
    case 'openai': return openaiModels
    case 'xai': return xaiModels
    default: return claudeModels
  }
}

function defaultModelFor(provider: string) {
  switch (provider) {
    case 'custom': return ''
    case 'gemini': return 'gemini-3.8-flash'
    case 'openai': return 'gpt-5'
    case 'xai': return 'grok-4'
    default: return 'claude-sonnet-5'
  }
}

// Danh sách lấy từ nhà cung cấp nếu có, không thì dùng danh sách kèm sẵn.
const { fetchedModels, modelsSource, loadCatalog } = useAIModelCatalog(() => {
  const payload = aiSettingsPayload(aiSettings, useCustomBaseUrl.value)
  return JSON.stringify([payload.provider, payload.base_url, payload.api_key, payload.clear_api_key])
})
const refreshingModels = ref(false)

const modelOptions = computed(() => {
  if (fetchedModels.value.length) {
    return fetchedModels.value.map(m => ({ title: m.title, value: m.id }))
  }
  const fallback = fallbackModels(aiSettings.provider)
  // Model đang dùng phải luôn có mặt, nếu không ô chọn sẽ hiện trống.
  if (aiSettings.model && !fallback.some(m => m.value === aiSettings.model)) {
    return [{ title: `${aiSettings.model} (đang dùng)`, value: aiSettings.model }, ...fallback]
  }
  return fallback
})

async function loadModelList() {
  try {
    await loadCatalog(async () => {
      const { data } = await api.get(`/tenants/${tenantId.value}/settings/ai/models`)
      return data
    })
  } catch {
    // Không lấy được thì im lặng dùng danh sách kèm sẵn
  }
}

async function refreshModelList() {
  const validation = validateAISettings(aiSettings, useCustomBaseUrl.value, hasSavedKey.value, false)
  if (validation) {
    showSnack(validation, 'error')
    return
  }
  const draft = aiSettingsPayload(aiSettings, useCustomBaseUrl.value)
  refreshingModels.value = true
  try {
    const data = await loadCatalog(async () => {
      const { data } = await api.post(`/tenants/${tenantId.value}/settings/ai/models/refresh`, draft)
      return data
    })
    if (!data) return
    showSnack(`Đã cập nhật ${fetchedModels.value.length} model`, 'success')
  } catch (err: any) {
    const res = err.response?.data
    showSnack(res?.message || res?.error || t('error'), 'error')
  } finally {
    refreshingModels.value = false
  }
}

function onProviderChange() {
  // Reset to default model when switching provider
  aiSettings.model = defaultModelFor(aiSettings.provider)
  aiSettings.apiKey = ''
  aiSettings.baseUrl = ''
  aiSettings.timeoutSeconds = 120
  hasSavedKey.value = false
  useCustomBaseUrl.value = aiSettings.provider === 'custom'
  // Danh sách model của nhà cung cấp cũ không còn đúng nữa
  fetchedModels.value = []
  modelsSource.value = ''
}

function onEndpointChange() {
  // Never reuse a saved credential against a newly entered host.
  if (aiSettings.apiKey === MASKED_AI_KEY) aiSettings.apiKey = ''
  hasSavedKey.value = false
  fetchedModels.value = []
  modelsSource.value = ''
}

async function loadSettings() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    if (data.settings.ai_provider) aiSettings.provider = data.settings.ai_provider
    if (data.settings.ai_model) aiSettings.model = data.settings.ai_model
    aiSettings.apiKey = data.settings.ai_api_key || ''
    hasSavedKey.value = !!data.settings.ai_api_key
    aiSettings.baseUrl = data.settings.ai_base_url || ''
    aiSettings.timeoutSeconds = data.settings.ai_timeout_seconds ? Number(data.settings.ai_timeout_seconds) : 120
    useCustomBaseUrl.value = !!data.settings.ai_base_url || aiSettings.provider === 'custom'
    if (data.settings.ai_batch_mode) aiSettings.batchMode = data.settings.ai_batch_mode === 'true'
    if (data.settings.ai_batch_size) aiSettings.batchSize = parseInt(data.settings.ai_batch_size) || 5
    if (data.settings.exchange_rate_vnd) generalSettings.exchangeRate = parseFloat(data.settings.exchange_rate_vnd) || 26000
    if (data.settings.app_url) generalSettings.appUrl = data.settings.app_url
    if (data.tenant) {
      generalSettings.companyName = data.tenant.name || ''
      generalSettings.timezone = data.tenant.timezone || 'Asia/Ho_Chi_Minh'
      generalSettings.language = data.tenant.language || 'vi'
    }
  } catch {
    // Settings not yet saved
  }
}

const savingAnalysis = ref(false)

async function saveAnalysis() {
  savingAnalysis.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/analysis`, {
      batch_mode: aiSettings.batchMode ? 'true' : 'false',
      batch_size: String(aiSettings.batchSize),
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingAnalysis.value = false
  }
}

async function saveAI() {
  const validation = validateAISettings(aiSettings, useCustomBaseUrl.value, hasSavedKey.value)
  if (validation) {
    showSnack(validation, 'error')
    return
  }
  savingAI.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/ai`, aiSettingsPayload(aiSettings, useCustomBaseUrl.value))
    await loadSettings()
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.message || err.response?.data?.error || t('error'), 'error')
  } finally {
    savingAI.value = false
  }
}

async function testKey() {
  const validation = validateAISettings(aiSettings, useCustomBaseUrl.value, hasSavedKey.value)
  if (validation) {
    aiTestResult.value = { ok: false, message: validation }
    return
  }
  const draft = aiSettingsPayload(aiSettings, useCustomBaseUrl.value)
  testingKey.value = true
  aiTestResult.value = null
  try {
    const timeout = (aiSettings.provider === 'custom' ? aiSettings.timeoutSeconds : 30) * 1000 + 10000
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/ai/test`, draft, { timeout })
    if (JSON.stringify(draft) !== JSON.stringify(aiSettingsPayload(aiSettings, useCustomBaseUrl.value))) return
    const detail = data.model ? `${data.provider} / ${data.model}` : data.provider
    aiTestResult.value = { ok: true, message: `${detail}: ${data.message}` }
    showSnack(`${detail}: ${data.message}`, 'success')
  } catch (err: any) {
    const res = err.response?.data
    if (JSON.stringify(draft) !== JSON.stringify(aiSettingsPayload(aiSettings, useCustomBaseUrl.value))) return
    aiTestResult.value = { ok: false, message: res?.message || res?.error || t('error') }
    showSnack(res?.message || res?.error || t('error'), 'error')
  } finally {
    testingKey.value = false
  }
}

// Đổi số byte thành chuỗi người đọc được. Dùng bội số 1024 cho khớp với cách hệ
// điều hành báo dung lượng ổ đĩa.
function dinhDangDungLuong(bytes: number): string {
  if (!bytes || bytes < 0) return '0 MB'
  const don = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = bytes
  while (v >= 1024 && i < don.length - 1) {
    v /= 1024
    i++
  }
  // Dưới 10 thì giữ một chữ số thập phân cho đỡ mất thông tin (1,4 GB).
  const soChu = v < 10 && i > 0 ? 1 : 0
  return `${v.toFixed(soChu).replace('.', ',')} ${don[i]}`
}

// Hai cách diễn đạt cùng một con số: câu đầy đủ cho dòng trạng thái, và cụm
// ngắn để nhét vào giữa câu khác mà không lặp chữ.
const storageUsageParts = computed(() => {
  if (!storage.local_files) return null
  return {
    size: dinhDangDungLuong(storage.local_bytes),
    count: storage.local_files.toLocaleString('vi-VN'),
  }
})

const storageLocalUsage = computed(() => {
  const p = storageUsageParts.value
  if (!p) return ''
  const text = t('storage_local_usage', p)
  return storage.local_partial ? `${text} ${t('storage_usage_partial')}` : text
})

async function loadStorage() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings/storage`)
    Object.assign(storage, data, { secret_key: '' })
    dungS3.value = data.backend === 's3'
    testResult.value = null
  } catch { /* giữ mặc định lưu trên máy chủ */ }
}

function storagePayload() {
  return {
    backend: dungS3.value ? 's3' : 'local',
    endpoint: storage.endpoint,
    bucket: storage.bucket,
    region: storage.region,
    access_key: storage.access_key,
    secret_key: storage.secret_key,
    prefix: storage.prefix,
    force_path_style: storage.force_path_style,
  }
}

async function testStorage() {
  testingStorage.value = true
  testResult.value = null
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/storage/test`, storagePayload())
    testResult.value = { ok: data.ok === true, message: data.message || '' }
  } catch (e: any) {
    testResult.value = { ok: false, message: e.response?.data?.message || t('connection_failed') }
  } finally {
    testingStorage.value = false
  }
}

async function saveStorage() {
  savingStorage.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/storage`, storagePayload())
    await loadStorage()
    showSnack(t('settings_saved'), 'success')
  } catch (e: any) {
    showSnack(e.response?.data?.message || t('save_failed'), 'error')
  } finally {
    savingStorage.value = false
  }
}

async function saveGeneral() {
  savingGeneral.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/general`, {
      company_name: generalSettings.companyName,
      timezone: generalSettings.timezone,
      language: generalSettings.language,
      exchange_rate_vnd: generalSettings.exchangeRate,
      app_url: generalSettings.appUrl,
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingGeneral.value = false
  }
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snackbar.value = true
}

onMounted(async () => {
  await loadSettings()
  // Nạp sau khi đã biết nhà cung cấp và model đang chọn
  loadModelList()
  loadStorage()
})
</script>

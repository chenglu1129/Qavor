<script setup>
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { message, Modal } from 'ant-design-vue'
import { ChevronDown, Edit3, Plus, RefreshCw, RotateCcw, Trash2 } from 'lucide-vue-next'

import PageShoulder from '@/components/shared/PageShoulder.vue'
import { modelApi } from '@/apis/model_api'
import {
  buildModelPayload,
  buildModelTestPayload,
  createDefaultModelForm,
  formatModelTestError,
  formatModelTestSuccess,
  isModelConnectionTestSupported,
  modelToForm,
  MODEL_PROTOCOL_OPTIONS,
  resetAdvancedFields
} from '@/utils/modelConfig'

const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const testResult = ref(null)
const models = ref([])
const total = ref(0)
// statsModels 缓存全量模型（仅用于顶部统计），避免分页后按类型计数只统计当前页
const statsModels = ref([])
const keyword = ref('')
const modelType = ref('')
const page = ref(1)
const pageSize = ref(10)
const showModal = ref(false)
const editingId = ref(null)
const advancedOpen = ref(false)
const form = reactive(createDefaultModelForm())
// suppressTestResultReset 用于在 resetForm 期间暂停 watch，避免重复清空。
const suppressTestResultReset = ref(false)
// 供应商选择与远程模型列表
const providers = ref([])
const selectedProvider = ref(null)
const selectedProviderName = ref(null)
const remoteModels = ref([])
// 最近一次成功拉取的时间戳（0 表示还没拉过），用于在提示行里标注候选的新鲜度
const remoteModelsAt = ref(0)
// 当前列表对应的缓存键，避免同一次输入里反复回填
const loadedCacheKey = ref('')
const fetchingModels = ref(false)
// 「模型名称」本身是 auto-complete：拉取成功后自动聚焦并展开候选，也可直接手填
const modelNameSelect = ref(null)
// 显式控制候选面板开合：AutoComplete 默认只在输入时展开，拉取成功后需要主动弹出来
const modelNameOpen = ref(false)

const typeOptions = [
  { label: '全部类型', value: '' },
  { label: 'Chat', value: 'chat' },
  { label: 'Embedding', value: 'embedding' },
  { label: 'Rerank', value: 'rerank' }
]

const stats = computed(() => ({
  total: total.value,
  enabled: statsModels.value.filter((model) => model.enabled).length,
  chat: statsModels.value.filter((model) => model.model_type === 'chat').length,
  embedding: statsModels.value.filter((model) => model.model_type === 'embedding').length,
  rerank: statsModels.value.filter((model) => model.model_type === 'rerank').length
}))

// 自定义供应商标识：供应商为必填项，用户手动填写时选择「自定义」
const CUSTOM_PROVIDER = 'custom'

const providerOptions = computed(() => [
  { label: '自定义配置', value: CUSTOM_PROVIDER },
  ...providers.value.map((p) => ({ label: p.displayName, value: p.name }))
])

// ---- 模型候选：供应商预设（离线即时）+ 接口拉取（最新） ----
const toModelOption = (name) => ({ label: name, value: name })

// 供应商模板自带常用模型：选中供应商即出候选，不用先点按钮、也不用先填 API Key
const presetModels = computed(() => (selectedProvider.value?.models || []).filter(Boolean))

// 拉取结果优先于预设（更新鲜），同名只出现一次
const modelOptions = computed(() => {
  const fetched = remoteModels.value
  const fetchedSet = new Set(fetched)
  const presets = presetModels.value.filter((name) => !fetchedSet.has(name))
  const groups = []
  if (fetched.length) {
    groups.push({ label: `已从服务商获取（${fetched.length}）`, options: fetched.map(toModelOption) })
  }
  if (presets.length) {
    groups.push({ label: `供应商预设（${presets.length}）`, options: presets.map(toModelOption) })
  }
  return groups
})

const modelOptionCount = computed(() => remoteModels.value.length + presetModels.value.length)

// 只有用户真的在键盘输入时才过滤：否则输入框里已有的值（例如预设自动填入的 deepseek-chat）
// 会把其余候选全部挡掉，用户点开只看到一个选项，以为列表没拉到
const modelNameTyping = ref(false)

// 空输入时展示全部候选（聚焦即出列表）；有输入时按子串过滤，同时仍允许手动填任意模型名
const filterModelOption = (inputValue, option) =>
  !modelNameTyping.value ||
  !inputValue ||
  String(option.value || '').toLowerCase().includes(String(inputValue).toLowerCase())

const onModelNameSearch = () => {
  modelNameTyping.value = true
}

// 聚焦时才展开候选面板（没有候选就保持收起，避免出现空面板）；失焦 / 选中后收起
const openModelNameOptions = () => {
  modelNameTyping.value = false
  if (modelOptionCount.value) modelNameOpen.value = true
}
const closeModelNameOptions = () => {
  modelNameOpen.value = false
}

// 拉取成功的时间，用于提示行展示新鲜度
const remoteModelsTimeText = computed(() => {
  if (!remoteModelsAt.value) return ''
  const d = new Date(remoteModelsAt.value)
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
})

const modelNameHint = computed(() => {
  if (form.model_type === 'rerank') return 'Rerank 模型没有 /models 接口，请按服务商文档手动填写'
  if (remoteModels.value.length) {
    const at = remoteModelsTimeText.value
    return `已从服务商获取 ${remoteModels.value.length} 个模型${at ? `（${at}）` : ''}，可随时点右侧按钮刷新`
  }
  if (presetModels.value.length) {
    return `内置 ${presetModels.value.length} 个${selectedProvider.value?.displayName || ''}预设模型，可直接下拉选；点右侧按钮可拉取该账号实际可用的列表`
  }
  if (editingId.value && !form.api_key.trim()) return '编辑模式：API Key 可留空，拉取列表会沿用已保存的密钥'
  return '可直接手动输入模型名称，或先选供应商获得预设候选'
})

const canFetchModels = computed(() => {
  if (form.model_type === 'rerank') return false
  if (!form.base_url.trim()) return false
  if (form.protocol === 'ollama') return true
  return Boolean(form.api_key.trim()) || Boolean(editingId.value)
})

const fetchTooltip = computed(() => {
  if (form.model_type === 'rerank') return 'Rerank 协议没有列表接口'
  if (!form.base_url.trim()) return '请先填写 Base URL'
  if (!canFetchModels.value) return '请先填写 API Key'
  return '从服务商拉取最新模型列表'
})

// ---- 拉取结果本地缓存：关掉弹窗再打开，候选还在（按 协议 + Base URL 区分） ----
const MODEL_CACHE_PREFIX = 'qavor:remote-models:'
const modelCacheKey = () => {
  const base = form.base_url.trim()
  if (!base) return ''
  return `${MODEL_CACHE_PREFIX}${form.protocol || 'openai'}|${base}`
}

const restoreModelCache = () => {
  const key = modelCacheKey()
  remoteModels.value = []
  remoteModelsAt.value = 0
  if (!key) return
  loadedCacheKey.value = key
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return
    const cached = JSON.parse(raw)
    const items = Array.isArray(cached?.items) ? cached.items.filter((m) => typeof m === 'string' && m) : []
    if (!items.length) return
    remoteModels.value = items
    remoteModelsAt.value = Number(cached?.ts) || 0
  } catch {
    // 缓存损坏时忽略，退化成只有供应商预设
  }
}

// 地址/协议变化后自动换用对应缓存（用户改完地址不必等失焦就能看到上次拉到的列表）
let cacheTimer = null
watch(
  () => [form.protocol, form.base_url],
  () => {
    if (cacheTimer) clearTimeout(cacheTimer)
    cacheTimer = setTimeout(() => {
      const key = modelCacheKey()
      if (!key || key === loadedCacheKey.value) return
      restoreModelCache()
    }, 400)
  }
)

const saveModelCache = (items) => {
  const key = modelCacheKey()
  if (!key) return
  loadedCacheKey.value = key
  try {
    window.localStorage.setItem(key, JSON.stringify({ items, ts: Date.now() }))
  } catch {
    // 隐私模式下 localStorage 可能不可写，忽略即可
  }
}

// 拉取全量模型用于顶部类型统计（不受分页影响）
const loadStats = async () => {
  try {
    const response = await modelApi.list({ page: 1, page_size: 100 })
    const data = response?.data || { total: 0, items: [] }
    statsModels.value = data.items || []
  } catch {
    // 统计失败不影响列表展示
  }
}

const testResultDescription = computed(() => {
  if (!testResult.value) return ''
  if (!testResult.value.success) {
    const { message, detail } = testResult.value
    // detail 为脱敏后的原始错误，换行展示便于排查
    return detail ? `${message}\n${detail}` : message
  }
  let desc = testResult.value.message
  if (form.model_type === 'embedding' && testResult.value.dimension) {
    desc += ` · 向量维度：${testResult.value.dimension}`
  }
  return desc
})

const resetForm = (source = null) => {
  suppressTestResultReset.value = true
  Object.assign(form, source ? modelToForm(source) : createDefaultModelForm())
  advancedOpen.value = false
  testResult.value = null
  // 下一轮事件循环恢复 watch 监听，确保 Object.assign 触发的回调被跳过。
  Promise.resolve().then(() => {
    suppressTestResultReset.value = false
  })
}

const resetAdvanced = () => {
  Object.assign(form, resetAdvancedFields())
}

// 监听连接相关字段变化，编辑后清空已有测试结果，避免结果与表单不一致。
watch(
  () => [form.name, form.protocol, form.base_url, form.api_key, form.timeout, form.model_type],
  () => {
    if (suppressTestResultReset.value) return
    if (testResult.value) testResult.value = null
  }
)

// 切换到 Rerank 时隐藏远程模型列表（该类型没有 /models 接口）
watch(
  () => form.model_type,
  () => {
    if (form.model_type === 'rerank') {
      remoteModels.value = []
      remoteModelsAt.value = 0
      modelNameOpen.value = false
    }
  }
)

const loadModels = async () => {
  loading.value = true
  try {
    const response = await modelApi.list({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value,
      model_type: modelType.value
    })
    const data = response?.data || { total: 0, items: [] }
    models.value = data.items || []
    total.value = data.total || 0
  } catch (error) {
    message.error(error.message || '加载模型配置失败')
  } finally {
    loading.value = false
  }
}

const searchModels = () => {
  page.value = 1
  loadModels()
}

const openCreate = () => {
  editingId.value = null
  resetForm()
  resetProviderState()
  showModal.value = true
}

const openEdit = (model) => {
  editingId.value = model.id
  resetForm(model)
  resetProviderState()
  // 已有配置若来自内置供应商，编辑时自动回选该供应商：预设模型、协议提示都能立刻恢复（不改动已保存的地址与协议）
  const matched = matchProviderByBaseURL(model.base_url)
  if (matched) {
    selectedProviderName.value = matched.name
    selectedProvider.value = matched
  }
  restoreModelCache()
  showModal.value = true
}

const normalizeBaseURL = (url) => String(url || '').trim().replace(/\/+$/, '').toLowerCase()

// 按地址反查供应商模板（忽略结尾斜杠与大小写）
const matchProviderByBaseURL = (baseURL) => {
  const target = normalizeBaseURL(baseURL)
  if (!target) return null
  return providers.value.find((p) => normalizeBaseURL(p.baseURL) === target) || null
}

// 打开弹窗时重置供应商选择与远程模型列表（默认选中「自定义」，编辑已有配置不预选内置供应商）
const resetProviderState = () => {
  selectedProvider.value = null
  selectedProviderName.value = CUSTOM_PROVIDER
  remoteModels.value = []
  remoteModelsAt.value = 0
  loadedCacheKey.value = ''
  modelNameOpen.value = false
}

// 拉取内置供应商列表（后端 llm.ProviderRegistry）
const loadProviders = async () => {
  try {
    const response = await modelApi.getProviders()
    providers.value = Array.isArray(response?.data) ? response.data : []
  } catch {
    // 供应商列表加载失败不阻塞表单（仍可手动填写）
  }
}

// 选中供应商 → 自动填充 Base URL 与协议、立刻带出该供应商的预设模型（离线，零等待）；
// 选「自定义」时不自动填充，回到手动填写
const onProviderChange = (name) => {
  const provider = name && name !== CUSTOM_PROVIDER
    ? providers.value.find((p) => p.name === name)
    : null
  selectedProvider.value = provider || null
  modelNameOpen.value = false
  if (provider) {
    form.base_url = provider.baseURL
    form.protocol = provider.protocol
    // 换供应商时，旧供应商的拉取结果不再适用，改用新地址的缓存
    restoreModelCache()
    // 模型名称为空时给个起点，用户可以自己改成列表里的其他模型
    if (!form.name.trim() && provider.models?.length) {
      form.name = provider.models[0]
    }
  } else {
    remoteModels.value = []
    remoteModelsAt.value = 0
  }
}

// 切换协议会影响拉取端点（openai /models vs ollama /api/tags），旧候选作废后重新读缓存
const onProtocolChange = () => {
  modelNameOpen.value = false
  restoreModelCache()
}

// 远程拉取模型列表（后端代理 {base_url}/models 或 Ollama /api/tags）
const fetchRemoteModels = async () => {
  if (!form.base_url.trim()) {
    message.warning('请先填写 Base URL')
    return
  }
  if (form.protocol !== 'ollama' && !form.api_key.trim() && !editingId.value) {
    message.warning('请先填写 API Key，再拉取模型列表')
    return
  }
  fetchingModels.value = true
  try {
    const response = await modelApi.fetchRemoteModels({
      base_url: form.base_url.trim(),
      api_key: form.api_key,
      protocol: form.protocol,
      // 编辑已有配置时 Key 不回显：带上 model_id，让后端沿用已保存的密钥
      model_id: editingId.value || undefined
    })
    const items = normalizeRemoteModels(response?.data)
    if (!items.length) {
      message.info('未获取到模型列表，请确认该服务已部署模型')
      return
    }
    remoteModels.value = items
    remoteModelsAt.value = Date.now()
    saveModelCache(items)
    // 只有一个模型时没有可选性，直接填好省一次点击
    if (items.length === 1 && !form.name.trim()) {
      form.name = items[0]
      message.success(`已获取 1 个模型：${items[0]}，已自动填入`)
      return
    }
    message.success(`已获取 ${items.length} 个模型，点「模型名称」即可挑选`)
    await nextTick()
    modelNameOpen.value = true
    modelNameSelect.value?.focus?.()
  } catch (error) {
    message.error(error.message || '获取模型列表失败')
  } finally {
    fetchingModels.value = false
  }
}

// 后端正常返回 string[]；这里兼容 { models: [] } / { data: [] } 等异形响应，避免静默失败
const normalizeRemoteModels = (data) => {
  const list = Array.isArray(data)
    ? data
    : Array.isArray(data?.models)
      ? data.models
      : Array.isArray(data?.data)
        ? data.data
        : []
  return list
    .map((item) => (typeof item === 'string' ? item : item?.id || item?.name || ''))
    .filter(Boolean)
}

// 模型名称输入框用 auto-complete：候选来自远程列表，也允许直接手填

const save = async () => {
  if (!selectedProviderName.value) {
    message.warning('请选择供应商')
    return
  }
  if (!form.name.trim() || !form.protocol.trim() || !form.base_url.trim()) {
    message.warning('请填写模型名称、协议和 Base URL')
    return
  }

  let payload
  try {
    payload = buildModelPayload(form)
  } catch (error) {
    message.error(error.message)
    return
  }

  saving.value = true
  try {
    if (editingId.value) await modelApi.update(editingId.value, payload)
    else await modelApi.create(payload)
    message.success(editingId.value ? '模型配置已保存' : '模型配置已创建')
    showModal.value = false
    await loadModels()
    loadStats()
  } catch (error) {
    message.error(error.message || '保存模型配置失败')
  } finally {
    saving.value = false
  }
}

const testConnection = async () => {
  if (!isModelConnectionTestSupported(form.model_type)) {
    message.warning('暂不支持该模型类型的连接测试')
    return
  }
  if (!form.name.trim() || !form.protocol.trim() || !form.base_url.trim()) {
    message.warning('请填写模型名称、协议和 Base URL')
    return
  }
  if (!editingId.value && !form.api_key.trim()) {
    message.warning('请填写 API Key')
    return
  }

  let payload
  try {
    payload = buildModelTestPayload(form, editingId.value || null)
  } catch (error) {
    message.error(error.message)
    return
  }

  testing.value = true
  try {
    const response = await modelApi.testConnection(payload)
    if (response?.code !== 0) {
      testResult.value = {
        success: false,
        message: response?.message || '连接测试失败',
        detail: response?.detail || ''
      }
      return
    }
    const data = response?.data || {}
    testResult.value = {
      success: true,
      message: formatModelTestSuccess(data),
      dimension: data.dimension || 0
    }
  } catch (error) {
    const { message: friendly, detail } = formatModelTestError(error)
    testResult.value = {
      success: false,
      message: friendly,
      detail
    }
  } finally {
    testing.value = false
  }
}

const remove = (model) => {
  Modal.confirm({
    title: `删除模型配置“${model.name}”？`,
    content: '删除后将无法通过该模型配置发起请求。',
    okText: '删除',
    okType: 'danger',
    cancelText: '取消',
    async onOk() {
      try {
        await modelApi.remove(model.id)
        message.success('模型配置已删除')
        if (models.value.length === 1 && page.value > 1) page.value -= 1
        await loadModels()
        loadStats()
      } catch (error) {
        message.error(error.message || '删除模型配置失败')
      }
    }
  })
}

const formatDate = (value) => (value ? new Date(value).toLocaleString() : '-')

defineExpose({ loading, stats, loadModels })
onMounted(() => {
  loadModels()
  loadStats()
  loadProviders()
})
</script>

<template>
  <div class="model-manage-panel">
    <PageShoulder
      v-model:search="keyword"
      search-placeholder="搜索模型名称..."
      @update:search="searchModels"
    >
      <template #actions>
        <a-select
          v-model:value="modelType"
          :options="typeOptions"
          style="width: 140px"
          @change="searchModels"
        />
        <a-button type="primary" @click="openCreate">
          <Plus :size="15" />
          新增模型
        </a-button>
      </template>
    </PageShoulder>

    <div class="model-toolbar">
      <span>共 {{ total }} 个模型配置</span>
      <span>当前页启用 {{ stats.enabled }} 个</span>
    </div>

    <a-spin :spinning="loading">
      <div v-if="!loading && !models.length" class="empty-state">暂无模型配置</div>
      <div v-else class="model-list">
        <div v-for="model in models" :key="model.id" class="model-row">
          <div class="model-main">
            <div class="model-remark">{{ model.remark || '未填写备注' }}</div>
            <div class="model-url">{{ model.base_url }}</div>
          </div>
          <a-tag :color="model.model_type === 'chat' ? 'blue' : model.model_type === 'embedding' ? 'green' : 'orange'">
            {{ model.model_type === 'rerank' ? 'Rerank' : model.model_type }}
          </a-tag>
          <a-tag :color="model.enabled ? 'success' : 'default'">
            {{ model.enabled ? '已启用' : '已禁用' }}
          </a-tag>
          <span class="model-updated">{{ formatDate(model.updated_at) }}</span>
          <div class="model-actions">
            <a-button type="text" title="编辑" @click="openEdit(model)">
              <Edit3 :size="15" />
            </a-button>
            <a-button type="text" danger title="删除" @click="remove(model)">
              <Trash2 :size="15" />
            </a-button>
          </div>
        </div>
      </div>
    </a-spin>

    <a-pagination
      v-if="total"
      v-model:current="page"
      v-model:page-size="pageSize"
      :total="total"
      :show-size-changer="true"
      class="model-pagination"
      @change="loadModels"
      @show-size-change="loadModels"
    />

    <a-modal
      v-model:open="showModal"
      :title="editingId ? '编辑模型配置' : '新增模型配置'"
      :width="680"
    >
      <div class="form-grid">
        <!-- 顺序跟着依赖走：先「连哪家服务 + 地址 + 密钥」，最后才是「用哪个模型」 -->
        <label>
          供应商 *
          <a-select
            v-model:value="selectedProviderName"
            :options="providerOptions"
            placeholder="选择供应商模板，选「自定义」可手动填写"
            @change="onProviderChange"
          />
        </label>
        <label v-if="form.model_type !== 'rerank'">
          协议 *
          <a-select
            v-model:value="form.protocol"
            :options="MODEL_PROTOCOL_OPTIONS"
            @change="onProtocolChange"
          />
        </label>
        <label v-else>
          协议
          <div class="rerank-protocol">
            <a-tag color="orange">Rerank 固定协议</a-tag>
            <span class="rerank-protocol-hint">Cohere / Jina 风格 HTTP 重排，自动请求 {base_url}/v1/rerank，无需选择</span>
          </div>
        </label>
        <div v-if="selectedProvider" class="provider-hint">
          {{ selectedProvider.displayName }} · 已自动填充官方请求地址{{
            selectedProvider.models?.length ? ` 与 ${selectedProvider.models.length} 个常用模型` : ''
          }}，{{
            selectedProvider.protocol === 'ollama' ? '本地服务无需 API Key，可直接拉取列表' : '填好 API Key 后可拉取该账号的完整列表'
          }}
        </div>
        <label class="full-width">
          Base URL *
          <a-input
            v-model:value="form.base_url"
            :placeholder="form.model_type === 'rerank'
              ? 'https://api.jina.ai/v1(自动追加 /v1/rerank)或填完整 /rerank 端点'
              : 'https://api.openai.com/v1'"
            @blur="restoreModelCache"
          />
        </label>
        <label>
          API Key
          <a-input-password
            v-model:value="form.api_key"
            :placeholder="editingId ? '留空表示沿用已保存密钥' : '填写后即可拉取模型列表'"
          />
        </label>
        <label>
          超时（毫秒）
          <a-input-number v-model:value="form.timeout" :min="1000" :max="300000" style="width: 100%" />
        </label>
        <label class="full-width">
          模型名称 *
          <span class="name-with-fetch">
            <a-auto-complete
              ref="modelNameSelect"
              v-model:value="form.name"
              :options="modelOptions"
              :filter-option="filterModelOption"
              :open="modelNameOpen"
              placeholder="可直接输入，或从下拉里选"
              allow-clear
              @focus="openModelNameOptions"
              @search="onModelNameSearch"
              @blur="closeModelNameOptions"
              @select="closeModelNameOptions"
            >
              <template #notFoundContent>
                <span class="model-name-empty">没有匹配的模型，可直接手动输入</span>
              </template>
            </a-auto-complete>
            <a-tooltip v-if="form.model_type !== 'rerank'" :title="fetchTooltip">
              <a-button
                class="fetch-models-btn"
                :loading="fetchingModels"
                :disabled="!canFetchModels"
                @click="fetchRemoteModels"
              >
                <RefreshCw v-if="!fetchingModels" :size="14" />
              </a-button>
            </a-tooltip>
          </span>
          <span class="model-name-hint">{{ modelNameHint }}</span>
        </label>
        <label>
          备注
          <a-input v-model:value="form.remark" placeholder="便于识别此模型配置" />
        </label>
        <label>
          模型类型
          <a-select
            v-model:value="form.model_type"
            :options="typeOptions.slice(1)"
            style="width: 100%"
          />
        </label>
        <div class="enabled-field">
          <span>启用模型</span>
          <a-switch v-model:checked="form.enabled" size="small" />
        </div>
        <div class="advanced-section full-width">
          <button class="advanced-toggle" type="button" @click="advancedOpen = !advancedOpen">
            <span class="advanced-title">
              <ChevronDown :size="15" :class="{ rotated: advancedOpen }" />
              高级选项
            </span>
            <span class="advanced-hint">{{ form.model_type === 'rerank' ? '请求头配置' : '请求头与推理参数' }}</span>
          </button>
          <div v-if="advancedOpen" class="advanced-content">
            <div class="advanced-toolbar">
              <span>高级配置</span>
              <a-button type="link" size="small" @click="resetAdvanced">
                <RotateCcw :size="13" />
                重置
              </a-button>
            </div>
            <div class="advanced-heading">
              <span>请求头 JSON</span>
            </div>
            <a-textarea v-model:value="form.headers" :rows="4" />
            <template v-if="form.model_type !== 'rerank'">
              <div class="advanced-heading">
                <span>默认推理参数 JSON</span>
              </div>
              <a-textarea v-model:value="form.params" :rows="8" />
            </template>
          </div>
        </div>
      </div>

      <a-alert
        v-if="testResult"
        class="model-test-result"
        :type="testResult.success ? 'success' : 'error'"
        :message="testResult.success ? '连接测试成功' : '连接测试失败'"
        show-icon
      >
        <template #description>
          <span class="model-test-result-desc">{{ testResultDescription }}</span>
          <div v-if="!testResult.success && testResult.detail" class="model-test-result-detail">
            {{ testResult.detail }}
          </div>
        </template>
      </a-alert>

      <template #footer>
        <a-button @click="showModal = false">取消</a-button>
        <a-button
          :loading="testing"
          :disabled="!isModelConnectionTestSupported(form.model_type)"
          @click="testConnection"
        >
          测试连接
        </a-button>
        <a-button type="primary" :loading="saving" @click="save">保存</a-button>
      </template>
    </a-modal>
  </div>
</template>

<style lang="less" scoped>
.model-manage-panel {
  min-height: 100%;
  padding: 20px 24px 28px;
}

.model-toolbar {
  display: flex;
  gap: 16px;
  margin: 14px 0 10px;
  color: var(--gray-600);
  font-size: 12px;
}

.model-list {
  overflow: hidden;
  border: 1px solid var(--gray-150);
  border-radius: 8px;
  background: var(--gray-0);
}

.model-row {
  display: flex;
  align-items: center;
  gap: 14px;
  min-height: 68px;
  padding: 12px 16px;
  border-top: 1px solid var(--gray-100);

  &:first-child { border-top: 0; }
}

.model-main { flex: 1; min-width: 0; }
.model-remark { color: var(--gray-900); font-weight: 600; }
.model-url, .model-updated { color: var(--gray-500); font-size: 12px; }
.model-url { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.model-updated { width: 150px; }
.model-actions { display: flex; gap: 2px; }
.model-pagination { margin-top: 16px; text-align: right; }
.empty-state { padding: 64px 0; color: var(--gray-500); text-align: center; }

.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;

  label {
    display: flex;
    flex-direction: column;
    gap: 6px;
    color: var(--gray-700);
    font-size: 12px;
    font-weight: 500;
  }

  .full-width { grid-column: 1 / -1; }
}

.provider-hint {
  grid-column: 1 / -1;
  margin-top: -8px;
  padding: 6px 10px;
  border-radius: 6px;
  background: #f6ffed;
  border: 1px solid #b7eb8f;
  color: var(--gray-700);
  font-size: 12px;
  line-height: 1.5;
}

.name-with-fetch {
  display: flex;
  gap: 8px;
  align-items: center;

  .ant-input,
  .ant-select { flex: 1; min-width: 0; }
}

.fetch-models-btn {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
}

.model-name-hint {
  color: var(--gray-500);
  font-size: 11px;
  line-height: 1.5;
}

.model-name-empty {
  color: var(--gray-500);
  font-size: 12px;
}

.enabled-field {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 10px;
  min-height: 32px;
  color: var(--gray-700);
  font-size: 12px;
  font-weight: 500;
}

.rerank-protocol {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  min-height: 32px;
  justify-content: center;
}

.rerank-protocol-hint {
  color: var(--gray-500);
  font-size: 11px;
  line-height: 1.5;
}

.advanced-section {
  border-top: 1px solid var(--gray-100);
  padding-top: 4px;
}

.advanced-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 8px 0;
  border: 0;
  background: transparent;
  color: var(--gray-700);
  cursor: pointer;
  text-align: left;
}

.advanced-title,
.advanced-heading,
.advanced-hint {
  display: flex;
  align-items: center;
}

.advanced-title { gap: 6px; font-weight: 600; }
.advanced-title svg { transition: transform 0.2s; }
.advanced-title svg.rotated { transform: rotate(180deg); }
.advanced-hint { color: var(--gray-500); font-size: 11px; }
.advanced-content { display: flex; flex-direction: column; gap: 8px; padding: 4px 0 8px; }
.advanced-toolbar { display: flex; align-items: center; justify-content: space-between; color: var(--gray-700); font-size: 12px; font-weight: 600; }
.advanced-heading { color: var(--gray-700); font-size: 12px; }
.advanced-heading .ant-btn { display: inline-flex; align-items: center; gap: 4px; padding-inline: 4px; }

.model-test-hint {
  margin-top: 12px;
  color: var(--gray-500);
  font-size: 12px;
}

.model-test-result {
  margin-top: 12px;
}

.model-test-result-desc {
  word-break: break-word;
  overflow-wrap: anywhere;
  white-space: pre-line;
}

.model-test-result-detail {
  margin-top: 6px;
  color: var(--gray-500);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
  overflow-wrap: anywhere;
}

.model-test-result-detail {
  margin-top: 6px;
  color: var(--gray-500);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
  overflow-wrap: anywhere;
}

.model-test-result-detail {
  margin-top: 6px;
  color: var(--gray-500);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
  overflow-wrap: anywhere;
}

@media (max-width: 760px) {
  .model-manage-panel { padding: 14px; }
  .model-row { align-items: flex-start; flex-wrap: wrap; }
  .model-updated { width: auto; }
  .form-grid { grid-template-columns: 1fr; }
  .form-grid .full-width { grid-column: auto; }
  .ant-modal-footer {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 8px;
  }
}
</style>

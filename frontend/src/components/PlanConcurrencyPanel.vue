<template>
  <section class="concurrency-panel" aria-label="账号并发">
    <header class="concurrency-heading">
      <div><h4>最近 24 小时并发</h4><p>各成员平均并发与账号总峰值 · 每分钟汇总</p></div>
      <div class="concurrency-actions">
        <NButton v-if="canManage && data" secondary size="small" :disabled="saving" @click="toggleEditor">{{ editing ? '收起设置' : '分配并发' }}</NButton>
        <NButton secondary size="small" :loading="loading" :disabled="saving" @click="load">刷新</NButton>
      </div>
    </header>
    <p v-if="loadError" role="alert" class="concurrency-error">{{ loadError }}<span v-if="data"> · 下方为上次成功获取的数据</span></p>
    <p v-if="loading && !data" role="status" class="concurrency-empty">正在读取并发记录…</p>
    <template v-if="data">
      <dl class="concurrency-summary">
        <div><dt>当前并发</dt><dd>{{ data.current }}</dd></div>
        <div><dt>24 小时峰值</dt><dd>{{ data.peak }}</dd></div>
        <div><dt>账号上限</dt><dd>{{ data.account_max === 0 ? '不限制' : data.account_max }}</dd></div>
        <div><dt>共享池容量</dt><dd>{{ data.policy.enabled ? sharedCapacity : '未启用成员分配' }}</dd></div>
      </dl>
      <div v-if="hasHistory" class="concurrency-chart"><ConcurrencyChart :value="data" :theme="theme" :member-order="memberOrder" /></div>
      <p v-else class="concurrency-empty">并发历史从采集启用后开始记录，稍后刷新即可查看。</p>
      <p class="concurrency-note">仅统计经过 ShareSub 的请求。彩色面积为平均并发，可出现小数；深色线为同一时刻的总峰值。空白时段未采集，账号上限虚线显示当前配置。</p>
      <div class="concurrency-members" aria-label="成员当前并发">
        <span v-for="member in data.members" :key="member.user_id">{{ member.username }} <strong>{{ member.current }}</strong></span>
      </div>
      <p class="concurrency-updated">更新于 {{ new Date(data.updated_at).toLocaleTimeString('zh-CN', { hour12: false }) }} · 页面可见时每 15 秒刷新</p>
      <form v-if="editing" class="concurrency-editor" @submit.prevent="save">
        <div class="concurrency-setting-heading">
          <label for="member-concurrency-enabled">启用成员并发分配</label>
          <NSwitch id="member-concurrency-enabled" v-model:value="draft.enabled" :disabled="saving" aria-label="启用成员并发分配" />
        </div>
        <p>基础名额专属保留，剩余名额组成共享池。个人上限包含基础与共享占用；填 0 表示跟随账号上限。新成员默认基础 0、个人上限 0。</p>
        <p v-if="data.account_max === 0" class="concurrency-error">开启前，请先在账号配置中设置大于 0 的最大并发。</p>
        <div class="concurrency-table-wrap">
          <table>
            <thead><tr><th>成员</th><th>基础名额</th><th>个人上限</th></tr></thead>
            <tbody><tr v-for="member in draft.members" :key="member.user_id">
              <th scope="row">{{ member.username }}</th>
              <td><NInputNumber v-model:value="member.base_limit" :min="0" :max="100" :precision="0" :disabled="saving || !draft.enabled" :input-props="{ 'aria-label': `${member.username} 基础名额` }" /></td>
              <td><NInputNumber v-model:value="member.max_concurrency" :min="0" :max="100" :precision="0" :disabled="saving || !draft.enabled" :input-props="{ 'aria-label': `${member.username} 个人上限` }" /></td>
            </tr></tbody>
          </table>
        </div>
        <p v-if="draft.enabled">已保留 {{ reserved }} 个基础名额 · 共享池 {{ data.account_max - reserved }} 个</p>
        <p v-if="validationError" role="alert" class="concurrency-error">{{ validationError }}</p>
        <p v-if="saveError" role="alert" class="concurrency-error">{{ saveError }}</p>
        <p class="concurrency-note">设置影响后续请求，不中断正在运行的请求。停用后恢复为仅限制账号总并发。</p>
        <div class="concurrency-actions"><NButton :disabled="saving" @click="editing = false">取消</NButton><NButton type="primary" attr-type="submit" :loading="saving" :disabled="Boolean(validationError)">保存分配</NButton></div>
      </form>
      <p v-if="saved" role="status" class="concurrency-saved">并发分配已保存。</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NButton, NInputNumber, NSwitch } from 'naive-ui'
import { api } from '../api'
import { APIRequestError } from '../api/client'
import { adminAPI } from '../api/admin'
import type { ConcurrencyPolicy, PlanConcurrency } from '../types'
import type { ResolvedTheme } from '../themePreference'
import ConcurrencyChart from './ConcurrencyChart.vue'

const props = defineProps<{ planId: string; adminMode: boolean; canManage: boolean; theme: ResolvedTheme; memberOrder: string[] }>()
const data = ref<PlanConcurrency | null>(null)
const loading = ref(false)
const loadError = ref('')
const saveError = ref('')
const saving = ref(false)
const editing = ref(false)
const saved = ref(false)
// Editable number inputs can be empty; the API contract always contains numbers.
const draft = ref<{ enabled: boolean; members: { user_id: string; username: string; base_limit: number | null; max_concurrency: number | null }[] }>({ enabled: false, members: [] })
let controller: AbortController | null = null
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false
const reserved = computed(() => draft.value.members.reduce((sum, member) => sum + (member.base_limit ?? 0), 0))
const sharedCapacity = computed(() => data.value ? data.value.account_max - data.value.policy.members.reduce((sum, member) => sum + member.base_limit, 0) : 0)
const hasHistory = computed(() => data.value !== null && data.value.points.some(point => point.observed_seconds > 0))
const validationError = computed(() => {
  if (draft.value.members.some(member => member.base_limit === null || member.max_concurrency === null)) return '请填写所有成员的基础名额与个人上限。'
  if (draft.value.members.some(member => member.max_concurrency! > 0 && member.max_concurrency! < member.base_limit!)) return '个人上限不能低于基础名额。'
  if (!draft.value.enabled || !data.value) return ''
  if (data.value.account_max <= 0) return '请先设置账号最大并发。'
  if (reserved.value > data.value.account_max) return '基础名额总和不能超过账号上限。'
  return ''
})
async function load() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  try {
    const result = props.adminMode ? await adminAPI.adminPlanConcurrency(props.planId, current.signal) : await api.planConcurrency(props.planId, current.signal)
    if (current.signal.aborted || disposed) return
    data.value = result
    loadError.value = ''
  } catch (error) {
    if (!current.signal.aborted && !disposed) loadError.value = error instanceof Error ? error.message : '并发记录读取失败，请重试。'
  } finally { if (controller === current && !disposed) loading.value = false }
}
function toggleEditor() {
  if (editing.value) { editing.value = false; return }
  if (!data.value) return
  draft.value = { enabled: data.value.policy.enabled, members: data.value.policy.members.map(member => ({ ...member })) }
  editing.value = true
  saved.value = false
  saveError.value = ''
}
async function save() {
  if (validationError.value || saving.value || !props.canManage) return
  const policy: ConcurrencyPolicy = { enabled: draft.value.enabled, members: draft.value.members.map(member => ({ ...member, base_limit: member.base_limit!, max_concurrency: member.max_concurrency! })) }
  saving.value = true
  saveError.value = ''
  try {
    if (props.adminMode) await adminAPI.adminUpdatePlanConcurrency(props.planId, policy)
    else await api.updatePlanConcurrency(props.planId, policy)
    if (disposed) return
    editing.value = false
    saved.value = true
    await load()
  } catch (error) { if (!disposed) saveError.value = error instanceof APIRequestError && error.code === 'conflict' ? '成员列表或账号配置已变更，请刷新并重新打开分配设置。' : error instanceof Error ? error.message : '保存失败，请重试。' }
  finally { if (!disposed) saving.value = false }
}
onMounted(() => {
  void load()
  timer = setInterval(() => { if (document.visibilityState === 'visible' && !loading.value && !saving.value && !editing.value) void load() }, 15000)
})
watch(() => [props.planId, props.adminMode], () => { data.value = null; editing.value = false; saved.value = false; void load() })
watch(() => props.canManage, allowed => { if (!allowed) editing.value = false })
onBeforeUnmount(() => { disposed = true; controller?.abort(); clearInterval(timer) })
</script>

<style scoped>
.concurrency-panel { background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: 18px; min-width: 0; }
.concurrency-heading, .concurrency-setting-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
h4 { margin: 0; font-size: 14px; }
.concurrency-heading p, .concurrency-note, .concurrency-updated, .concurrency-editor p { color: var(--muted); font-size: 12px; line-height: 1.65; }
.concurrency-heading p { margin: 5px 0 0; }
.concurrency-actions { display: flex; gap: 8px; justify-content: flex-end; }
.concurrency-summary { display: flex; flex-wrap: wrap; gap: 16px 36px; margin: 22px 0 12px; }
.concurrency-summary div { display: flex; align-items: baseline; gap: 10px; }
dt { color: var(--muted); font-size: 12px; }
dd { margin: 0; font-weight: 700; font-size: 16px; font-variant-numeric: tabular-nums; }
.concurrency-chart { height: 300px; }
.concurrency-empty { padding: 35px 0; text-align: center; color: var(--muted); }
.concurrency-members { display: flex; flex-wrap: wrap; gap: 8px 24px; font-size: 12px; }
.concurrency-members strong { margin-left: 6px; font-variant-numeric: tabular-nums; }
.concurrency-updated { margin-bottom: 0; }
.concurrency-editor { margin-top: 20px; padding-top: 20px; border-top: 1px solid var(--line); }
.concurrency-setting-heading label { font-size: 13px; font-weight: 600; }
.concurrency-table-wrap { overflow-x: auto; }
table { width: 100%; border-collapse: collapse; font-size: 12px; }
th, td { padding: 10px 12px; text-align: left; border-bottom: 1px solid var(--line); }
td { min-width: 130px; }
.concurrency-error, .concurrency-editor .concurrency-error { color: var(--danger-ink); font-size: 12px; }
.concurrency-saved { color: var(--teal); font-size: 12px; }
@media (max-width: 640px) {
  .concurrency-panel { padding: 14px; }
  .concurrency-heading { flex-wrap: wrap; }
  .concurrency-chart { height: 260px; }
  .concurrency-summary { gap: 12px 20px; }
}
</style>

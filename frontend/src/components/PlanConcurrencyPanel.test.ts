// @vitest-environment happy-dom

import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import PlanConcurrencyPanel from './PlanConcurrencyPanel.vue'
import { api } from '../api'
import { adminAPI } from '../api/admin'
import type { PlanConcurrency } from '../types'

const fixture = (): PlanConcurrency => ({
  account_id: 'account', account_max: 12, current: 3, peak: 5, updated_at: '2026-09-14T00:01:00Z',
  policy: { enabled: true, members: [{ user_id: 'a', username: 'Alice', base_limit: 2, max_concurrency: 6 }, { user_id: 'b', username: 'Bob', base_limit: 2, max_concurrency: 6 }] },
  points: [{ bucket_start: '2026-09-14T00:00:00Z', observed_seconds: 60, average: 2.5, peak: 5 }],
  members: [{ user_id: 'a', username: 'Alice', current: 2, average: [1] }, { user_id: 'b', username: 'Bob', current: 1, average: [1.5] }],
})
const stubs = { ConcurrencyChart: true }
const mounted: ReturnType<typeof mount>[] = []
function render(canManage = true, adminMode = false) {
  const wrapper = mount(PlanConcurrencyPanel, { props: { planId: 'plan', adminMode, canManage, theme: 'light', memberOrder: [] }, global: { stubs } })
  mounted.push(wrapper)
  return wrapper
}
beforeEach(() => {
  vi.spyOn(api, 'planConcurrency').mockResolvedValue(fixture())
  vi.spyOn(api, 'updatePlanConcurrency').mockResolvedValue({ updated: true })
  vi.spyOn(adminAPI, 'adminPlanConcurrency').mockResolvedValue(fixture())
  vi.spyOn(adminAPI, 'adminUpdatePlanConcurrency').mockResolvedValue({ updated: true })
})
afterEach(() => { for (const wrapper of mounted.splice(0)) wrapper.unmount(); vi.restoreAllMocks() })

describe('PlanConcurrencyPanel', () => {
  it('shows current member usage and prevents nonowners from editing', async () => {
    const wrapper = render(false)
    await flushPromises()
    expect(wrapper.text()).toContain('最近 24 小时并发')
    expect(wrapper.text()).toContain('Alice 2')
    expect(wrapper.text()).not.toContain('分配并发')
  })
  it('validates reservations and saves the complete policy without overwriting it on refresh', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '分配并发')!.trigger('click')
    await wrapper.get('input[aria-label="Alice 个人上限"]').setValue(0)
    await wrapper.get('input[aria-label="Alice 个人上限"]').trigger('blur')
    await wrapper.get('input[aria-label="Alice 基础名额"]').setValue(11)
    await wrapper.get('input[aria-label="Alice 基础名额"]').trigger('blur')
    expect(wrapper.text()).toContain('基础名额总和不能超过账号上限')
    await wrapper.get('form').trigger('submit')
    expect(api.updatePlanConcurrency).not.toHaveBeenCalled()
    await wrapper.get('input[aria-label="Alice 基础名额"]').setValue(3)
    await wrapper.get('input[aria-label="Alice 基础名额"]').trigger('blur')
    await wrapper.get('input[aria-label="Alice 个人上限"]').setValue(6)
    await wrapper.get('input[aria-label="Alice 个人上限"]').trigger('blur')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.updatePlanConcurrency).toHaveBeenCalledWith('plan', { enabled: true, members: [{ user_id: 'a', username: 'Alice', base_limit: 3, max_concurrency: 6 }, { user_id: 'b', username: 'Bob', base_limit: 2, max_concurrency: 6 }] })
    expect(wrapper.text()).toContain('并发分配已保存')
  })
  it('uses administrator endpoints', async () => {
    const wrapper = render(true, true)
    await flushPromises()
    expect(adminAPI.adminPlanConcurrency).toHaveBeenCalled()
    expect(api.planConcurrency).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === '分配并发')!.trigger('click')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(adminAPI.adminUpdatePlanConcurrency).toHaveBeenCalled()
  })
  it('shows request errors and aborts work on unmount', async () => {
    vi.mocked(api.planConcurrency).mockRejectedValueOnce(new Error('读取失败'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('读取失败')
    const signal = vi.mocked(api.planConcurrency).mock.calls[0][1]!
    wrapper.unmount()
    mounted.splice(mounted.indexOf(wrapper), 1)
    expect(signal.aborted).toBe(true)
  })
})

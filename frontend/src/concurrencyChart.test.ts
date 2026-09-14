import { describe, expect, it } from 'vitest'
import { concurrencyChartData } from './concurrencyChart'
import type { PlanConcurrency } from './types'

export function concurrencyFixture(): PlanConcurrency {
  return {
    account_id: 'account', account_max: 12, current: 3, peak: 5, updated_at: '2026-09-14T00:01:00Z',
    policy: { enabled: true, members: [{ user_id: 'a', username: 'Alice', base_limit: 2, max_concurrency: 6 }, { user_id: 'b', username: 'Bob', base_limit: 2, max_concurrency: 6 }] },
    points: [{ bucket_start: '2026-09-14T00:00:00Z', observed_seconds: 60, average: 2.5, peak: 5 }, { bucket_start: '2026-09-14T00:01:00Z', observed_seconds: 0, average: 0, peak: 0 }],
    members: [{ user_id: 'a', username: 'Alice', current: 2, average: [1, 0] }, { user_id: 'b', username: 'Bob', current: 1, average: [1.5, 0] }],
  }
}

describe('concurrency chart', () => {
  it('stacks averages, plots the real total peak separately and leaves unobserved minutes blank', () => {
    const chart = concurrencyChartData(concurrencyFixture(), ['Bob', 'Alice'], false)
    expect(chart.datasets[0].data).toEqual([1, null])
    expect(chart.datasets[1].data).toEqual([1.5, null])
    expect(chart.datasets[2].data).toEqual([5, null])
    expect(chart.datasets[0].stack).toBe('members')
    expect(chart.datasets[2].stack).toBe('peak')
    expect(chart.datasets[0].borderColor).toBe('#18a27f')
    expect(chart.datasets[3].data).toEqual([12, 12])
  })
  it('omits an unlimited account ceiling', () => {
    const data = concurrencyFixture()
    data.account_max = 0
    expect(concurrencyChartData(data, [], true).datasets).toHaveLength(3)
  })
})

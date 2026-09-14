import type { ChartData } from 'chart.js'
import type { PlanConcurrency } from './types'

const colors = ['#4b7bec', '#18a27f', '#d59020', '#e05260', '#8b5cf6', '#d64b8c', '#18a6b8', '#eb7f43', '#57b86d', '#a66dd4', '#e3b341', '#3d93d8']

export function concurrencyChartData(value: PlanConcurrency, memberOrder: string[], dark: boolean): ChartData<'line'> {
  const names = [...memberOrder, ...value.members.map(member => member.username).filter(name => !memberOrder.includes(name))]
  const datasets: ChartData<'line'>['datasets'] = value.members.map(member => {
    const color = colors[names.indexOf(member.username) % colors.length]
    return {
      label: member.username,
      data: member.average.map((average, i) => value.points[i].observed_seconds > 0 ? average : null),
      borderColor: color,
      backgroundColor: `${color}66`,
      order: 2, stack: 'members', fill: true, stepped: 'before', pointRadius: 0, borderWidth: 1,
    }
  })
  datasets.push({
    label: '总并发峰值', data: value.points.map(point => point.observed_seconds > 0 ? point.peak : null),
    borderColor: dark ? '#f7f5f2' : '#222327', backgroundColor: 'transparent',
    order: 1, stack: 'peak', fill: false, stepped: 'before', borderWidth: 1.5, pointRadius: 0,
  })
  if (value.account_max > 0) datasets.push({
    label: '当前账号上限', data: value.points.map(() => value.account_max),
    borderColor: dark ? '#ed715c' : '#c94734', backgroundColor: 'transparent',
    order: 0, stack: 'limit', fill: false, borderDash: [6, 5], borderWidth: 1.5, pointRadius: 0,
  })
  return {
    labels: value.points.map(point => new Date(point.bucket_start).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })),
    datasets,
  }
}

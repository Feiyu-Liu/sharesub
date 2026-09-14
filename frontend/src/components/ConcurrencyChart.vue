<template>
  <Line :data="chartData" :options="options" role="img" aria-label="最近24小时成员平均并发堆叠图、账号总并发峰值和当前上限" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Line } from 'vue-chartjs'
import { CategoryScale, Chart as ChartJS, Filler, Legend, LinearScale, LineElement, PointElement, Tooltip, type ChartOptions } from 'chart.js'
import type { PlanConcurrency } from '../types'
import type { ResolvedTheme } from '../themePreference'
import { concurrencyChartData } from '../concurrencyChart'

ChartJS.register(CategoryScale, LinearScale, LineElement, PointElement, Filler, Legend, Tooltip)
const props = defineProps<{ value: PlanConcurrency; theme: ResolvedTheme; memberOrder: string[] }>()
const chartData = computed(() => concurrencyChartData(props.value, props.memberOrder, props.theme === 'dark'))
const options = computed<ChartOptions<'line'>>(() => {
  const dark = props.theme === 'dark'
  const text = dark ? '#a5a19b' : '#6d7078'
  return {
    responsive: true, maintainAspectRatio: false, animation: false,
    interaction: { intersect: false, mode: 'index' },
    plugins: {
      legend: { labels: { color: text, usePointStyle: true, boxWidth: 7, padding: 16, font: { size: 10 } } },
      tooltip: {
        backgroundColor: dark ? '#292a2d' : '#ffffff',
        titleColor: dark ? '#f7f5f2' : '#222327', bodyColor: text,
        borderColor: dark ? '#45474a' : '#e2e3df', borderWidth: 1, padding: 12,
        callbacks: {
          label: item => `${item.dataset.label}: ${Number(item.parsed.y).toLocaleString('zh-CN', { maximumFractionDigits: 2 })}`,
          afterBody: items => {
            if (!items.length) return ''
            const point = props.value.points[items[0].dataIndex]
            return point.observed_seconds > 0
              ? `总平均并发：${point.average.toFixed(2)}\n本分钟已采集 ${Math.min(60, point.observed_seconds).toFixed(0)} 秒`
              : '此分钟尚无采集数据'
          },
        },
      },
    },
    scales: {
      x: { grid: { display: false }, ticks: { color: text, maxTicksLimit: 9, maxRotation: 0, font: { size: 10 } } },
      y: { stacked: true, beginAtZero: true, title: { display: true, text: '并发请求数', color: text }, grid: { color: dark ? '#353638' : '#e2e3df' }, ticks: { color: text, precision: 0 } },
    },
  }
})
</script>

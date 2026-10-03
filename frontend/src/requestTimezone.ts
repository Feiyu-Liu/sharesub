import type { RequestTimezone } from './types'

export const requestTimezones: Array<{ value: RequestTimezone; label: string }> = [
  { value: '', label: '关闭（默认）' },
  { value: 'Asia/Singapore', label: '新加坡（Asia/Singapore）' },
  { value: 'Asia/Tokyo', label: '东京（Asia/Tokyo）' },
  { value: 'Asia/Seoul', label: '首尔（Asia/Seoul）' },
  { value: 'America/Los_Angeles', label: '美西（America/Los_Angeles）' },
  { value: 'America/New_York', label: '美东（America/New_York）' },
  { value: 'Europe/London', label: '伦敦（Europe/London）' },
]

export function requestTimezoneLabel(timezone: RequestTimezone) {
  return requestTimezones.find(candidate => candidate.value === timezone)!.label
}

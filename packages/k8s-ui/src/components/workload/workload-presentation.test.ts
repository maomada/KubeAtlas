import { afterEach, expect, it, vi } from 'vitest'
import {
  formatWorkloadCronDescription,
  formatWorkloadDuration,
  formatWorkloadRelativeAgeTime,
  formatWorkloadRolloutDetail,
  formatWorkloadRolloutLabel,
  formatWorkloadStatusText,
} from './workload-presentation'

afterEach(() => vi.useRealTimers())

it('formats workload relative time and preserves empty and invalid inputs', () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-04-21T12:00:00Z'))
  expect(formatWorkloadRelativeAgeTime('2026-04-21T08:00:00Z')).toBe('4 小时前')
  expect(formatWorkloadRelativeAgeTime(undefined, '从未')).toBe('从未')
  expect(formatWorkloadRelativeAgeTime('', '从未')).toBe('从未')
  expect(formatWorkloadRelativeAgeTime('not-a-timestamp')).toBe('not-a-timestamp')
})

it('uses a localized clock-skew fallback and preserves long-future timestamps', () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-04-21T12:00:00Z'))
  expect(formatWorkloadRelativeAgeTime('2026-04-21T12:00:30Z')).toBe('刚刚')
  const future = '2026-04-21T12:05:00Z'
  expect(formatWorkloadRelativeAgeTime(future)).toBe(new Date(future).toLocaleString('zh-CN'))
})

it('localizes generated rollout summaries while preserving raw phases and reasons', () => {
  expect(formatWorkloadRolloutDetail('1/1 updated · 1 available')).toBe('1/1 已更新 · 1 个可用')
  expect(formatWorkloadRolloutDetail('1/1 ready')).toBe('1/1 就绪')
  expect(formatWorkloadRolloutLabel('Stable')).toBe('稳定')
  expect(formatWorkloadRolloutLabel('constructor')).toBe('constructor')
  expect(formatWorkloadRolloutLabel('toString')).toBe('toString')
  expect(formatWorkloadRolloutLabel('New revision cannot start')).toBe('新版本无法启动')
  expect(formatWorkloadRolloutDetail('constructor')).toBe('constructor')
  expect(formatWorkloadRolloutDetail('toString')).toBe('toString')
  expect(formatWorkloadCronDescription('constructor')).toBe('constructor')
  expect(formatWorkloadStatusText('Running')).toBe('Running')
  expect(formatWorkloadStatusText('Deleted')).toBe('已删除')
  expect(formatWorkloadRolloutDetail('BackOff')).toBe('BackOff')
})

it('localizes each generated rollout-detail shape', () => {
  const cases: Array<[string, string]> = [
    ['No rollout is pending', '当前没有待处理的发布'],
    ['No replicas requested', '未请求副本'],
    ['Selector matches no nodes', '选择器未匹配到节点'],
    ['Waiting for the Deployment controller to observe generation', '等待 Deployment 控制器观察到当前版本'],
    ['The Rollout controller reported a failed revision', 'Rollout 控制器报告了失败版本'],
    ['The template change was accepted; waiting for live rollout status', '模板更改已接受，正在等待实时发布状态'],
    ['1/2 available', '1/2 个可用'],
    ['1/2 up-to-date', '1/2 已是最新'],
    ['Paused at 1/2 updated', '已暂停：1/2 已更新'],
    ['Waiting for old replicas to stop · 1 available', '等待旧副本停止 · 1 个可用'],
    ['OnDelete strategy · 1/2 updated', 'OnDelete 策略 · 1/2 已更新'],
    ['1/2 target Pods updated · 1/2 ready', '1/2 个目标 Pod 已更新 · 1/2 就绪'],
    ['1/2 Pods intentionally retained', '1/2 个 Pod 按配置保留'],
  ]
  for (const [detail, expected] of cases) expect(formatWorkloadRolloutDetail(detail)).toBe(expected)
})

it('localizes generated duration and cron descriptions without rewriting raw schedules', () => {
  expect(formatWorkloadDuration(2 * 24 * 60 * 60_000 + 5 * 60 * 60_000)).toBe('2 天 5 小时')
  const cronDescriptions: Array<[string, string]> = [
    ['Daily at midnight', '每天 00:00'],
    ['Every hour', '每小时'],
    ['Every 12 hours', '每 12 小时'],
    ['Every minute', '每分钟'],
    ['Daily at 4:00', '每天 4:00'],
    ['Every hour at :05', '每小时的第 05 分钟'],
    ['Every 5 minutes', '每 5 分钟'],
    ['Weekdays at 9:15', '工作日 9:15'],
  ]
  for (const [description, expected] of cronDescriptions) expect(formatWorkloadCronDescription(description)).toBe(expected)
  expect(formatWorkloadCronDescription('0 * * * *')).toBe('0 * * * *')
})

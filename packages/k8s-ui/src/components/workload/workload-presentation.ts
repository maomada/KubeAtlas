const rolloutLabels: Record<string, string> = {
  Stable: '稳定',
  Healthy: '健康',
  Degraded: '需关注',
  Unhealthy: '不健康',
  Alert: '告警',
  Neutral: '中性',
  Unknown: '未知',
  'Applying change': '正在应用更改',
  'Rollout stalled': '发布停滞',
  'Rollout paused': '发布已暂停',
  'Scaled to zero': '已缩至零',
  'Scaled to 0': '已缩至 0',
  'No targets': '无匹配节点',
  'Replacing replicas': '正在替换副本',
  'Waiting for new revision': '等待新版本',
  Scaling: '正在扩缩容',
  'Rolling out': '正在发布',
  'Waiting for Pod restart': '等待 Pod 重启',
  'Partition reached': '已达到更新分区',
  'Waiting for controller': '等待控制器状态',
  'New revision cannot start': '新版本无法启动',
  Deleted: '已删除',
}

const rolloutDetails: Record<string, string> = {
  'No rollout is pending': '当前没有待处理的发布',
  'No replicas requested': '未请求副本',
  'Selector matches no nodes': '选择器未匹配到节点',
  'Waiting for the Deployment controller to observe generation': '等待 Deployment 控制器观察到当前版本',
  'Waiting for the StatefulSet controller to observe generation': '等待 StatefulSet 控制器观察到当前版本',
  'Waiting for the DaemonSet controller to observe generation': '等待 DaemonSet 控制器观察到当前版本',
  'Waiting for the Rollout controller to observe generation': '等待 Rollout 控制器观察到当前版本',
  'The Rollout controller reported a failed revision': 'Rollout 控制器报告了失败版本',
  'The template change was accepted; waiting for live rollout status': '模板更改已接受，正在等待实时发布状态',
}

const cronDescriptions: Record<string, string> = {
  'Daily at midnight': '每天 00:00',
  'Every hour': '每小时',
  'Every minute': '每分钟',
}

function ownLookup(values: Record<string, string>, key: string): string | undefined {
  return Object.prototype.hasOwnProperty.call(values, key) ? values[key] : undefined
}

export function formatWorkloadRolloutLabel(label: string): string {
  return ownLookup(rolloutLabels, label) ?? label
}

export function formatWorkloadStatusText(text: string): string {
  return text.split(' · ').map((part) => {
    const label = formatWorkloadRolloutLabel(part)
    if (label !== part) return label
    const updating = /^Updating (\d+)\/(\d+)$/.exec(part)
    return updating ? `正在更新 ${updating[1]}/${updating[2]}` : part
  }).join(' · ')
}

export function formatWorkloadRolloutDetail(detail: string | undefined): string {
  if (!detail) return ''
  const exact = ownLookup(rolloutDetails, detail)
  if (exact) return exact

  let match = /^(\d+)\/(\d+) updated · (\d+) available$/.exec(detail)
  if (match) return `${match[1]}/${match[2]} 已更新 · ${match[3]} 个可用`
  match = /^(\d+)\/(\d+) available$/.exec(detail)
  if (match) return `${match[1]}/${match[2]} 个可用`
  match = /^(\d+)\/(\d+) ready$/.exec(detail)
  if (match) return `${match[1]}/${match[2]} 就绪`
  match = /^(\d+)\/(\d+) up-to-date$/.exec(detail)
  if (match) return `${match[1]}/${match[2]} 已是最新`
  match = /^Paused at (\d+)\/(\d+) updated$/.exec(detail)
  if (match) return `已暂停：${match[1]}/${match[2]} 已更新`
  match = /^Waiting for old replicas to stop · (\d+) available$/.exec(detail)
  if (match) return `等待旧副本停止 · ${match[1]} 个可用`
  match = /^OnDelete strategy · (\d+)\/(\d+) updated$/.exec(detail)
  if (match) return `OnDelete 策略 · ${match[1]}/${match[2]} 已更新`
  match = /^(\d+)\/(\d+) target Pods updated · (\d+)\/(\d+) ready$/.exec(detail)
  if (match) return `${match[1]}/${match[2]} 个目标 Pod 已更新 · ${match[3]}/${match[4]} 就绪`
  match = /^(\d+)\/(\d+) Pods intentionally retained$/.exec(detail)
  if (match) return `${match[1]}/${match[2]} 个 Pod 按配置保留`
  return detail
}

export function formatWorkloadRelativeAgeTime(value?: string, fallback = '-'): string {
  if (!value) return fallback
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value

  const elapsedMs = Date.now() - timestamp
  if (elapsedMs < 0) {
    return elapsedMs > -60_000 ? '刚刚' : new Date(timestamp).toLocaleString('zh-CN')
  }
  const seconds = Math.floor(elapsedMs / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}

export function formatWorkloadDuration(ms: number): string {
  const seconds = Math.floor(ms / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)
  if (days > 0) return `${days} 天 ${hours % 24} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes % 60} 分钟`
  if (minutes > 0) return `${minutes} 分钟 ${seconds % 60} 秒`
  return `${seconds} 秒`
}

export function formatWorkloadCronDescription(description: string): string {
  const exact = ownLookup(cronDescriptions, description)
  if (exact) return exact

  let match = /^Every (\d+) hours?$/.exec(description)
  if (match) return `每 ${match[1]} 小时`
  match = /^Daily at (\d{1,2}:\d{2})$/.exec(description)
  if (match) return `每天 ${match[1]}`
  match = /^Every hour at :(\d{2})$/.exec(description)
  if (match) return `每小时的第 ${match[1]} 分钟`
  match = /^Every (\d+) minutes?$/.exec(description)
  if (match) return `每 ${match[1]} 分钟`
  match = /^Weekdays at (\d{1,2}:\d{2})$/.exec(description)
  if (match) return `工作日 ${match[1]}`
  return description
}

import type { Translate } from '../../hooks/useLocale';
import type { IssueSeverity } from './types';
import { BADGE_SEVERITY_COLORS as sev } from '../ui/Badge';
import { NEUTRAL_CHIP_CLASS } from '../ui/CardSection';
import {
  TONE_FILL_CLASS,
  TONE_HEADER_BAND_CLASS,
  TONE_RAIL_CLASS,
  TONE_SOLID_CLASS,
  TONE_TEXT_CLASS,
  type SeverityTone,
} from '../ui/severity-tone';

// Visual language for the 2-tier Issues severity. critical = red, warning =
// amber. Issues and Checks are different severity axes but must read as one
// product, so the color strings are shared via the tone module; here we only map
// each tier onto its tone. The pill still pulls from the canonical Badge tones
// so it matches every status badge elsewhere.
const ISSUE_SEVERITY_TONE: Record<IssueSeverity, SeverityTone> = {
  critical: 'red',
  warning: 'amber',
};

const byTone = <T,>(toneMap: Record<SeverityTone, T>): Record<IssueSeverity, T> => ({
  critical: toneMap[ISSUE_SEVERITY_TONE.critical],
  warning: toneMap[ISSUE_SEVERITY_TONE.warning],
});

export const ISSUE_SEVERITY_LABEL: Record<IssueSeverity, string> = {
  critical: '严重',
  warning: '警告',
};

// Pill badge — the loud, explicit severity signal on a row (rendered with `badge-sm`).
export const ISSUE_SEVERITY_BADGE_CLASS: Record<IssueSeverity, string> = {
  critical: sev.error,
  warning: sev.warning,
};

export const ISSUE_SEVERITY_TEXT_CLASS = byTone(TONE_TEXT_CLASS);
export const ISSUE_SEVERITY_FILL_CLASS = byTone(TONE_FILL_CLASS);
export const ISSUE_SEVERITY_RAIL_CLASS = byTone(TONE_RAIL_CLASS);
export const ISSUE_SEVERITY_SOLID_CLASS = byTone(TONE_SOLID_CLASS);
export const ISSUE_SEVERITY_HEADER_BAND_CLASS = byTone(TONE_HEADER_BAND_CLASS);

const GROUP_CHIP_EMPHASIS_CLASS = 'inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-semibold leading-tight bg-theme-elevated text-theme-text-primary ring-1 ring-theme-border-light';

const EMPHASIZED_GROUPS = new Set(['control_plane', 'unknown']);

export function groupBadgeClass(group: string): string {
  return EMPHASIZED_GROUPS.has(group) ? GROUP_CHIP_EMPHASIS_CLASS : NEUTRAL_CHIP_CLASS;
}

// Display labels. The server emits raw snake_case category/group enums (so a
// new category needs no frontend deploy to APPEAR); the UI humanizes for
// display, falling back to title-cased snake_case for anything unmapped.
const CATEGORY_LABEL: Record<string, string> = {
  unschedulable: '无法调度',
  quota_exceeded: '超出配额',
  admission_webhook_blocking: '准入被阻止',
  image_pull_failed: '镜像拉取失败',
  container_waiting: '容器等待中',
  init_container_failed: '初始化容器失败',
  crashloop: '崩溃循环',
  oom_killed: 'OOM 终止',
  liveness_probe_failed: '健康检查失败',
  readiness_failed: '尚未就绪，无法接收流量',
  workload_degraded: '工作负载状态降级',
  high_restart: '重启次数过多',
  missing_config_ref: '缺少引用',
  pdb_blocks_evictions: '驱逐操作受阻',
  secret_sync_failed: 'Secret 同步失败',
  service_no_endpoints: '没有端点',
  service_mixed_backends: 'Service 后端不一致',
  ingress_backend_missing: 'Ingress 后端缺失',
  ingress_class_missing: 'Ingress 类别缺失',
  load_balancer_pending: '负载均衡器等待中',
  gateway_not_ready: 'Gateway 尚未就绪',
  gateway_route_invalid: 'Gateway 路由无效',
  dns_failure: 'DNS 失败',
  network_policy_denied: 'NetworkPolicy 阻止流量',
  pvc_pending: 'PVC 等待中',
  pvc_lost: 'PVC 丢失',
  pv_failed: 'PV 失败',
  pvc_resize_failed: 'PVC 扩容失败',
  volume_mount_failed: '卷挂载失败',
  volume_access_mode_conflict: '卷访问模式冲突',
  backup_failed: '备份失败',
  backup_target_unavailable: '备份目标不可用',
  backup_stalled: '备份停滞',
  job_failed: 'Job 失败',
  cronjob_failed: 'CronJob 需要处理',
  rollout_stalled: '发布停滞',
  hpa_limited_or_failed: '自动扩缩受限或失败',
  rbac_forbidden: '权限不足',
  certificate_not_ready: 'Certificate 尚未就绪',
  pod_security_violation: 'Pod Security 阻止操作',
  node_not_ready: 'Node 尚未就绪',
  node_provisioning_failed: 'Node 配置失败',
  apiservice_unavailable: 'API 扩展不可用',
  crossplane_reconcile_failed: 'Crossplane 调和失败',
  termination_stuck: '终止过程停滞',
  operator_condition_failed: '控制器报告问题',
  gitops_sync_failed: 'GitOps 同步失败',
  gitops_render_failed: 'GitOps 渲染失败',
  gitops_spec_invalid: 'GitOps spec 无效',
  gitops_operation_failed: 'GitOps 操作失败',
  gitops_out_of_sync: 'GitOps 不同步',
  gitops_health_degraded: 'GitOps 健康状态降级',
  gitops_stale: 'GitOps 比对结果已过期',
  helm_release_failed: 'Helm release 失败',
  webhook_backend_down: 'Webhook 后端不可用',
  control_plane_not_ready: '控制平面尚未就绪',
  machine_not_ready: 'Machine 尚未就绪',
  unknown: '未知',
};

const GROUP_LABEL: Record<string, string> = {
  scheduling: '调度',
  startup: '启动',
  runtime: '运行时',
  configuration: '配置',
  networking: '网络',
  storage: '存储',
  scaling: '扩缩',
  security: '安全',
  control_plane: '控制平面',
  unknown: '未知',
};

function humanize(raw: string): string {
  if (!raw) return '';
  const spaced = raw.replace(/_/g, ' ');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

const preferChinese: Translate = zh => zh;

export function categoryLabel(category: string, t: Translate = preferChinese): string {
  const english = category === 'crashloop' ? 'Crash loop' : humanize(category);
  return t(CATEGORY_LABEL[category] ?? english, english);
}

// Reasons filed under a category whose label names a different operation:
// CNPG archiving and an unanswered scheduled run sit under backup_failed.
const REASON_TITLE: Record<string, string> = {
  InvalidSchedule: 'Scheduling error reported',
  UnparseableSchedule: 'Scheduling error reported',
  UnParseableCronJobSchedule: 'Scheduling error reported',
  UnknownTimeZone: 'Time zone error reported',
  MissSchedule: 'Missed schedule reported',
  TooManyMissedTimes: 'Missed schedules reported',
  CNPGWALArchivingFailing: "WAL archiving failing",
  CNPGLastBackupFailed: "Latest backup failed",
  CNPGScheduleDestinationMissing: "Backup schedule has no destination",
  CNPGInstanceReadinessMismatch: "Instance Pods contradict Cluster readiness",
  CNPGPrimaryLabelMismatch: "Primary Pod label contradicts Cluster status",
  CNPGScheduledRunNoBackup: "No successful backup since a scheduled run",
};

/** The title a reason has wherever it is shown, when its category's label would name the wrong operation. */
export function issueReasonTitle(reason: string | undefined): string | undefined {
  return reason ? REASON_TITLE[reason] : undefined;
}

/** An issue row's title: its category, unless the reason names it better. */
export function issueTitle(issue: { category: string; reason?: string }, t: Translate = preferChinese): string {
  return issueReasonTitle(issue.reason) || categoryLabel(issue.category, t);
}

export function groupLabel(group: string, t: Translate = preferChinese): string {
  const english = humanize(group);
  return t(GROUP_LABEL[group] ?? english, english);
}

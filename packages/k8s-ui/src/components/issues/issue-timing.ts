import type { Translate } from '../../hooks/useLocale';
import { isDeploymentLikeWorkloadKind } from '../../types';
import type { Issue } from './types';

const preferChinese: Translate = zh => zh;

export type IssueTimingDisplayKind = 'creation' | 'regression' | 'deletion';

export interface IssueTimingDisplay {
  kind: IssueTimingDisplayKind;
  chip: string;
  meta: string;
  tooltip: string;
}

export function issueFirstSeenTitle(issue: Issue, t: Translate = preferChinese): string | null {
  if (!issue.first_seen) return null;
  const coverage = issue.onset_coverage;
  const unknown = coverage?.unknown ?? 0;
  const base = t(`至少从 ${new Date(issue.first_seen).toLocaleString()} 起处于活动状态`, `Active since at least ${new Date(issue.first_seen).toLocaleString()}`);
  if (unknown === 0) return `${base}.`;
  const total = (coverage?.known ?? 0) + unknown;
  return t(`${base}；其中 ${unknown}/${total} 个信号的时间未知。`, `${base}. Onset is unknown for ${unknown}/${total} signals.`);
}

export function issueResourceCreatedTitle(issue: Issue, t: Translate = preferChinese): string | null {
  if (!issue.resource_created_at) return null;
  const affectedCount = Math.max(issue.count ?? 0, issue.members?.length ?? 0);
  const label = affectedCount > 1
    ? t('最早受影响资源的创建时间', 'Earliest affected resource created')
    : affectedCount === 1
      ? t('受影响资源的创建时间', 'Affected resource created')
      : t('资源创建时间', 'Resource created');
  return `${label} ${new Date(issue.resource_created_at).toLocaleString()}`;
}

function isDeploymentLikeCreation(issue: Issue): boolean {
  const group = issue.group ?? '';
  if (issue.kind === 'Pod') {
    return issue.issue_timing_basis === 'pod_creation'
      || issue.issue_timing_basis === 'owner_condition';
  }
  return isDeploymentLikeWorkloadKind(issue.kind, group);
}

function issueTimingIndependentOfOnset(issue: Issue): boolean {
  return issue.issue_timing_basis === 'owner_condition'
    || issue.issue_timing_basis === 'pod_creation'
    || issue.issue_timing_basis === 'spec';
}

function onsetUncertaintyMeta(issue: Issue, t: Translate): string {
  const coverage = issue.onset_coverage;
  const unknown = coverage?.unknown ?? 0;
  if (issue.first_seen && (coverage?.known ?? 0) > 0 && unknown > 0) {
    return t(`确切开始时间未知（${unknown}/${(coverage?.known ?? 0) + unknown} 个信号）`, `Exact onset is unknown (${unknown}/${(coverage?.known ?? 0) + unknown} signals)`);
  }
  return t('确切开始时间未知', 'Exact onset is unknown');
}

function onsetUncertaintyTooltip(issue: Issue, t: Translate): string {
  const coverage = issue.onset_coverage;
  const unknown = coverage?.unknown ?? 0;
  if (issue.first_seen && (coverage?.known ?? 0) > 0 && unknown > 0) {
    return t(`无法确定 ${unknown}/${(coverage?.known ?? 0) + unknown} 个信号的确切开始时间。`, `The exact onset is unknown for ${unknown}/${(coverage?.known ?? 0) + unknown} signals.`);
  }
  return t('无法确定此问题的确切开始时间。', 'The exact onset of this issue is unknown.');
}

export function issueTimingForDisplay(issue: Issue, t: Translate = preferChinese): IssueTimingDisplay | null {
  const partialOnset = Boolean(issue.first_seen && (issue.onset_coverage?.unknown ?? 0) > 0);
  if (partialOnset && !issueTimingIndependentOfOnset(issue)) return null;
  return issueTiming(issue, t);
}

export function issueTiming(issue: Issue, t: Translate = preferChinese): IssueTimingDisplay | null {
  const coverage = issue.onset_coverage;
  const onsetUnknown = issue.onset_unknown || (coverage?.unknown ?? 0) > 0;
  const mixedOnset = Boolean(issue.first_seen && (coverage?.known ?? 0) > 0 && (coverage?.unknown ?? 0) > 0);
  const grouped = (coverage?.known ?? 0) + (coverage?.unknown ?? 0) > 1;
  const uncertaintyMeta = onsetUncertaintyMeta(issue, t);
  const uncertaintyTooltip = onsetUncertaintyTooltip(issue, t);
  // A stuck deletion is timed from its deletionTimestamp. The creation-vs-healthy
  // classification only says whether the object was deleted soon after it was
  // created, which is not what the age beside this chip measures.
  if (issue.issue_timing_basis === 'deletion' && issue.issue_timing) {
    return {
      kind: 'deletion',
      chip: t('自删除起', 'Since deletion was requested'),
      meta: t('自请求删除后持续待删除', 'Deletion has been pending since it was requested'),
      tooltip: t('从请求删除时开始计时；对象仍然存在。', 'Timed from the delete request. The object still exists.'),
    };
  }
  switch (issue.issue_timing) {
    case 'started_after_resource_was_healthy': {
      if (issue.issue_timing_basis === 'owner_condition') {
        if (onsetUnknown) {
          return {
            kind: 'regression',
            chip: mixedOnset ? t('健康状态回退', 'Health regressed') : t('工作负载健康状态回退 · 开始时间未知', 'Workload health regressed · onset unknown'),
            meta: t(`${uncertaintyMeta}；工作负载在当前健康状态回退前曾处于健康状态`, `${uncertaintyMeta}. The workload was healthy before this regression.`),
            tooltip: t(`所属工作负载在当前故障状态前曾处于健康状态。 ${uncertaintyTooltip}`, `The owning workload was healthy before this failure. ${uncertaintyTooltip}`),
          };
        }
        return {
          kind: 'regression',
          chip: t('健康状态回退', 'Health regressed'),
          meta: t('工作负载健康状态回退', 'Workload health regressed'),
          tooltip: t('所属工作负载在当前故障状态前曾处于健康状态。这并不能确定此问题的发生时间或归因。', 'The owning workload was healthy before this state. The onset and cause of this issue remain unknown.'),
        };
      }
      if (onsetUnknown && issue.issue_timing_basis === 'pod_creation') {
        if (grouped) {
          return {
            kind: 'regression',
            chip: t('健康期后新建的 Pod 发生故障', 'New Pods failed after a healthy period'),
            meta: t(`${uncertaintyMeta}；故障 Pod 在此前健康期后创建`, `${uncertaintyMeta}. Failed Pods were created after the earlier healthy period.`),
            tooltip: t(`故障 Pod 在此前健康期后创建。 ${uncertaintyTooltip}`, `Failed Pods were created after the earlier healthy period. ${uncertaintyTooltip}`),
          };
        }
        return {
          kind: 'regression',
          chip: t('后续发布出现故障', 'Later rollout failed'),
          meta: t(`${uncertaintyMeta}；受影响的 Pod 属于此前健康期之后的后续发布`, `${uncertaintyMeta}. Affected Pods belong to a later rollout, after the earlier healthy period.`),
          tooltip: t(`受影响的 Pod 属于此前健康期之后的工作负载修订版本。 ${uncertaintyTooltip}`, `Affected Pods belong to a workload revision after the earlier healthy period. ${uncertaintyTooltip}`),
        };
      }
      return {
        kind: 'regression',
        chip: issue.kind === 'Node' && issue.reason === 'NotReady' ? 'NotReady' : t('健康期后发生故障', 'Failed after a healthy period'),
        meta: t('故障信号出现在健康期之后', 'The failure signal appeared after a healthy period'),
        tooltip: issue.kind === 'Node' && issue.reason === 'NotReady' ? t('Ready 条件从此时起不再报告健康状态。', 'The Ready condition stopped reporting a healthy state at this time.') : t('此故障信号出现前曾有一段健康期。', 'This failure signal followed a healthy period.'),
      };
    }
    case 'started_at_resource_creation': {
      if (onsetUnknown && issue.issue_timing_basis === 'owner_condition') {
        return {
          kind: 'creation',
          chip: t('工作负载从未健康', 'Workload never became healthy'),
          meta: t(`${uncertaintyMeta}；工作负载部署后始终未达到健康状态`, `${uncertaintyMeta}. The workload never became healthy after deployment.`),
          tooltip: t(`所属工作负载部署后始终未达到健康状态。 ${uncertaintyTooltip}`, `The owning workload never became healthy after deployment. ${uncertaintyTooltip}`),
        };
      }
      if (onsetUnknown && issue.issue_timing_basis === 'pod_creation') {
        if (grouped) {
          return {
            kind: 'creation',
            chip: t('启动阶段 Pod 故障', 'Pod startup failure'),
            meta: t(`${uncertaintyMeta}；故障发生在工作负载启动期间创建的 Pod 中`, `${uncertaintyMeta}. Failures appeared in Pods created during workload startup.`),
            tooltip: t(`故障发生在工作负载启动期间创建的 Pod 中。 ${uncertaintyTooltip}`, `Failures appeared in Pods created during workload startup. ${uncertaintyTooltip}`),
          };
        }
        return {
          kind: 'creation',
          chip: t('Pod 在启动阶段失败', 'Pods failed during startup'),
          meta: t(`${uncertaintyMeta}；受影响的 Pod 在工作负载启动期间失败`, `${uncertaintyMeta}. Affected Pods failed during workload startup.`),
          tooltip: t(`受影响的 Pod 在其工作负载修订版本启动期间失败。 ${uncertaintyTooltip}`, `Affected Pods failed during startup of their workload revision. ${uncertaintyTooltip}`),
        };
      }
      if (onsetUnknown && issue.issue_timing_basis === 'spec') {
        return {
          kind: 'creation',
          chip: t('首次协调时即无效', 'Invalid from first reconciliation'),
          meta: t(`${uncertaintyMeta}；初始 spec 从首次协调起就处于故障状态`, `${uncertaintyMeta}. The initial spec failed from its first reconciliation.`),
          tooltip: t(`初始 spec 表明该资源从首次协调起就处于故障状态。 ${uncertaintyTooltip}`, `The initial spec shows that this resource failed from its first reconciliation. ${uncertaintyTooltip}`),
        };
      }
      if (isDeploymentLikeCreation(issue)) {
        return {
          kind: 'creation',
          chip: t('自部署起', 'Since deployment'),
          meta: t('自部署或首次协调时即存在', 'Present since deployment or first reconciliation'),
          tooltip: t('故障信号始于部署或首次协调期间。', 'The failure signal began during deployment or first reconciliation.'),
        };
      }
      return {
        kind: 'creation',
        chip: t('自创建起', 'Since creation'),
        meta: t('自创建或首次协调时即存在', 'Present since creation or first reconciliation'),
        tooltip: t('故障信号始于资源创建或首次协调期间。', 'The failure signal began during resource creation or first reconciliation.'),
      };
    }
    default:
      return null;
  }
}

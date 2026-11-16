import { isDeploymentLikeWorkloadKind } from '../../types';
import type { Issue } from './types';

export type IssueTimingDisplayKind = 'creation' | 'regression' | 'deletion';

export interface IssueTimingDisplay {
  kind: IssueTimingDisplayKind;
  chip: string;
  meta: string;
  tooltip: string;
}

export function issueFirstSeenTitle(issue: Issue): string | null {
  if (!issue.first_seen) return null;
  const coverage = issue.onset_coverage;
  const unknown = coverage?.unknown ?? 0;
  const base = `至少从 ${new Date(issue.first_seen).toLocaleString()} 起处于活动状态`;
  if (unknown === 0) return `${base}.`;
  const total = (coverage?.known ?? 0) + unknown;
  return `${base}；其中 ${unknown}/${total} 个信号的时间未知。`;
}

export function issueResourceCreatedTitle(issue: Issue): string | null {
  if (!issue.resource_created_at) return null;
  const affectedCount = Math.max(issue.count ?? 0, issue.members?.length ?? 0);
  const label = affectedCount > 1
    ? '最早受影响资源的创建时间'
    : affectedCount === 1
      ? '受影响资源的创建时间'
      : '资源创建时间';
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

function onsetUncertaintyMeta(issue: Issue): string {
  const coverage = issue.onset_coverage;
  const unknown = coverage?.unknown ?? 0;
  if (issue.first_seen && (coverage?.known ?? 0) > 0 && unknown > 0) {
    return `确切开始时间未知（${unknown}/${(coverage?.known ?? 0) + unknown} 个信号）`;
  }
  return '确切开始时间未知';
}

function onsetUncertaintyTooltip(issue: Issue): string {
  const coverage = issue.onset_coverage;
  const unknown = coverage?.unknown ?? 0;
  if (issue.first_seen && (coverage?.known ?? 0) > 0 && unknown > 0) {
    return `无法确定 ${unknown}/${(coverage?.known ?? 0) + unknown} 个信号的确切开始时间。`;
  }
  return '无法确定此问题的确切开始时间。';
}

export function issueTimingForDisplay(issue: Issue): IssueTimingDisplay | null {
  const partialOnset = Boolean(issue.first_seen && (issue.onset_coverage?.unknown ?? 0) > 0);
  if (partialOnset && !issueTimingIndependentOfOnset(issue)) return null;
  return issueTiming(issue);
}

export function issueTiming(issue: Issue): IssueTimingDisplay | null {
  const coverage = issue.onset_coverage;
  const onsetUnknown = issue.onset_unknown || (coverage?.unknown ?? 0) > 0;
  const mixedOnset = Boolean(issue.first_seen && (coverage?.known ?? 0) > 0 && (coverage?.unknown ?? 0) > 0);
  const grouped = (coverage?.known ?? 0) + (coverage?.unknown ?? 0) > 1;
  const uncertaintyMeta = onsetUncertaintyMeta(issue);
  const uncertaintyTooltip = onsetUncertaintyTooltip(issue);
  // A stuck deletion is timed from its deletionTimestamp. The creation-vs-healthy
  // classification only says whether the object was deleted soon after it was
  // created, which is not what the age beside this chip measures.
  if (issue.issue_timing_basis === 'deletion' && issue.issue_timing) {
    return {
      kind: 'deletion',
      chip: '自删除起',
      meta: '自请求删除后持续待删除',
      tooltip: '从请求删除时开始计时；对象仍然存在。',
    };
  }
  switch (issue.issue_timing) {
    case 'started_after_resource_was_healthy': {
      if (issue.issue_timing_basis === 'owner_condition') {
        if (onsetUnknown) {
          return {
            kind: 'regression',
            chip: mixedOnset ? '健康状态回退' : '工作负载健康状态回退 · 开始时间未知',
            meta: `${uncertaintyMeta}；工作负载在当前健康状态回退前曾处于健康状态`,
            tooltip: `所属工作负载在当前故障状态前曾处于健康状态。 ${uncertaintyTooltip}`,
          };
        }
        return {
          kind: 'regression',
          chip: '健康状态回退',
          meta: '工作负载健康状态回退',
          tooltip: '所属工作负载在当前故障状态前曾处于健康状态。这并不能确定此问题的发生时间或归因。',
        };
      }
      if (onsetUnknown && issue.issue_timing_basis === 'pod_creation') {
        if (grouped) {
          return {
            kind: 'regression',
            chip: '健康期后新建的 Pod 发生故障',
            meta: `${uncertaintyMeta}；故障 Pod 在此前健康期后创建`,
            tooltip: `故障 Pod 在此前健康期后创建。 ${uncertaintyTooltip}`,
          };
        }
        return {
          kind: 'regression',
          chip: '后续发布出现故障',
          meta: `${uncertaintyMeta}；受影响的 Pod 属于此前健康期之后的后续发布`,
          tooltip: `受影响的 Pod 属于此前健康期之后的工作负载修订版本。 ${uncertaintyTooltip}`,
        };
      }
      return {
        kind: 'regression',
        chip: issue.kind === 'Node' && issue.reason === 'NotReady' ? 'NotReady' : '健康期后发生故障',
        meta: '故障信号出现在健康期之后',
        tooltip: issue.kind === 'Node' && issue.reason === 'NotReady' ? 'Ready 条件从此时起不再报告健康状态。' : '此故障信号出现前曾有一段健康期。',
      };
    }
    case 'started_at_resource_creation': {
      if (onsetUnknown && issue.issue_timing_basis === 'owner_condition') {
        return {
          kind: 'creation',
          chip: '工作负载从未健康',
          meta: `${uncertaintyMeta}；工作负载部署后始终未达到健康状态`,
          tooltip: `所属工作负载部署后始终未达到健康状态。 ${uncertaintyTooltip}`,
        };
      }
      if (onsetUnknown && issue.issue_timing_basis === 'pod_creation') {
        if (grouped) {
          return {
            kind: 'creation',
            chip: '启动阶段 Pod 故障',
            meta: `${uncertaintyMeta}；故障发生在工作负载启动期间创建的 Pod 中`,
            tooltip: `故障发生在工作负载启动期间创建的 Pod 中。 ${uncertaintyTooltip}`,
          };
        }
        return {
          kind: 'creation',
          chip: 'Pod 在启动阶段失败',
          meta: `${uncertaintyMeta}；受影响的 Pod 在工作负载启动期间失败`,
          tooltip: `受影响的 Pod 在其工作负载修订版本启动期间失败。 ${uncertaintyTooltip}`,
        };
      }
      if (onsetUnknown && issue.issue_timing_basis === 'spec') {
        return {
          kind: 'creation',
          chip: '首次协调时即无效',
          meta: `${uncertaintyMeta}；初始 spec 从首次协调起就处于故障状态`,
          tooltip: `初始 spec 表明该资源从首次协调起就处于故障状态。 ${uncertaintyTooltip}`,
        };
      }
      if (isDeploymentLikeCreation(issue)) {
        return {
          kind: 'creation',
          chip: '自部署起',
          meta: '自部署或首次协调时即存在',
          tooltip: '故障信号始于部署或首次协调期间。',
        };
      }
      return {
        kind: 'creation',
        chip: '自创建起',
        meta: '自创建或首次协调时即存在',
        tooltip: '故障信号始于资源创建或首次协调期间。',
      };
    }
    default:
      return null;
  }
}

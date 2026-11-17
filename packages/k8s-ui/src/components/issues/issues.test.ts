// @vitest-environment jsdom
import { afterEach, describe, it, expect, vi } from 'vitest'
import { act, createElement } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToString } from 'react-dom/server'
import { compareIssues, issueSortAnchor, subjectRef, memberRef, normalizeImagePullMessage, issueMessageParts, type Issue } from './types'
import { categoryLabel, groupBadgeClass, groupLabel, issueTitle } from './severity'
import { IssueRow, IssuesView } from './IssuesView'
import { issueFirstSeenTitle, issueResourceCreatedTitle, issueTiming } from './issue-timing'

const base: Issue = {
  id: 'id-0',
  severity: 'warning',
  source: 'problem',
  category: 'crashloop',
  category_group: 'runtime',
  grouping_scope: 'workload',
  kind: 'Deployment',
  name: 'app',
  reason: 'CrashLoopBackOff',
}
const mk = (o: Partial<Issue>): Issue => ({ ...base, ...o })

afterEach(() => {
  vi.useRealTimers()
})

describe('compareIssues', () => {
  it('orders critical before warning regardless of observed age', () => {
    const warn = mk({ id: 'w', severity: 'warning', first_seen: '2026-05-01T00:00:00Z' }) // newer
    const crit = mk({ id: 'c', severity: 'critical', first_seen: '2026-01-01T00:00:00Z' }) // older
    expect([warn, crit].sort(compareIssues).map((i) => i.id)).toEqual(['c', 'w'])
  })

  it('breaks same-severity ties by first_seen DESC (newest observed issue first)', () => {
    const older = mk({ id: 'o', first_seen: '2026-01-01T00:00:00Z' })
    const newer = mk({ id: 'n', first_seen: '2026-05-01T00:00:00Z' })
    expect([older, newer].sort(compareIssues).map((i) => i.id)).toEqual(['n', 'o'])
  })

  it('compares sort anchors as instants across timezone formats', () => {
    const newerLocal = mk({ id: 'local', first_seen: '2026-08-09T23:00:00-07:00' })
    const olderUTC = mk({ id: 'utc', onset_unknown: true, resource_created_at: '2026-08-10T01:00:00Z' })
    expect([olderUTC, newerLocal].sort(compareIssues).map((i) => i.id)).toEqual(['local', 'utc'])
  })

  it('orders direct startup blockers before generic problem rows at same severity', () => {
    const generic = mk({ id: 'generic', source: 'problem', first_seen: '2026-05-01T00:00:00Z' })
    const blocker = mk({ id: 'blocker', source: 'scheduling', first_seen: '2026-01-01T00:00:00Z' })
    const missing = mk({ id: 'missing', source: 'missing_ref', first_seen: '2026-03-01T00:00:00Z' })
    expect([generic, blocker, missing].sort(compareIssues).map((i) => i.id)).toEqual(['blocker', 'missing', 'generic'])
  })

  it('does NOT reshuffle same-severity rows when only last_seen changes (anti-churn)', () => {
    // Two same-severity rows, same first_seen — order is the deterministic name tiebreak.
    const a = mk({ id: 'id-a', name: 'a', first_seen: '2026-01-01T00:00:00Z', last_seen: '2026-05-01T00:00:00Z' })
    const b = mk({ id: 'id-b', name: 'b', first_seen: '2026-01-01T00:00:00Z', last_seen: '2026-05-30T00:00:00Z' })
    const before = [a, b].sort(compareIssues).map((i) => i.id)
    expect(before).toEqual(['id-a', 'id-b'])
    // A refetch bumps a's last_seen to "now". Sorting on last_seen would flip the
    // order; keying on first_seen + identity must NOT — this is the whole point
    // of the first_seen-based sort.
    const aRefetched = mk({ ...a, last_seen: '2026-06-01T00:00:00Z' })
    const after = [aRefetched, b].sort(compareIssues).map((i) => i.id)
    expect(after).toEqual(before)
  })

  it('uses resource creation as the stable fallback only when onset is unknown', () => {
    const unknownOlder = mk({ id: 'unknown-old', name: 'unknown-old', onset_unknown: true, resource_created_at: '2026-01-01T00:00:00Z' })
    const unknownNewer = mk({ id: 'unknown-new', name: 'unknown-new', onset_unknown: true, resource_created_at: '2026-05-01T00:00:00Z' })
    const known = mk({ id: 'known', name: 'known', first_seen: '2026-03-01T00:00:00Z', resource_created_at: '2025-01-01T00:00:00Z' })
    const noMetadata = mk({ id: 'none', name: 'none', onset_unknown: true })

    expect(issueSortAnchor(known)).toBe('2026-03-01T00:00:00Z')
    expect([unknownOlder, noMetadata, known, unknownNewer].sort(compareIssues).map((i) => i.id)).toEqual([
      'unknown-new',
      'known',
      'unknown-old',
      'none',
    ])
  })
})

describe('category/group label fallbacks', () => {
  it('returns the mapped label, else humanizes (server-added category needs no frontend deploy)', () => {
    expect(categoryLabel('crashloop')).toBe('崩溃循环')
    expect(categoryLabel('some_new_future_category')).toBe('Some new future category')
    expect(issueTitle({ category: 'backup_failed', reason: 'CNPGWALArchivingFailing' })).toBe('WAL archiving failing')
    expect(issueTitle({ category: 'backup_failed', reason: 'BackupFailed' })).toBe('备份失败')
  })
  it('humanizes an unmapped group', () => {
    expect(groupLabel('runtime')).toBe('运行时')
    expect(groupLabel('some_future_group')).toBe('Some future group')
  })
  it('keeps ordinary group chips neutral and emphasizes control-plane/unknown groups', () => {
    expect(groupBadgeClass('runtime')).toContain('text-theme-text-secondary')
    expect(groupBadgeClass('runtime')).not.toContain('ring-theme-border-light')
    expect(groupBadgeClass('control_plane')).toContain('text-theme-text-primary')
    expect(groupBadgeClass('control_plane')).toContain('ring-theme-border-light')
    expect(groupBadgeClass('unknown')).toContain('ring-theme-border-light')
    expect(groupBadgeClass('some_future_group')).toContain('text-theme-text-secondary')
    expect(groupBadgeClass('some_future_group')).not.toContain('ring-theme-border-light')
  })
})

it('localizes the healthy and unavailable issue-queue empty states', () => {
  const healthy = renderToString(createElement(IssuesView, { issues: [], anyData: true }))
  expect(healthy).toContain('当前没有问题')
  expect(healthy).toContain('所选范围内没有活动问题')

  const unavailable = renderToString(createElement(IssuesView, { issues: [], anyData: false }))
  expect(unavailable).toContain('暂无问题数据')
  expect(unavailable).toContain('连接集群后即可填充问题队列')
})

it('localizes the truncated affected-resource message while preserving resource identity', async () => {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  act(() => {
    root.render(createElement(IssueRow, {
      issue: mk({
        count: 5,
        members: [
          { kind: 'Pod', namespace: 'payments-prod', name: 'api-0' },
          { kind: 'Pod', namespace: 'payments-prod', name: 'api-1' },
        ],
        members_truncated: true,
      }),
      open: true,
      onToggle: () => undefined,
    }))
  })
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  expect(host.textContent).toContain('已显示 2 / 5 个受影响资源；展开对象可查看其余资源。')
  expect(host.textContent).toContain('Pod')
  expect(host.textContent).toContain('payments-prod')
  expect(host.textContent).toContain('api-0')
  expect(host.textContent).not.toContain('Showing 2 of 5')
  act(() => root.unmount())
  host.remove()
  document.body.innerHTML = ''
})

describe('IssueRow', () => {
  it('keeps relative age visible and adds deployment timing as a secondary tag', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-30T12:00:00Z'))

    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        id: 'baseline',
        first_seen: '2026-06-28T12:00:00Z',
        issue_timing: 'started_at_resource_creation',
      }),
      open: false,
      onToggle: () => undefined,
    }))

    expect(html).toContain('2d')
    expect(html).toContain('自部署起')
  })

  it('promotes after-healthy timing in the collapsed row', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-30T12:00:00Z'))

    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        id: 'regression',
        first_seen: '2026-06-30T10:00:00Z',
        issue_timing: 'started_after_resource_was_healthy',
        issue_timing_basis: 'condition',
      }),
      open: false,
      onToggle: () => undefined,
    }))

    expect(html).toContain('2h')
    expect(html).toContain('健康期后发生故障')
  })

  it('labels a stuck deletion by how long deletion has been pending', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-30T12:00:00Z'))

    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        id: 'terminating',
        kind: 'VMAgent',
        group: 'operator.victoriametrics.com',
        category: 'termination_stuck',
        category_group: 'control_plane',
        reason: 'Terminating stuck',
        first_seen: '2026-06-30T11:30:00Z',
        issue_timing: 'started_at_resource_creation',
        issue_timing_basis: 'deletion',
      }),
      open: true,
      onToggle: () => undefined,
    }))

    expect(html).toContain('30m')
    expect(html).toContain('自删除起')
    expect(html).toContain('自请求删除后持续待删除')
    expect(html).not.toContain('自创建起')
    expect(html).not.toContain('first reconciliation')
  })

  it('uses the category-group chip class in the queue row', () => {
    const html = renderToString(createElement(IssueRow, {
      issue: mk({ category_group: 'control_plane', kind: 'Node' }),
      open: false,
      onToggle: () => undefined,
    }))

    expect(html).toContain('控制平面')
    expect(html).toContain(groupBadgeClass('control_plane'))
  })

  it('does not fill collapsed rows with low-information unknown-onset labels', () => {
    const html = renderToString(createElement(IssueRow, {
      issue: mk({ onset_unknown: true }),
      open: false,
      onToggle: () => undefined,
    }))

    expect(html).not.toMatch(/onset unknown/i)
    expect(html).not.toContain('0s')
  })

  it('shows independent timing alongside an unknown onset', () => {
    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        onset_unknown: true,
        issue_timing: 'started_at_resource_creation',
        issue_timing_basis: 'owner_condition',
      }),
      open: true,
      onToggle: () => undefined,
    }))

    expect(html).toContain('工作负载从未健康')
    expect(html).toContain('确切开始时间未知；工作负载部署后始终未达到健康状态')
    expect(html).not.toContain('自部署起')
  })

  it('qualifies a workload regression when the issue onset is unknown', () => {
    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        onset_unknown: true,
        issue_timing: 'started_after_resource_was_healthy',
        issue_timing_basis: 'owner_condition',
      }),
      open: true,
      onToggle: () => undefined,
    }))

    expect(html).toContain('工作负载健康状态回退')
    expect(html).toContain('开始时间未知')
    expect(html).toContain('确切开始时间未知；工作负载在当前健康状态回退前曾处于健康状态')
    expect(html).not.toContain('workload health regressed')
  })

  it('renders mixed groups as a lower-bound age and suppresses a group-wide timing claim', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-30T12:00:00Z'))

    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        first_seen: '2026-06-30T10:00:00Z',
        onset_coverage: { known: 2, unknown: 1 },
        issue_timing: 'started_after_resource_was_healthy',
      }),
      open: true,
      onToggle: () => undefined,
    }))

    expect(html).toContain('≥')
    expect(html).toContain('其中 1/3 个信号的时间未知')
    expect(html).toContain('2 小时前')
    expect(html).not.toContain('健康期后发生故障')
  })

  it('keeps onset-independent workload evidence visible on mixed groups', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-30T12:00:00Z'))

    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        first_seen: '2026-06-30T10:00:00Z',
        onset_coverage: { known: 1, unknown: 1 },
        issue_timing: 'started_after_resource_was_healthy',
        issue_timing_basis: 'owner_condition',
      }),
      open: true,
      onToggle: () => undefined,
    }))

    expect(html).toContain('≥')
    expect(html).toContain('健康状态回退')
    expect(html).not.toContain('工作负载健康状态回退 · 开始时间未知')
    expect(html).not.toContain('工作负载健康状态回退')
    expect(html).toContain('确切开始时间未知（1/2 个信号）')
    expect(html).toContain('部分信号至少从 2 小时前起持续至今')
    expect(html).not.toContain('exact onset unknown;')
  })

  it('keeps lower-bound age, unknown count, and freshness for mixed creation groups', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-30T12:00:00Z'))

    const html = renderToString(createElement(IssueRow, {
      issue: mk({
        first_seen: '2026-06-30T10:00:00Z',
        last_seen: '2026-06-30T11:30:00Z',
        onset_coverage: { known: 1, unknown: 1 },
        issue_timing: 'started_at_resource_creation',
        issue_timing_basis: 'owner_condition',
      }),
      open: true,
      onToggle: () => undefined,
    }))

    expect(html).toContain('工作负载从未健康')
    expect(html).toContain('确切开始时间未知（1/2 个信号）')
    expect(html).toContain('部分信号至少从 2 小时前起持续至今')
    expect(html).toContain('最近出现于 30 分钟前')
  })

  it('presents ordinary first-seen time as a conservative active lower bound', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-30T12:00:00Z'))

    const html = renderToString(createElement(IssueRow, {
      issue: mk({ first_seen: '2026-06-30T10:00:00Z' }),
      open: true,
      onToggle: () => undefined,
    }))

    expect(html).toContain('至少从 2 小时前起持续至今')
    expect(html).toContain('dateTime="2026-06-30T10:00:00Z"')
    expect(html).toContain('>2h</time>')
    expect(html).not.toContain('开始于 2 小时前')
  })
})

describe('onset provenance copy', () => {
  it('describes a known first-seen value as an active lower bound', () => {
    const title = issueFirstSeenTitle(mk({ first_seen: '2026-06-30T10:00:00Z' }))

    expect(title).toContain('至少从')
    expect(title).not.toContain('started')
  })

  it('labels a one-member workload rollup as affected-resource context', () => {
    const title = issueResourceCreatedTitle(mk({
      kind: 'Deployment',
      count: 1,
      members: [{ kind: 'Pod', namespace: 'prod', name: 'web-abc' }],
      resource_created_at: '2026-06-30T10:00:00Z',
    }))

    expect(title).toContain('受影响资源的创建时间')
  })
})

describe('issueTiming', () => {
  it('keeps deployment wording for deployment-like creation failures', () => {
    const deployment = issueTiming(mk({
      kind: 'Deployment',
      group: 'apps',
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'condition',
    }))
    expect(deployment).toMatchObject({
      kind: 'creation',
      chip: '自部署起',
      meta: '自部署或首次协调时即存在',
      tooltip: '故障信号始于部署或首次协调期间。',
    })
    expect(`${deployment?.chip} ${deployment?.meta} ${deployment?.tooltip}`).not.toMatch(/since deploy|since creation/i)

    expect(issueTiming(mk({
      kind: 'Rollout',
      group: 'argoproj.io',
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'condition',
    }))).toMatchObject({
      kind: 'creation',
      chip: '自部署起',
      meta: '自部署或首次协调时即存在',
      tooltip: '故障信号始于部署或首次协调期间。',
    })
  })

  it('uses deployment wording for pod issues only when workload timing evidence is present', () => {
    expect(issueTiming(mk({
      kind: 'Pod',
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'pod_creation',
    }))).toMatchObject({
      kind: 'creation',
      chip: '自部署起',
      tooltip: '故障信号始于部署或首次协调期间。',
    })

    const creation = issueTiming(mk({
      kind: 'Pod',
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'phase',
    }))
    expect(creation).toMatchObject({
      kind: 'creation',
      chip: '自创建起',
      meta: '自创建或首次协调时即存在',
      tooltip: '故障信号始于资源创建或首次协调期间。',
    })
    expect(`${creation?.chip} ${creation?.meta} ${creation?.tooltip}`).not.toMatch(/since deploy|since creation/i)
  })

  it('uses self-explanatory copy when exact onset and independent timing have different scopes', () => {
    expect(issueTiming(mk({
      kind: 'Pod',
      onset_unknown: true,
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'pod_creation',
    }))).toMatchObject({
      chip: 'Pod 在启动阶段失败',
      meta: '确切开始时间未知；受影响的 Pod 在工作负载启动期间失败',
    })

    expect(issueTiming(mk({
      kind: 'Rollout',
      group: 'argoproj.io',
      onset_unknown: true,
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'spec',
    }))).toMatchObject({
      chip: '首次协调时即无效',
      meta: '确切开始时间未知；初始 spec 从首次协调起就处于故障状态',
    })
  })

  it('uses group-safe pod timing copy for multiple contributing signals', () => {
    expect(issueTiming(mk({
      kind: 'Deployment',
      onset_coverage: { known: 0, unknown: 3 },
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'pod_creation',
    }))).toMatchObject({
      chip: '启动阶段 Pod 故障',
      meta: '确切开始时间未知；故障发生在工作负载启动期间创建的 Pod 中',
    })

    expect(issueTiming(mk({
      kind: 'Deployment',
      onset_coverage: { known: 0, unknown: 3 },
      issue_timing: 'started_after_resource_was_healthy',
      issue_timing_basis: 'pod_creation',
    }))).toMatchObject({
      chip: '健康期后新建的 Pod 发生故障',
      meta: '确切开始时间未知；故障 Pod 在此前健康期后创建',
    })
  })

  it('uses resource creation wording for non-deployment creation failures', () => {
    expect(issueTiming(mk({
      kind: 'PersistentVolumeClaim',
      issue_timing: 'started_at_resource_creation',
      issue_timing_basis: 'phase',
    }))).toMatchObject({
      kind: 'creation',
      chip: '自创建起',
      meta: '自创建或首次协调时即存在',
      tooltip: '故障信号始于资源创建或首次协调期间。',
    })
  })

  it('times stuck deletions from the deletion request, however soon after creation it came', () => {
    for (const timing of ['started_at_resource_creation', 'started_after_resource_was_healthy'] as const) {
      for (const kind of ['Pod', 'VMAgent']) {
        const display = issueTiming(mk({
          kind,
          category: 'termination_stuck',
          issue_timing: timing,
          issue_timing_basis: 'deletion',
        }))
        expect(display).toMatchObject({
          kind: 'deletion',
          chip: '自删除起',
          meta: '自请求删除后持续待删除',
        })
        expect(`${display?.chip} ${display?.meta} ${display?.tooltip}`).not.toMatch(/creation|healthy|deploy/i)
      }
    }
  })

  it('describes direct after-healthy timing without coupling it to first_seen', () => {
    const display = issueTiming(mk({
      first_seen: '2026-06-30T10:00:00Z',
      issue_timing: 'started_after_resource_was_healthy',
      issue_timing_basis: 'condition',
    }))

    expect(display).toMatchObject({
      kind: 'regression',
      chip: '健康期后发生故障',
      meta: '故障信号出现在健康期之后',
    })
    expect(`${display?.chip} ${display?.meta} ${display?.tooltip}`).not.toContain('2h')
    expect(`${display?.chip} ${display?.meta} ${display?.tooltip}`).not.toMatch(/baseline|safe|ignore/i)
  })

  it('presents owner-condition timing as workload-level rather than cause-specific', () => {
    const display = issueTiming(mk({
      first_seen: '2026-06-30T10:00:00Z',
      issue_timing: 'started_after_resource_was_healthy',
      issue_timing_basis: 'owner_condition',
    }))

    expect(display).toMatchObject({
      kind: 'regression',
      chip: '健康状态回退',
      meta: '工作负载健康状态回退',
    })
    expect(display?.tooltip).toContain('并不能确定此问题的发生时间或归因')
    expect(`${display?.chip} ${display?.meta} ${display?.tooltip}`).not.toContain('2h')
  })

  it('returns null when there is no confident timing signal', () => {
    expect(issueTiming(mk({ issue_timing: undefined, issue_timing_basis: undefined }))).toBeNull()
  })
})

describe('subjectRef / memberRef', () => {
  it('subjectRef defaults empty group/namespace and threads cluster_id', () => {
    const issue = mk({ cluster_id: 'cl_1', kind: 'Deployment', name: 'web' }) // no group/namespace
    expect(subjectRef(issue)).toEqual({ cluster_id: 'cl_1', group: '', kind: 'Deployment', namespace: '', name: 'web' })
  })
  it('memberRef threads the issue cluster_id onto a member', () => {
    const issue = mk({ cluster_id: 'cl_2' })
    const member = { group: 'apps', kind: 'Pod', namespace: 'ns', name: 'p1' }
    expect(memberRef(issue, member)).toEqual({ ...member, cluster_id: 'cl_2' })
  })
})

describe('image-pull message normalization', () => {
  const notFound =
    'Back-off pulling image "reg.io/team/api:v2": ErrImagePull: rpc error: code = NotFound desc = failed to pull and unpack image "reg.io/team/api:v2": failed to resolve reference "reg.io/team/api:v2": "reg.io/team/api:v2": not found'

  it('extracts cause + single image ref from the verbose CRI string', () => {
    expect(normalizeImagePullMessage(notFound)).toBe('Image not found: reg.io/team/api:v2')
  })
  it('classifies the common failure modes', () => {
    expect(normalizeImagePullMessage('pull access denied for image "x:1", repository does not exist or may require authorization')).toBe('Not authorized to pull image: x:1')
    expect(normalizeImagePullMessage('failed to pull image "x:1": dial tcp: lookup reg.io: no such host')).toBe('Registry unreachable: x:1')
    expect(normalizeImagePullMessage('toomanyrequests: rate limit exceeded for image "x:1"')).toBe('Registry rate-limited: x:1')
  })
  it('returns null for shapes it does not recognize (caller keeps raw)', () => {
    expect(normalizeImagePullMessage('some novel kubelet error')).toBeNull()
    expect(normalizeImagePullMessage('')).toBeNull()
  })

  it('issueMessageParts normalizes image-pull headline and keeps raw as detail', () => {
    const parts = issueMessageParts(mk({ category: 'image_pull_failed', reason: 'ImagePullBackOff', message: notFound }))
    expect(parts.headline).toBe('Image not found: reg.io/team/api:v2')
    expect(parts.detail).toBe(notFound)
  })
  it('does NOT mislabel a non-image "not found" message (gating)', () => {
    // missing_config_ref carries 'secret "x" not found' — must stay verbatim, no detail split.
    const parts = issueMessageParts(mk({ category: 'missing_config_ref', reason: 'Missing Secret', message: 'secret "project-infra" not found' }))
    expect(parts.headline).toBe('secret "project-infra" not found')
    expect(parts.detail).toBe('')
  })
})

describe('IssueRow diagnosis raw messages', () => {
  it('shows raw_message when cleaned issue copy has no parsed cause', () => {
    const issue = mk({
      category: 'gitops_operation_failed',
      category_group: 'configuration',
      severity: 'critical',
      reason: 'OperationFailed',
      message: 'app path does not exist',
      raw_message: 'rpc error: code = Unknown desc = app path does not exist',
    })

    const html = renderToString(createElement(IssueRow, { issue, open: true, onToggle: () => undefined, as: 'div' }))

    expect(html).toContain('app path does not exist')
    expect(html).toContain('rpc error: code = Unknown desc = app path does not exist')
  })

  it("doesn't present KubeAtlas's own summary as a raw error", () => {
    const base = {
      category: 'network_policy_denied',
      category_group: 'networking',
      severity: 'warning' as const,
      reason: 'NetworkPolicy denies all ingress',
      message: 'Would block all inbound traffic to the 2 pods behind Service recommendation',
      cause: 'NetworkPolicy deny-all leaves the 2 pods behind Service recommendation with no allowed inbound traffic.',
    }
    const kubeatlas = renderToString(createElement(IssueRow, {
      issue: mk({
        ...base,
        message_source: 'kubeatlas',
        diagnostic_context: { role: 'candidate', facts: [{ type: 'network_policy_isolates', confidence: 'high', refs: [{ kind: 'Service', namespace: 'shop', name: 'recommendation' }] }] },
      }),
      open: true, onToggle: () => undefined, as: 'div',
    }))
    expect(kubeatlas).not.toContain('原始错误')
    expect(kubeatlas).toContain('leaves the 2 pods behind Service recommendation')
    expect(kubeatlas).toContain('受影响的资源')

    // Without the marker the message is still treated as detector output.
    const unmarked = renderToString(createElement(IssueRow, { issue: mk(base), open: true, onToggle: () => undefined, as: 'div' }))
    expect(unmarked).toContain('原始错误')
  })
})

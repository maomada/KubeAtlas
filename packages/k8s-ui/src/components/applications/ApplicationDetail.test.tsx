import { describe, expect, it, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import { ApplicationDetail, type ApplicationDetailProps } from './ApplicationDetail'
import type { AppHistory, AppRow, AppSourceRef } from '../../utils/applications'
import { workloadKey } from '../../utils/topology-neighborhood'

const app: AppRow = {
  key: "app:prod:checkout",
  name: "checkout",
  namespace: "prod",
  health: "healthy",
  workload_class: "mixed",
  appVersion: "1.2.3",
  workloads: [
    {
      kind: "Deployment",
      namespace: "prod",
      name: "checkout-api",
      workload_class: "service",
      health: "healthy",
      ready: 3,
      desired: 3,
      restarts: 0,
      version: "1.2.3",
    },
    {
      kind: "Deployment",
      namespace: "prod",
      name: "checkout-worker",
      workload_class: "worker",
      health: "healthy",
      ready: 1,
      desired: 1,
      restarts: 0,
      version: "1.2.3",
    },
  ],
  relationships: {
    services: ["checkout-api"],
    ingresses: ["checkout"],
    configs: 2,
    scalers: 1,
    storage: 1,
    networkPolicies: 1,
    serviceRefs: [{ kind: 'Service', namespace: 'prod', name: 'checkout-api' }],
    ingressRefs: [{ kind: 'Ingress', namespace: 'prod', name: 'checkout' }],
    configRefs: [
      { kind: 'ConfigMap', namespace: 'prod', name: 'checkout-config' },
      { kind: 'Secret', namespace: 'prod', name: 'checkout-secret' },
    ],
    scalerRefs: [{ kind: 'HorizontalPodAutoscaler', namespace: 'prod', name: 'checkout-api' }],
    storageRefs: [{ kind: 'PersistentVolumeClaim', namespace: 'prod', name: 'checkout-data' }],
    networkPolicyRefs: [{ kind: 'NetworkPolicy', namespace: 'prod', name: 'checkout-ingress' }],
  },
  events: [
    {
      type: "Warning",
      reason: "BackOff",
      object: "Deployment/checkout-api",
      message: "Back-off restarting failed container",
      count: 2,
      lastSeen: "1m ago",
    },
  ],
};

function renderDetail(props: Partial<ApplicationDetailProps> = {}) {
  const componentProps = {
    app,
    onBack: () => {},
    renderWorkload: (workload) => <div>Runtime for {workload.name}</div>,
    renderOverviewIssues: () => null,
    ...props,
  } as ApplicationDetailProps;
  return renderToString(<ApplicationDetail {...componentProps} />);
}

describe("ApplicationDetail shell", () => {
  it("shows rollout activity in the application summary and workload matrix", () => {
    const rolling: AppRow = {
      ...app,
      workloads: app.workloads.map((workload, index) => index === 0 ? {
        ...workload,
        rollout: {
          phase: 'progressing',
          active: true,
          manual: false,
          label: 'Rolling out',
          detail: '2/3 updated · 3 available',
          desired: 3,
          updated: 2,
          ready: 3,
          available: 3,
        },
      } : workload),
    }
    const html = renderDetail({ app: rolling })

    expect(html).toContain('Rolling out')
    expect(html).toContain('1 个工作负载受影响 · 4/4 就绪')
    expect(html).toContain('2/3 updated · 3 available')
  })

  it("preserves the built-in issue surface when the host slot is omitted", () => {
    const html = renderDetail({ renderOverviewIssues: undefined });

    expect(html).toContain('应用视图')
    expect(html).toContain('概览')
    expect(html).toContain('拓扑')
    expect(html).toContain('历史记录')
    expect(html).toContain('BackOff on checkout-api')
    expect(html).toContain('应用身份')
    expect(html).toContain('根据 Kubernetes 所有权分组')
    expect(html).toContain('入口')
    expect(html).toContain('依赖')
    expect(html).toContain('svc<!-- -->/</span>checkout-api')
    expect(html).toContain('ing<!-- -->/</span>checkout')
    expect(html).toContain('配置')
    expect(html).toContain('自动扩缩容')
    expect(html).toContain('存储')
    expect(html).toContain('网络策略')
    expect(html).not.toContain('Related resources')
    expect(html).toContain('工作负载')
    expect(html).not.toContain('Runtime for')
    expect(html).not.toContain('Components')
    expect(html).not.toContain('变更')
    expect(html).not.toContain('YAML')
    expect(html).not.toContain('成本')
    expect(html).not.toContain('>Deploy<')
  })

  it("defaults to the application overview scope", () => {
    const html = renderDetail();

    expect(html).toContain("应用视图");
    expect(html).toContain("概览");
    expect(html).toContain("拓扑");
    expect(html).toContain("历史记录");
    expect(html).not.toContain("BackOff on checkout-api");
    expect(html).toContain("应用身份");
    expect(html).toContain("入口");
    expect(html).toContain("依赖");
    expect(html).toContain("svc<!-- -->/</span>checkout-api");
    expect(html).toContain("ing<!-- -->/</span>checkout");
    expect(html).toContain("配置");
    expect(html).toContain("自动扩缩容");
    expect(html).toContain("存储");
    expect(html).not.toContain("Related resources");
    expect(html).toContain("工作负载");
    expect(html).not.toContain("Runtime for");
    expect(html).not.toContain("Components");
    expect(html).not.toContain("变更");
    expect(html).not.toContain("YAML");
    expect(html).not.toContain("成本");
    expect(html).not.toContain(">Deploy<");
  });

  it("renders complete localized overview copy and preserves native resource names and kinds", () => {
    const overviewApp: AppRow = {
      ...app,
      relationships: {
        ...app.relationships,
        pdbRefs: [
          { kind: "PodDisruptionBudget", namespace: "prod", name: "checkout-pdb" },
        ],
      },
    };
    const html = renderDetail({ app: overviewApp });

    expect(html).toContain('aria-label="应用视图"');
    expect(html).toContain("流量可通过 Ingress 或 Route 资源进入此应用。");
    expect(html).toContain("应用工作负载引用的 ConfigMap 和 Secret。");
    expect(html).toContain("应用工作负载挂载的 PersistentVolumeClaim。");
    expect(html).toContain("保护应用工作负载的 PodDisruptionBudget。");
    expect(html).toContain("选择应用工作负载的 NetworkPolicy。");
    expect(html).toContain("checkout-config");
    expect(html).toContain("checkout-secret");
    expect(html).toContain("checkout-pdb");
    expect(html).toContain("checkout-api");
    expect(html).toContain(">Deployment</td>");
    expect(html).toContain("svc<!-- -->/</span>checkout-api");
    expect(html).not.toContain("Application views");
  });

  it("renders the localized topology empty state", () => {
    const html = renderDetail({
      selectedView: "topology",
      onSelectView: () => {},
      topology: undefined,
    });

    expect(html).toContain("暂无拓扑数据");
    expect(html).toContain("KubeAtlas 无法为此应用构建拓扑。");
  });

  it("describes a zero desired replica count without implying it is unset", () => {
    const zeroDesiredApp: AppRow = {
      ...app,
      workloads: app.workloads.map((workload) => ({
        ...workload,
        ready: 0,
        desired: 0,
      })),
    };
    const html = renderDetail({ app: zeroDesiredApp });

    expect(html).toContain("没有期望副本");
  });

  it("shows workload scope without application view tabs when the URL names a workload", () => {
    const selected = app.workloads[1];
    const html = renderDetail({
      selectedView: "overview",
      onSelectView: () => {},
      selectedWorkloadKey: workloadKey(selected),
      onSelectWorkload: () => {},
    });

    expect(html).toContain('Runtime for')
    expect(html).toContain('checkout-worker')
    expect(html).not.toContain('应用视图')
    expect(html).not.toContain('概览')
    expect(html).not.toContain('拓扑')
    expect(html).not.toContain('历史记录')
    expect(html).not.toContain('应用身份')
  })

  it("renders the optional application cost view only when the host selects it", () => {
    const withCostTab = renderDetail({
      selectedView: "overview",
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      renderCostView: () => <div>Application cost body</div>,
    });

    expect(withCostTab).toContain("成本");
    expect(withCostTab).not.toContain("Application cost body");

    const selectedCost = renderDetail({
      selectedView: "overview",
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      renderCostView: () => <div>Application cost body</div>,
      costViewSelected: true,
      onSelectCostView: () => {},
    });

    expect(selectedCost).toContain("成本");
    expect(selectedCost).toContain("Application cost body");
    expect(selectedCost).not.toContain("Source &amp; provenance");
  });

  it("does not render a workload selector for a single-workload app", () => {
    const singleApp: AppRow = {
      ...app,
      workloads: [app.workloads[0]],
    };
    const selected = singleApp.workloads[0];
    const html = renderDetail({
      app: singleApp,
      selectedWorkloadKey: workloadKey(selected),
      onSelectWorkload: () => {},
    });

    expect(html).toContain("Runtime for");
    expect(html).toContain("checkout-api");
    expect(html).toContain("工作负载");
    expect(html).toContain("Deployment<!-- -->/<!-- -->checkout-api");
    expect(html).not.toContain('aria-haspopup="listbox"');
    expect(html).not.toContain("应用视图");
  });

  it("opens the workload directly for uncontrolled single-workload apps", () => {
    const singleApp: AppRow = {
      ...app,
      workloads: [app.workloads[0]],
    };
    const html = renderDetail({ app: singleApp });

    expect(html).toContain("Runtime for");
    expect(html).toContain("checkout-api");
    expect(html).not.toContain("应用视图");
  });

  // A controlled host's only way to say "no workload in the URL" is null, so
  // without this the application scope a single-workload app must not have
  // becomes the default for every URL-driven host.
  it("opens the workload directly for controlled single-workload apps with no workload in the URL", () => {
    const singleApp: AppRow = {
      ...app,
      workloads: [app.workloads[0]],
    };
    const html = renderDetail({
      app: singleApp,
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
    });

    expect(html).toContain("Runtime for");
    expect(html).toContain("checkout-api");
    expect(html).not.toContain("应用视图");
    expect(html).not.toContain('aria-haspopup="listbox"');
  });

  it("keeps workload scope for a controlled single-workload app whose URL names a workload it no longer has", () => {
    const singleApp: AppRow = {
      ...app,
      workloads: [app.workloads[0]],
    };
    const html = renderDetail({
      app: singleApp,
      selectedWorkloadKey: workloadKey(app.workloads[1]),
      onSelectWorkload: () => {},
    });

    expect(html).toContain("Runtime for");
    expect(html).toContain("checkout-api");
    expect(html).not.toContain("应用视图");
  });

  it("keeps application scope for a controlled multi-workload app with no workload in the URL", () => {
    const html = renderDetail({
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
    });

    expect(html).toContain("应用视图");
    expect(html).not.toContain("Runtime for");
  });

  it("renders one filtered application chronology with workload and source actions", () => {
    const html = renderDetail({
      selectedView: "history",
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyMode: 'local',
      historyRange: 'all',
      historyRangeOptions: [{ value: 'all', label: 'All observed' }],
      onOpenTimeline: () => {},
      historyItems: [
        {
          id: 'source-sync',
          category: 'deployment',
          title: 'Argo CD sync',
          timestamp: '2026-07-13T10:00:00.000Z',
          sourceRef: { type: 'gitops', tool: 'argocd', kind: 'Application', namespace: 'argocd', name: 'checkout' },
        },
        {
          id: 'runtime-change',
          category: 'change',
          title: 'Deployment updated',
          timestamp: '2026-07-13T09:00:00.000Z',
          resource: { kind: 'Deployment', namespace: 'prod', name: 'checkout-api' },
        },
        {
          id: 'problem',
          category: 'problem',
          title: 'BackOff',
          timestamp: '2026-07-13T08:00:00.000Z',
          resource: { kind: 'Pod', namespace: 'prod', name: 'checkout-api-abc12' },
        },
      ],
      onOpenSource: () => {},
    });

    expect(html).toContain("应用历史记录");
    expect(html).toContain("部署");
    expect(html).toContain("变更");
    expect(html).toContain("问题");
    expect(html).toContain("打开完整时间线");
    expect(html).toContain("Deployment");
    expect(html).toContain("prod");
    expect(html).toContain("checkout-api");
    expect(html).toContain("查看 Argo CD Application");
    expect(html).toContain("变更和问题反映此 KubeAtlas 实例保留的事件。部署来源历史记录可能覆盖不同的时间段。");
    expect(html).not.toContain("Current incidents");
  });

  it('reports retained event limits separately from recording gaps', () => {
    const html = renderDetail({
      selectedView: 'history',
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyMode: 'retained',
      historyRange: '7d',
      historyRangeOptions: [{ value: '7d', label: '7 days' }],
      historyRuntimeLimited: true,
      historyItems: [{
        id: 'runtime-change',
        category: 'change',
        title: 'Deployment updated',
        timestamp: '2026-07-13T09:00:00.000Z',
      }],
    })

    expect(html).toContain('显示这些命名空间中最近的 10,000 条事件。此范围内更早的应用活动可能未包含。')
    expect(html).not.toContain('此时间范围的部分记录覆盖不完整。')
  })

  it('preserves custom history range labels and values', () => {
    const html = renderDetail({
      selectedView: 'history',
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyMode: 'retained',
      historyRange: '7d',
      historyRangeOptions: [
        { value: 'all', label: 'All observed' },
        { value: '7d', label: 'Custom window' },
        { value: '24h', label: 'constructor' },
        { value: '30d', label: 'toString' },
      ],
      historyItems: [],
      onHistoryRangeChange: () => {},
    })

    expect(html).toContain('所有已观察记录')
    expect(html).toContain('Custom window')
    expect(html).toContain('value="all"')
    expect(html).toContain('value="7d"')
    expect(html).toMatch(/<option[^>]*value="24h"[^>]*>constructor<\/option>/)
    expect(html).toMatch(/<option[^>]*value="30d"[^>]*>toString<\/option>/)
  })

  it('reports ring truncation with its own copy, only in retained mode', () => {
    const base = {
      selectedView: 'history' as const,
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyRange: '7d' as const,
      historyRangeOptions: [{ value: '7d' as const, label: '7 days' }],
      historyRingTruncated: true,
      historyItems: [{
        id: 'ring-change',
        category: 'change' as const,
        title: 'Deployment updated',
        timestamp: '2026-07-13T09:00:00.000Z',
      }],
    }
    const retained = renderDetail({ ...base, historyMode: 'retained' })
    expect(retained).toContain('事件历史记录可能不完整：保留窗口超过大小限制，最早的活动未加载。')
    // Distinct cause, distinct copy — not the per-query 10k note.
    expect(retained).not.toContain('显示这些命名空间中最近的 10,000 条事件。')

    const local = renderDetail({ ...base, historyMode: 'local' })
    expect(local).not.toContain('保留窗口超过大小限制')
    // Local can be ring-truncated too (the binary's 10k ring) - same warning,
    // source-appropriate copy.
    expect(local).toContain('事件历史记录可能不完整：事件存储的环形缓冲区已满，最早的活动未加载。')
  })

  it('shows the host history-limit note beside the history, and nothing without one', () => {
    const base = {
      selectedView: 'history' as const,
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyMode: 'local' as const,
      historyRange: 'all' as const,
      historyRangeOptions: [{ value: 'all' as const, label: 'All observed' }],
      historyItems: [],
    }
    expect(renderDetail({ ...base, historyRetentionNote: <span>Host limit note</span> })).toContain('<span>Host limit note</span>')
    expect(renderDetail(base)).not.toContain('Host limit note')
  })

  it('shows a single honest error state when runtime history fails without source history', () => {
    const html = renderDetail({
      selectedView: 'history',
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyRuntimeError: true,
      historyItems: [],
    })

    expect(html).toContain('无法加载应用变更。')
    expect(html).not.toContain('仍显示部署来源历史记录。')
    expect(html).not.toContain('此范围内没有应用历史记录。')
  })

  it('marks retained runtime rows as stale when a refresh fails', () => {
    const html = renderDetail({
      selectedView: 'history',
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyRuntimeError: true,
      historyItems: [{
        id: 'runtime-change',
        category: 'change',
        title: 'Deployment updated',
        timestamp: '2026-07-13T09:00:00.000Z',
      }],
    })

    expect(html).toContain('无法刷新应用变更；先前加载的变更可能已过时。')
    expect(html).toContain('先前加载的变更可能已过时。')
    expect(html).toContain('Deployment updated')
  })

  it("previews the latest retained deployment change in Overview", () => {
    const healthyApp: AppRow = { ...app, events: [] };
    const history: AppHistory = {
      appKey: healthyApp.key,
      sourceRef: {
        type: "gitops",
        tool: "argocd",
        group: "argoproj.io",
        kind: "Application",
        namespace: "argocd",
        name: "checkout",
      },
      summary: {
        state: "change",
        title: "Argo CD sync",
        detail: "Succeeded · abc123",
        timestamp: "2026-07-08T12:00:00Z",
      },
      anchors: [
        {
          type: "gitops",
          title: "Argo CD sync",
          status: "Succeeded",
          revision: "abc123",
          timestamp: "2026-07-08T12:00:00Z",
        },
      ],
    };
    const html = renderDetail({
      app: healthyApp,
      history,
      onOpenSource: () => {},
    });

    expect(html).toContain('最近变更')
    expect(html).toContain('Argo CD sync')
    expect(html).toContain('历史记录')
    expect(html).toContain('查看 Argo CD Application')
    expect(html).not.toContain('No application issues detected')
  })

  it('shows an authoritative deployment source without duplicating its action in history', () => {
    const sourceRef: AppSourceRef = { type: 'gitops', tool: 'flux', group: 'kustomize.toolkit.fluxcd.io', kind: 'Kustomization', namespace: 'flux-system', name: 'checkout' }
    const sourceApp: AppRow = { ...app, events: [], sourceRef }
    const history: AppHistory = {
      appKey: sourceApp.key,
      sourceRef,
      summary: { state: 'change', title: 'Flux reconciliation', timestamp: '2026-07-08T12:00:00Z' },
    }
    const html = renderDetail({ app: sourceApp, history, onOpenSource: () => {} })

    expect(html).toContain('部署来源')
    expect(html).toContain('Flux Kustomization')
    expect(html).toContain('flux-system/checkout')
    expect(html.match(/查看 Flux Kustomization/g)).toHaveLength(1)
    expect(html).not.toContain('App key')
    expect(html).not.toContain('Confidence')
    expect(html).not.toContain('Source &amp; provenance')
  })

  it('localizes a generic GitOps source action while preserving its name', () => {
    const sourceRef: AppSourceRef = {
      type: 'gitops',
      tool: 'custom-controller',
      kind: 'GitRepository',
      namespace: 'source-system',
      name: 'checkout',
    }
    const html = renderDetail({
      app: { ...app, sourceRef },
      onOpenSource: () => {},
    })

    expect(html).toContain('查看 GitOps 来源')
    expect(html).toContain('source-system/checkout')
  })

  it('separates healthy runtime from degraded delivery and rolls up the application status', () => {
    const sourceRef: AppSourceRef = { type: 'gitops', tool: 'argocd', group: 'argoproj.io', kind: 'Application', namespace: 'argocd', name: 'checkout' }
    const html = renderDetail({
      app: {
        ...app,
        health: 'degraded',
        runtimeHealth: 'healthy',
        sourceRef,
        sourceStatus: { sync: 'Synced', health: 'Degraded' },
      },
      onOpenSource: () => {},
    })

    expect(html).toContain('Degraded')
    expect(html).toContain('运行时')
    expect(html).toContain('健康')
    expect(html).toContain('4/4 就绪')
    expect(html).toContain('交付')
    expect(html.match(/Synced/g)).toHaveLength(2)
    expect(html).toContain('降级')
    expect(html.match(/Degraded/g)).toHaveLength(2)
  })

  it('warns when application workloads resolve to different deployment sources', () => {
    const html = renderDetail({ app: { ...app, sourceConflict: true } })

    expect(html).toContain('部署来源')
    expect(html).toContain('检测到多个部署来源')
    expect(html).toContain('此应用中的工作负载未共用同一个部署管理器。')
  })

  it('distinguishes inferred application identity from declared identity', () => {
    const inferred = renderDetail({
      app: {
        ...app,
        identity: { key: 'checkout', env: 'prod', confidence: 'medium', evidence: 'name stem checkout + shared image repo', source: 'name-stem' },
      },
    })
    expect(inferred).toContain('应用身份')
    expect(inferred).toContain('根据共享名称与镜像推断应用边界')
    expect(inferred).toContain('匹配依据：共享名称与镜像。')
    expect(inferred).toContain('name stem checkout + shared image repo')

    const declared = renderDetail({
      app: {
        ...app,
        identity: { key: 'checkout', env: 'prod', confidence: 'high', evidence: 'app.skyhook.io/app=checkout', source: 'explicit' },
      },
    })
    expect(declared).toContain('应用身份由app.skyhook.io/app 注解标识')
    expect(declared).toContain('app.skyhook.io/app=checkout')
    expect(declared).not.toContain('根据共享名称与镜像推断应用边界')
  })

  it('uses tier provenance when a grouped app has no cross-app identity', () => {
    const html = renderDetail({ app: { ...app, tier: 5 } })

    expect(html).toContain('应用身份')
    expect(html).toContain('按 Helm 元数据分组')
    expect(html).not.toContain('根据 Kubernetes 所有权分组')
  })

  it('does not present completed init containers as a long-running workload state', () => {
    const statefulApp: AppRow = {
      ...app,
      workloads: [
        { ...app.workloads[0], kind: 'StatefulSet', name: 'redis-master', reason: 'Completed' },
        { ...app.workloads[1], kind: 'StatefulSet', name: 'redis-replicas', reason: 'Completed' },
      ],
    }
    const jobApp: AppRow = {
      ...app,
      workloads: [
        { ...app.workloads[0], kind: 'Job', name: 'database-migration', workload_class: 'job', reason: 'Completed' },
        { ...app.workloads[1], kind: 'CronJob', name: 'database-migration-schedule', workload_class: 'job' },
      ],
    }

    expect(renderDetail({ app: statefulApp })).not.toMatch(/redis-master<\/button><div[^>]*>Completed/)
    expect(renderDetail({ app: jobApp })).toMatch(/database-migration<\/button><div[^>]*>Completed/)
  })

  it("does not duplicate current incidents in the Overview history preview", () => {
    const history: AppHistory = {
      appKey: app.key,
      summary: {
        state: "incident",
        title: "Current incident: BackOff on Deployment/checkout-api",
        detail: "Back-off restarting failed container",
      },
      incidents: [
        {
          severity: "warning",
          title: "BackOff on Deployment/checkout-api",
          object: "Deployment/checkout-api",
        },
      ],
    };
    const html = renderDetail({ history });

    expect(html).not.toContain("BackOff on checkout-api");
    expect(html).not.toContain(
      "Current incident: BackOff on Deployment/checkout-api",
    );
    expect(html).not.toContain("最近变更");
  });

  it('previews retained incidents in Overview when no current issue card exists', () => {
    const healthyApp: AppRow = { ...app, events: [] }
    const history: AppHistory = {
      appKey: healthyApp.key,
      summary: { state: 'incident', title: 'Current incident: FailedScheduling on Pod/checkout-api-abc', detail: '0/9 nodes are available' },
      incidents: [{ severity: 'warning', title: 'FailedScheduling on Pod/checkout-api-abc', object: 'Pod/checkout-api-abc' }],
    }
    const html = renderDetail({
      app: healthyApp,
      history,
    })

    expect(html).toContain('最近问题')
    expect(html).toContain('Current incident: FailedScheduling on Pod/checkout-api-abc')
    expect(html).toContain('查看历史记录')
  })

  it('does not duplicate retained incidents when the host renders current issues', () => {
    const healthyApp: AppRow = { ...app, events: [] }
    const history: AppHistory = {
      appKey: healthyApp.key,
      summary: { state: 'incident', title: 'Current incident: FailedScheduling on Pod/checkout-api-abc' },
    }
    const html = renderDetail({
      app: healthyApp,
      history,
      renderOverviewIssues: () => <div>Operational Issues (1)</div>,
      hasOverviewIssues: true,
    })

    expect(html).toContain('Operational Issues (1)')
    expect(html).not.toContain('最近问题')
    expect(html).not.toContain('Current incident: FailedScheduling on Pod/checkout-api-abc')
  })

  it('does not report idle zero-replica workloads as application issues', () => {
    const idleApp: AppRow = {
      ...app,
      health: "healthy",
      events: [],
      workloads: [
        {
          kind: "CronJob",
          namespace: "prod",
          name: "checkout-cleanup",
          workload_class: "job",
          health: "neutral",
          ready: 0,
          desired: 0,
          restarts: 0,
          version: "1.2.3",
        },
        {
          kind: "Job",
          namespace: "prod",
          name: "checkout-cleanup-123",
          workload_class: "job",
          health: "unknown",
          ready: 0,
          desired: 0,
          restarts: 0,
          version: "1.2.3",
        },
      ],
    };
    const html = renderDetail({ app: idleApp });

    expect(html).not.toContain('No application issues detected')
    expect(html).not.toContain('needs attention')
  })

  it("uses the resource namespace when linking a history item to a workload", () => {
    const ambiguousApp: AppRow = {
      ...app,
      workloads: [
        ...app.workloads,
        {
          kind: "Deployment",
          namespace: "canary",
          name: "checkout-api",
          workload_class: "service",
          health: "healthy",
          ready: 1,
          desired: 1,
          restarts: 0,
          version: "1.2.3",
        },
      ],
    };
    const html = renderDetail({
      app: ambiguousApp,
      selectedView: "history",
      onSelectView: () => {},
      selectedWorkloadKey: null,
      onSelectWorkload: () => {},
      historyItems: [{
        id: 'prod-change',
        category: 'change',
        title: 'Deployment updated',
        timestamp: '2026-07-13T09:00:00.000Z',
        resource: { kind: 'Deployment', namespace: 'prod', name: 'checkout-api' },
      }],
    });

    expect(html).toContain("Deployment");
    expect(html).toContain("prod");
    expect(html).toContain("checkout-api");
    expect(html).not.toContain("canary/checkout-api");
  });

  it("shows batch activity and injected operational issues on the application overview", () => {
    const batchApp: AppRow = {
      key: "app:prod:batch",
      name: "batch",
      namespace: "prod",
      health: "unhealthy",
      workload_class: "job",
      workloads: [
        {
          kind: "CronJob",
          namespace: "prod",
          name: "nightly",
          workload_class: "job",
          health: "healthy",
          ready: 0,
          desired: 0,
          restarts: 0,
          batch: {
            schedule: "0 3 * * *",
            retainedRuns: 3,
            succeededRuns: 3,
            failedRuns: 0,
            activeRuns: 0,
            latestRunName: "nightly-29720340",
            latestRunPhase: "Succeeded",
            latestStartedAt: "2026-07-05T10:25:00Z",
            lastSuccessfulAt: "2026-07-05T10:29:00Z",
          },
        },
        {
          kind: "Job",
          namespace: "prod",
          name: "ad-hoc-import",
          workload_class: "job",
          health: "unhealthy",
          ready: 0,
          desired: 1,
          restarts: 0,
          batch: {
            retainedRuns: 1,
            succeededRuns: 0,
            failedRuns: 1,
            activeRuns: 0,
            latestRunName: "ad-hoc-import",
            latestRunPhase: "Failed",
            latestStartedAt: "2026-07-05T09:25:00Z",
            message: "Job has reached the specified backoff limit",
          },
        },
      ],
    };
    const html = renderDetail({
      app: batchApp,
      renderOverviewIssues: () => <div>Operational Issues (1)</div>,
    });

    expect(html).toContain("应用视图");
    expect(html).toContain("Operational Issues (1)");
    expect(html).not.toContain("批处理活动");
    const workloadsPanel = html.indexOf(">工作负载</h2>");
    expect(workloadsPanel).toBeGreaterThanOrEqual(0);
    expect(html).toContain("批处理资源");
    expect(html).toContain("活动运行");
    expect(html).toContain("保留的运行");
    expect(html).toContain("1 CronJob / 1 Job");
    expect(html).toContain("3 次成功 / 1 次失败");
    expect(html).toContain("Succeeded");
    expect(html).toContain("nightly-29720340");
    expect(html).toContain("ad-hoc-import");
    expect(html).not.toContain("ad-hoc-import不可用");
    expect(html).not.toContain("Runtime for");
    expect(html).toContain('2xl:grid-cols-4');
  });

  it("localizes generated relative time while preserving the run name and phase", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-05T10:30:00.000Z"));
    try {
      const batchApp: AppRow = {
        ...app,
        workloads: [
          {
            ...app.workloads[0],
            kind: "Job",
            workload_class: "job",
            batch: {
              retainedRuns: 1,
              succeededRuns: 1,
              failedRuns: 0,
              activeRuns: 0,
              latestRunName: "nightly-run-01",
              latestRunPhase: "Succeeded",
              latestStartedAt: "2026-07-05T10:25:00.000Z",
            },
          },
          app.workloads[1],
        ],
      };
      const html = renderDetail({ app: batchApp });

      expect(html).toContain("nightly-run-01 · 5分钟前");
      expect(html).toContain("Succeeded");
    } finally {
      vi.useRealTimers();
    }
  });

  it("handles missing and unparseable run timestamps without changing raw values", () => {
    const batchApp = (latestStartedAt?: string): AppRow => ({
      ...app,
      workloads: [
        {
          ...app.workloads[0],
          kind: "Job",
          workload_class: "job",
          batch: {
            retainedRuns: 1,
            succeededRuns: 0,
            failedRuns: 1,
            activeRuns: 0,
            latestRunName: "nightly-run-invalid-time",
            latestRunPhase: "Failed",
            ...(latestStartedAt ? { latestStartedAt } : {}),
          },
        },
        app.workloads[1],
      ],
    });

    const missing = renderDetail({ app: batchApp() });
    expect(missing).toContain("nightly-run-invalid-time");
    expect(missing).not.toContain("nightly-run-invalid-time ·");
    expect(missing).toContain("Failed");

    const invalid = renderDetail({ app: batchApp("not-a-timestamp") });
    expect(invalid).toContain("nightly-run-invalid-time · not-a-timestamp");
    expect(invalid).toContain("Failed");
  });

  it("uses the delivery verdict in a successful batch application header", () => {
    const batchApp: AppRow = {
      key: "app:prod:batch",
      name: "batch",
      namespace: "prod",
      health: "unhealthy",
      runtimeHealth: "unhealthy",
      workload_class: "job",
      sourceStatus: { sync: "Synced", health: "Degraded" },
      workloads: [
        {
          kind: "CronJob",
          namespace: "prod",
          name: "nightly",
          workload_class: "job",
          health: "unhealthy",
          ready: 0,
          desired: 0,
          restarts: 0,
          batch: {
            retainedRuns: 2,
            succeededRuns: 1,
            failedRuns: 1,
            activeRuns: 0,
            latestRunPhase: "Succeeded",
          },
        },
      ],
    };
    const html = renderDetail({ app: batchApp });

    expect(html).toMatch(/<span class="text-sm font-semibold">降级<\/span>/);
    expect(html).toContain("Succeeded");
  });
});

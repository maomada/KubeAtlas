import { AlertTriangle, Bot, ClipboardCheck, GitBranch, Server, ShieldCheck } from 'lucide-react'
import { Badge } from '@maomada/k8s-ui'
import { useLocale } from '@maomada/k8s-ui/hooks/useLocale'
import type { ReactNode } from 'react'
import type { DashboardResponse, IssuesResponse } from '../api/client'
import { cloudErrorMessage, clusterPage, type HubCluster } from './cloudApi'
import { issueCoverageComplete, useFleetQuery, type ClusterResult } from './cloudFleet'
import { CloudError, CloudLink, CloudLoading, ClusterStatus } from './CloudShared'

export function FleetCoverage<T>({ results }: { results?: ClusterResult<T>[] }) {
  const { t } = useLocale()
  const failed = results?.filter(result => result.error) ?? []
  return failed.length > 0 && <div className="cloud-coverage" role="status">
    <AlertTriangle size={16} />
    <div>
      <strong>{t('集群数据覆盖不完整', 'Incomplete cluster coverage')}</strong>
      <p>{failed.map(result => `${result.cluster.name}: ${cloudErrorMessage(result.error, t)}`).join(' · ')}</p>
      <p>{t('无法访问的集群不会被计为健康或无数据。', 'Unreachable clusters are excluded from health and empty-state counts.')}</p>
    </div>
  </div>
}

export function CloudHome({ clusters, search }: { clusters: HubCluster[]; search: ReactNode }) {
  const { t } = useLocale()
  const dashboard = useFleetQuery<DashboardResponse>(clusters, '/dashboard')
  const issues = useFleetQuery<IssuesResponse>(clusters, '/issues')
  const dashboards = dashboard.data?.flatMap(result => result.data ? [result.data] : []) ?? []
  const checks = dashboards.flatMap(data => data.audit ? [data.audit] : [])
  const findings = checks.reduce((total, check) => total + check.warning + check.danger, 0)
  const passing = checks.reduce((total, check) => total + check.passing, 0)
  const certs = dashboards.flatMap(data => data.certificateHealth ? [data.certificateHealth] : [])
  const expiring = certs.reduce((total, cert) => total + cert.warning + cert.critical + cert.expired, 0)
  const visibleIssues = issues.data?.flatMap(result => result.data?.issues ?? []) ?? []
  const issuesComplete = clusters.length > 0 && !issues.isFetching && !issues.error && issues.data?.length === clusters.length && issues.data.every(result => result.data && !result.error && issueCoverageComplete(result.data))
  const dashboardsComplete = clusters.length > 0 && !dashboard.isFetching && !dashboard.error && dashboards.length === clusters.length && !dashboards.some(data => !data.cluster?.connected || data.accessRestricted || data.partialData?.length || data.deferredLoading)
  const healthComplete = issuesComplete && dashboardsComplete
  const restricted = dashboard.data?.filter(result => result.data?.accessRestricted) ?? []
  const restrictedNames = restricted.map(result => result.cluster.name).join(', ')
  const connected = clusters.filter(cluster => cluster.state === 'connected').length
  const controllers = dashboards.reduce((total, data) => total + (data.gitopsControllers?.controllers.length ?? 0), 0)
  return <div className="cloud-page cloud-home">
    <section className="cloud-home-search">
      {search}
      <p>{t('Pods、deployments、namespaces、secrets — 搜索所有集群 · 按 ', 'Pods, deployments, namespaces, secrets across all clusters · Press ')}<kbd>/</kbd>{t(' 聚焦', ' to focus')}</p>
    </section>
    {dashboard.error && <CloudError error={dashboard.error} retry={() => { void dashboard.refetch() }} />}
    <FleetCoverage results={dashboard.data} />
    {restricted.length > 0 && <div className="cloud-coverage" role="status">
      {t(
        `无法读取 ${restrictedNames} 的命名空间。空结果不能证明集群健康；请检查 Kubernetes 权限。`,
        `Cannot read namespaces in ${restrictedNames}. Check Kubernetes permissions before assessing cluster health.`,
      )}
    </div>}
    <div className="cloud-home-grid">
      <div className="cloud-home-left">
        <section className="cloud-card cloud-cluster-summary">
          <header><h2><Server size={16} />{t('集群', 'Clusters')}</h2><CloudLink href="/clusters">{t(`查看全部 ${clusters.length} 个 →`, `View all ${clusters.length} →`)}</CloudLink></header>
          <div className="cloud-cluster-summary-body">
            <div className="cloud-connection-count"><span>{connected}/{clusters.length}</span><small>{t('已连接', 'Connected')}</small></div>
            <div className="cloud-cluster-mini-list">
              {clusters.map(cluster => <CloudLink href={clusterPage(cluster.id)} className="cloud-cluster-mini" key={cluster.id}>
                <strong><Server size={14} />{cluster.name}</strong><ClusterStatus cluster={cluster} />
              </CloudLink>)}
              {!clusters.length && <CloudLink href="/install">{t('连接首个集群 →', 'Connect your first cluster →')}</CloudLink>}
            </div>
          </div>
        </section>
        <div className="cloud-home-metrics">
          <CloudLink href="/checks" className="cloud-card">
            <h2><ClipboardCheck size={15} />{t('检查', 'Checks')}</h2>
            {dashboard.isPending ? <CloudLoading /> : checks.length ? <>
              <div className="cloud-big-number">{findings}</div>
              <p>{t('项检查发现 ', findings === 1 ? 'check finding ' : 'check findings ')}{!dashboardsComplete && t('（部分数据）', '(partial data)')}</p>
              <small>{t(`${passing} / ${passing + findings} 项通过`, `${passing} of ${passing + findings} passed`)}{checks.some(check => check.missingInputs?.length) && t(' · 缺少输入', ' · Missing inputs')}</small>
            </> : <p>{t('检查数据不可用', 'Check data unavailable')}</p>}
          </CloudLink>
          <CloudLink href="/certs" className="cloud-card">
            <h2><ShieldCheck size={15} />{t('证书', 'Certificates')}</h2>
            <p>{dashboard.isPending ? t('加载中…', 'Loading…')
              : !certs.length ? t('证书数据不可用', 'Certificate data unavailable')
              : expiring ? t(`${expiring} 项需要关注`, `${expiring} need attention`)
              : dashboardsComplete && certs.length === clusters.length ? t('未来 30 天内无证书到期', 'No certificates expire in the next 30 days')
              : t('返回数据中无到期预警；覆盖范围不完整', 'No expiry alerts in the available data. Coverage is incomplete.')}</p>
          </CloudLink>
          <CloudLink href="/gitops" className="cloud-card">
            <h2><GitBranch size={15} />GitOps</h2>
            <p>{dashboard.isPending ? t('加载中…', 'Loading…')
              : controllers ? t(`检测到 ${controllers} 个控制器`, `${controllers} controller${controllers === 1 ? '' : 's'} detected`)
              : dashboardsComplete ? t('未检测到 Argo 或 Flux 控制器', 'No Argo or Flux controllers detected')
              : t('GitOps 覆盖范围不可用或不完整', 'GitOps coverage is unavailable or incomplete')}</p>
          </CloudLink>
        </div>
        <CloudLink href="/agents" className="cloud-card cloud-agent-card">
          <header><h2><Bot size={16} />AI Agent · MCP</h2><span>{t('连接 →', 'Connect →')}</span></header>
          <strong>{t('连接 AI Agent', 'Connect an AI agent')}</strong>
          <p>{t('使用独立、限定范围且有有效期的 MCP Token。Kubernetes 读写操作均以你的 Hub 身份执行。', 'Use a dedicated, scoped MCP token. Kubernetes operations run under your Hub identity.')}</p>
          <Badge severity="info">Streamable HTTP</Badge>
        </CloudLink>
      </div>
      <section className="cloud-card cloud-active-issues">
        <header><h2><AlertTriangle size={16} />{t('当前问题', 'Current issues')}</h2><CloudLink href="/issues">{t('查看全部 →', 'View all →')}</CloudLink></header>
        <FleetCoverage results={issues.data} />
        {issues.isPending ? <CloudLoading /> : issues.error ? <CloudError error={issues.error} /> : visibleIssues.length ? <div className="cloud-home-issue-list">
          {visibleIssues.slice(0, 8).map((issue, index) => <CloudLink href="/issues" key={`${issue.id}:${index}`}>
            <AlertTriangle size={16} /><div><strong>{issue.name}</strong><p>{issue.message}</p></div><Badge severity="warning">{issue.severity}</Badge>
          </CloudLink>)}
        </div> : <div className={`cloud-health-message ${healthComplete ? 'is-healthy' : ''}`}>
          <strong>{healthComplete ? t('集群运行正常', 'Clusters are healthy') : clusters.length ? t('集群健康状态尚不完整', 'Cluster health data is incomplete') : t('暂无已连接集群', 'No connected clusters')}</strong>
          <p>{healthComplete ? t('当前可见集群范围内没有实时问题。', 'No active issues in the clusters you can access.') : t('请连接所有已选集群并处理数据覆盖警告；在此之前，空队列不能视为健康。', 'Connect the selected clusters and resolve coverage warnings to assess their health.')}</p>
        </div>}
      </section>
    </div>
  </div>
}

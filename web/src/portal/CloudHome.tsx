import { AlertTriangle, Bot, ClipboardCheck, GitBranch, Server, ShieldCheck } from 'lucide-react'
import { Badge } from '@maomada/k8s-ui'
import type { ReactNode } from 'react'
import type { DashboardResponse, IssuesResponse } from '../api/client'
import { clusterPage, type HubCluster } from './cloudApi'
import { issueCoverageComplete, useFleetQuery, type ClusterResult } from './cloudFleet'
import { CloudError, CloudLink, CloudLoading, ClusterStatus } from './CloudShared'

export function FleetCoverage<T>({ results }: { results?: ClusterResult<T>[] }) {
  const failed = results?.filter(result => result.error) ?? []
  return failed.length > 0 && <div className="cloud-coverage" role="status"><AlertTriangle size={16} /><div><strong>集群数据覆盖不完整</strong><p>{failed.map(result => `${result.cluster.name}: ${result.error?.message}`).join(' · ')}</p><p>无法访问的集群不会被计为健康或无数据。</p></div></div>
}

export function CloudHome({ clusters, search }: { clusters: HubCluster[]; search: ReactNode }) {
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
  return <div className="cloud-page cloud-home"><section className="cloud-home-search">{search}<p>Pods、deployments、namespaces、secrets — 搜索所有集群 · 按 <kbd>/</kbd> 聚焦</p></section>
    {dashboard.error && <CloudError error={dashboard.error} retry={() => { void dashboard.refetch() }} />}
    <FleetCoverage results={dashboard.data} />
    {restricted.length > 0 && <div className="cloud-coverage" role="status">无法读取 {restricted.map(result => result.cluster.name).join(', ')} 的命名空间。空结果不能证明集群健康；请检查 Kubernetes 权限。</div>}
    <div className="cloud-home-grid"><div className="cloud-home-left">
      <section className="cloud-card cloud-cluster-summary"><header><h2><Server size={16} />集群</h2><CloudLink href="/clusters">查看全部 {clusters.length} 个 →</CloudLink></header><div className="cloud-cluster-summary-body"><div className="cloud-connection-count"><span>{clusters.filter(cluster => cluster.state === 'connected').length}/{clusters.length}</span><small>已连接</small></div><div className="cloud-cluster-mini-list">{clusters.map(cluster => <CloudLink href={clusterPage(cluster.id)} className="cloud-cluster-mini" key={cluster.id}><strong><Server size={14} />{cluster.name}</strong><ClusterStatus cluster={cluster} /></CloudLink>)}{!clusters.length && <CloudLink href="/install">连接首个集群 →</CloudLink>}</div></div></section>
      <div className="cloud-home-metrics"><CloudLink href="/checks" className="cloud-card"><h2><ClipboardCheck size={15} />检查</h2>{dashboard.isPending ? <CloudLoading /> : checks.length ? <><div className="cloud-big-number">{findings}</div><p>项检查发现 {!dashboardsComplete && '（部分数据）'}</p><small>{passing} / {passing + findings} 项通过{checks.some(check => check.missingInputs?.length) && ' · 缺少输入'}</small></> : <p>检查数据不可用</p>}</CloudLink>
        <CloudLink href="/certs" className="cloud-card"><h2><ShieldCheck size={15} />证书</h2><p>{dashboard.isPending ? '加载中…' : !certs.length ? '证书数据不可用' : expiring ? `${expiring} 项需要关注` : dashboardsComplete && certs.length === clusters.length ? '未来 30 天内无证书到期' : '返回数据中无到期预警；覆盖范围不完整'}</p></CloudLink>
        <CloudLink href="/gitops" className="cloud-card"><h2><GitBranch size={15} />GitOps</h2><p>{dashboard.isPending ? '加载中…' : dashboards.some(data => data.gitopsControllers?.controllers.length) ? `检测到 ${dashboards.reduce((total, data) => total + (data.gitopsControllers?.controllers.length ?? 0), 0)} 个控制器` : dashboardsComplete ? '未检测到 Argo 或 Flux 控制器' : 'GitOps 覆盖范围不可用或不完整'}</p></CloudLink></div>
      <CloudLink href="/agents" className="cloud-card cloud-agent-card"><header><h2><Bot size={16} />AI Agent · MCP</h2><span>连接 →</span></header><strong>连接 AI Agent</strong><p>使用独立、限定范围且有有效期的 MCP Token。Kubernetes 读写操作均以你的 Hub 身份执行。</p><Badge severity="info">Streamable HTTP</Badge></CloudLink>
    </div><section className="cloud-card cloud-active-issues"><header><h2><AlertTriangle size={16} />当前问题</h2><CloudLink href="/issues">查看全部 →</CloudLink></header><FleetCoverage results={issues.data} />{issues.isPending ? <CloudLoading /> : issues.error ? <CloudError error={issues.error} /> : visibleIssues.length ? <div className="cloud-home-issue-list">{visibleIssues.slice(0, 8).map((issue, index) => <CloudLink href="/issues" key={`${issue.id}:${index}`}><AlertTriangle size={16} /><div><strong>{issue.name}</strong><p>{issue.message}</p></div><Badge severity="warning">{issue.severity}</Badge></CloudLink>)}</div> : <div className={`cloud-health-message ${healthComplete ? 'is-healthy' : ''}`}><strong>{healthComplete ? '集群运行正常' : clusters.length ? '集群健康状态尚不完整' : '暂无已连接集群'}</strong><p>{healthComplete ? '当前可见集群范围内没有实时问题。' : '请连接所有已选集群并处理数据覆盖警告；在此之前，空队列不能视为健康。'}</p></div>}</section></div>
  </div>
}

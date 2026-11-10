import { AlertTriangle, Bot, ClipboardCheck, GitBranch, Server, ShieldCheck } from 'lucide-react'
import { Badge } from '@maomada/k8s-ui'
import type { ReactNode } from 'react'
import type { DashboardResponse, IssuesResponse } from '../api/client'
import { clusterPage, type HubCluster } from './cloudApi'
import { issueCoverageComplete, useFleetQuery, type ClusterResult } from './cloudFleet'
import { CloudError, CloudLink, CloudLoading, ClusterStatus } from './CloudShared'

export function FleetCoverage<T>({ results }: { results?: ClusterResult<T>[] }) {
  const failed = results?.filter(result => result.error) ?? []
  return failed.length > 0 && <div className="cloud-coverage" role="status"><AlertTriangle size={16} /><div><strong>Partial fleet coverage</strong><p>{failed.map(result => `${result.cluster.name}: ${result.error?.message}`).join(' · ')}</p><p>Unavailable clusters are not counted as healthy or empty.</p></div></div>
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
  return <div className="cloud-page cloud-home"><section className="cloud-home-search">{search}<p>Pods, deployments, namespaces, secrets — across all your clusters · <kbd>/</kbd> to focus</p></section>
    {dashboard.error && <CloudError error={dashboard.error} retry={() => { void dashboard.refetch() }} />}
    <FleetCoverage results={dashboard.data} />
    {restricted.length > 0 && <div className="cloud-coverage" role="status">Namespace access is unavailable in {restricted.map(result => result.cluster.name).join(', ')}. Empty results do not establish health; check Kubernetes permissions.</div>}
    <div className="cloud-home-grid"><div className="cloud-home-left">
      <section className="cloud-card cloud-cluster-summary"><header><h2><Server size={16} />Clusters</h2><CloudLink href="/clusters">View all {clusters.length} →</CloudLink></header><div className="cloud-cluster-summary-body"><div className="cloud-connection-count"><span>{clusters.filter(cluster => cluster.state === 'connected').length}/{clusters.length}</span><small>connected</small></div><div className="cloud-cluster-mini-list">{clusters.map(cluster => <CloudLink href={clusterPage(cluster.id)} className="cloud-cluster-mini" key={cluster.id}><strong><Server size={14} />{cluster.name}</strong><ClusterStatus cluster={cluster} /></CloudLink>)}{!clusters.length && <CloudLink href="/install">Connect your first cluster →</CloudLink>}</div></div></section>
      <div className="cloud-home-metrics"><CloudLink href="/checks" className="cloud-card"><h2><ClipboardCheck size={15} />Checks</h2>{dashboard.isPending ? <CloudLoading /> : checks.length ? <><div className="cloud-big-number">{findings}</div><p>findings {!dashboardsComplete && '(partial)'}</p><small>{passing} of {passing + findings} passing{checks.some(check => check.missingInputs?.length) && ' · inputs missing'}</small></> : <p>Check data unavailable</p>}</CloudLink>
        <CloudLink href="/certs" className="cloud-card"><h2><ShieldCheck size={15} />Certificates</h2><p>{dashboard.isPending ? 'Loading…' : !certs.length ? 'Certificate data unavailable' : expiring ? `${expiring} need attention` : dashboardsComplete && certs.length === clusters.length ? 'Nothing expiring in 30 days' : 'No expiry warnings in returned data; coverage incomplete'}</p></CloudLink>
        <CloudLink href="/gitops" className="cloud-card"><h2><GitBranch size={15} />GitOps</h2><p>{dashboard.isPending ? 'Loading…' : dashboards.some(data => data.gitopsControllers?.controllers.length) ? `${dashboards.reduce((total, data) => total + (data.gitopsControllers?.controllers.length ?? 0), 0)} controllers detected` : dashboardsComplete ? 'No Argo or Flux controllers detected' : 'GitOps coverage unavailable or incomplete'}</p></CloudLink></div>
      <CloudLink href="/agents" className="cloud-card cloud-agent-card"><header><h2><Bot size={16} />AI agents · MCP</h2><span>Connect →</span></header><strong>Connect an AI agent</strong><p>Use an independent, scoped, expiring MCP token. Kubernetes reads and writes run as your Hub identity.</p><Badge severity="info">Streamable HTTP</Badge></CloudLink>
    </div><section className="cloud-card cloud-active-issues"><header><h2><AlertTriangle size={16} />Active issues</h2><CloudLink href="/issues">View all →</CloudLink></header><FleetCoverage results={issues.data} />{issues.isPending ? <CloudLoading /> : issues.error ? <CloudError error={issues.error} /> : visibleIssues.length ? <div className="cloud-home-issue-list">{visibleIssues.slice(0, 8).map((issue, index) => <CloudLink href="/issues" key={`${issue.id}:${index}`}><AlertTriangle size={16} /><div><strong>{issue.name}</strong><p>{issue.message}</p></div><Badge severity="warning">{issue.severity}</Badge></CloudLink>)}</div> : <div className={`cloud-health-message ${healthComplete ? 'is-healthy' : ''}`}><strong>{healthComplete ? 'Fleet is healthy' : clusters.length ? 'Health is not fully known' : 'No clusters connected'}</strong><p>{healthComplete ? 'No live issues in your visible fleet scope right now.' : 'Connect all selected clusters and resolve coverage warnings before treating an empty queue as healthy.'}</p></div>}</section></div>
  </div>
}

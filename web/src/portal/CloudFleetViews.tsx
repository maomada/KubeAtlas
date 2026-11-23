import { useMemo, useState } from 'react'
import { ApplicationsView, Badge, ChecksView, IssuesView, type AppRow, type IssueResourceRef } from '@maomada/k8s-ui'
import type { AuditResponse, IssuesResponse } from '../api/client'
import { kindToPluralWithGroup } from '../utils/navigation'
import type { HubCluster } from './cloudApi'
import { clusterPage } from './cloudApi'
import { buildFleetApplications, type ApplicationTarget } from './cloudApplications'
import { issueCoverageComplete, useFleetQuery } from './cloudFleet'
import { FleetCoverage } from './CloudHome'
import { CloudDialog, CloudError, CloudLink, CloudLoading } from './CloudShared'
import { navigateCloud } from './cloudNavigation'
import { useLocale } from '@maomada/k8s-ui/hooks/useLocale'

export function resourceHref(ref: IssueResourceRef) {
  if (!ref.cluster_id) return '/clusters'
  const query = new URLSearchParams({ resource: ref.namespace ? `${ref.namespace}/${ref.name}` : ref.name })
  if (ref.group) query.set('apiGroup', ref.group)
  return clusterPage(ref.cluster_id, `/resources/${kindToPluralWithGroup(ref.kind, ref.group ?? '')}?${query}`)
}

export function CloudApplications({ clusters }: { clusters: HubCluster[] }) {
  const { t } = useLocale()
  const query = useFleetQuery<{ applications: AppRow[] }>(clusters, '/applications')
  const { targets, entries } = useMemo(() => buildFleetApplications(query.data ?? []), [query.data])
  const [destinations, setDestinations] = useState<ApplicationTarget[]>()
  function select(key: string) {
    const options = targets.get(key)
    if (options?.length === 1) navigateCloud(options[0].href)
    else if (options?.length) setDestinations(options)
  }
  return <div className="cloud-fleet-pane">
    <FleetCoverage results={query.data} />
    {query.error && <CloudError error={query.error} retry={() => { void query.refetch() }} />}
    <ApplicationsView entries={entries} variant="fleet" loading={query.isPending} onSelect={select}
      title={t('应用', 'Applications')}
      description={t('跨集群部署的软件；依据应用与 Helm release 证据，将服务、工作进程和任务分组。', 'Services, workers, and jobs across clusters, grouped using application and Helm release data.')}
      emptySlot={<div className="cloud-empty"><h2>{t('未返回应用数据', 'No application data returned')}</h2><p>{t('请检查集群连接和资源权限。', 'Check your cluster connections and resource permissions.')}</p></div>}
    />
    {destinations && <CloudDialog title={t('选择应用实例', 'Select an application instance')} onClose={() => setDestinations(undefined)}>
      <div className="cloud-form">{destinations.map(target => <CloudLink className="cloud-button" key={target.clusterId} href={target.href}>{target.clusterName} →</CloudLink>)}</div>
    </CloudDialog>}
  </div>
}

export function CloudIssues({ clusters }: { clusters: HubCluster[] }) {
  const { t } = useLocale()
  const query = useFleetQuery<IssuesResponse>(clusters, '/issues')
  const [search, setSearch] = useState('')
  const [severity, setSeverity] = useState('')
  const [namespace, setNamespace] = useState('')
  const allIssues = useMemo(() => query.data?.flatMap(result => (result.data?.issues ?? []).map(issue => ({ ...issue, cluster_id: result.cluster.id }))) ?? [], [query.data])
  const issues = allIssues.filter(issue => (!severity || issue.severity === severity) && (!namespace || issue.namespace === namespace) && [issue.name, issue.namespace, issue.kind, issue.message].join(' ').toLowerCase().includes(search.toLowerCase()))
  const incomplete = query.data?.filter(result => result.data && !issueCoverageComplete(result.data)) ?? []
  const anyData = !!query.data?.some(result => result.data)
  const complete = clusters.length > 0 && !query.isFetching && !query.error && query.data?.length === clusters.length && query.data.every(result => result.data && !result.error) && !incomplete.length
  const incompleteNames = incomplete.map(result => result.cluster.name).join(t('、', ', '))
  return <div className="cloud-page">
    <div className="cloud-page-heading"><div>
      <h1>{t('问题', 'Issues')}</h1>
      <p>{t('汇总所选集群中当前需要处理的问题，便于统一排查。', 'Review active issues across the selected clusters.')}</p>
    </div></div>
    <FleetCoverage results={query.data} />
    {query.error && <CloudError error={query.error} />}
    {incomplete.length > 0 && <p role="status" className="cloud-coverage">{t(
      `以下集群的问题来源不完整或已截断：${incompleteNames}。队列为空并不代表全部正常。`,
      `Issue data is incomplete or truncated for ${incompleteNames}. Resolve coverage warnings to assess these clusters.`,
    )}</p>}
    <div className="cloud-filters">
      <input type="search" aria-label={t('搜索问题', 'Search issues')} placeholder={t('搜索问题…', 'Search issues…')} value={search} onChange={event => setSearch(event.target.value)} />
      <select aria-label={t('严重程度', 'Severity')} value={severity} onChange={event => setSeverity(event.target.value)}>
        <option value="">{t('严重程度：任意', 'Any severity')}</option>
        <option value="critical">{t('严重', 'Critical')}</option>
        <option value="warning">{t('警告', 'Warning')}</option>
      </select>
      <select aria-label={t('命名空间', 'Namespace')} value={namespace} onChange={event => setNamespace(event.target.value)}>
        <option value="">{t('命名空间：任意', 'All namespaces')}</option>
        {Array.from(new Set(allIssues.map(issue => issue.namespace))).sort().filter(Boolean).map(value => <option key={value}>{value}</option>)}
      </select>
    </div>
    {query.isPending ? <CloudLoading /> : issues.length ? <IssuesView issues={issues} anyData={anyData} onResourceClick={ref => navigateCloud(resourceHref(ref))} resourceHref={resourceHref} clusterLabel={issue => clusters.find(cluster => cluster.id === issue.cluster_id)?.name} />
      : search || severity || namespace ? <div className="cloud-empty">
        <h2>{t('没有符合筛选条件的问题', 'No issues match these filters')}</h2>
        <button className="cloud-button" onClick={() => { setSearch(''); setSeverity(''); setNamespace('') }}>{t('清除筛选', 'Clear filters')}</button>
      </div> : complete ? <IssuesView issues={[]} anyData /> : <div className="cloud-empty">
        <h2>{t('问题可见性不完整', 'Incomplete issue visibility')}</h2>
        <p>{t('可用来源未返回问题数据。请先处理覆盖范围警告，再判断集群是否健康。', 'Available sources returned no issues. Resolve the coverage warnings to assess cluster health.')}</p>
      </div>}
  </div>
}

export function CloudChecks({ clusters }: { clusters: HubCluster[] }) {
  const { t } = useLocale()
  const query = useFleetQuery<AuditResponse>(clusters, '/audit')
  const rows = query.data ?? []
  const checks = rows.flatMap(result => (result.data?.groupedChecks ?? []).map(check => ({ ...check, subject: { ...check.subject, cluster_id: result.cluster.id }, representativeFinding: { ...check.representativeFinding, resource: { ...check.representativeFinding.resource, cluster_id: result.cluster.id } }, findings: check.findings.map(finding => ({ ...finding, resource: { ...finding.resource, cluster_id: result.cluster.id } })), environment: result.cluster.environment })))
  const catalog = Object.assign({}, ...rows.flatMap(result => result.data ? [result.data.checks] : []))
  const missing = rows.flatMap(({ data, cluster }) => {
    if (!data) return [t(`${cluster.name}：不可用`, `${cluster.name}: unavailable`)]
    const inputs = data.missingInputs ?? []
    return data.groupedChecks ? inputs : [...inputs, t(`${cluster.name}：检查分组不可用`, `${cluster.name}: grouped checks unavailable`)]
  })
  return <div className="cloud-page">
    <div className="cloud-page-heading"><div>
      <h1>{t('检查', 'Checks')}</h1>
      <p>{t('汇总所选集群的安全性、可靠性和效率最佳实践检查，形成修复队列。', 'Review security, reliability, and efficiency checks across the selected clusters.')}</p>
    </div></div>
    <FleetCoverage results={rows} />
    {query.error && <CloudError error={query.error} />}
    {query.isPending ? <CloudLoading /> : <ChecksView checks={checks} catalog={catalog} anyData={rows.some(result => result.data?.groupedChecks !== undefined)} evaluated={rows.reduce((sum, result) => sum + (result.data ? result.data.summary.passing + result.data.summary.warning + result.data.summary.danger : 0), 0)} missingInputs={missing} resourceHref={resourceHref} onResourceClick={ref => navigateCloud(resourceHref(ref))} clusterLabel={check => clusters.find(cluster => cluster.id === check.subject.cluster_id)?.name} clusterLabelById={id => clusters.find(cluster => cluster.id === id)?.name} />}
  </div>
}

interface Certificate {
  name: string; namespace: string; issuer: string; domains: string; expiry?: string; daysLeft?: number
  health: 'healthy' | 'degraded' | 'unhealthy' | 'unknown'; source: 'cert-manager' | 'tls-secret'
}

export function CloudCertificates({ clusters }: { clusters: HubCluster[] }) {
  const { t } = useLocale()
  const query = useFleetQuery<Certificate[]>(clusters, '/certificates')
  const [search, setSearch] = useState('')
  const [health, setHealth] = useState('')
  const certificates = (query.data ?? []).flatMap(result => (result.data ?? []).map(cert => ({ ...cert, cluster: result.cluster }))).sort((a, b) => (a.daysLeft ?? Infinity) - (b.daysLeft ?? Infinity))
  const filtered = certificates.filter(cert => (!health || cert.health === health) && [cert.name, cert.namespace, cert.issuer, cert.domains, cert.cluster.name].join(' ').toLowerCase().includes(search.toLowerCase()))
  const healthLabels = {
    unhealthy: t('已过期/即将到期', 'Expired or expiring soon'),
    degraded: t('本月到期', 'Expiring this month'),
    healthy: t('健康', 'Healthy'),
    unknown: t('未知', 'Unknown'),
  }
  return <div className="cloud-page">
    <div className="cloud-page-heading"><div>
      <h1>{t('证书', 'Certificates')}</h1>
      <p>{t('当前身份有权读取的证书元数据，包含 cert-manager Certificate 和 TLS Secret，按最近到期时间排序。', 'Accessible cert-manager Certificates and TLS Secrets, sorted by earliest expiry.')}</p>
    </div></div>
    <FleetCoverage results={query.data} />
    {query.error && <CloudError error={query.error} />}
    <div className="cloud-filters">{(['unhealthy', 'degraded', 'healthy', 'unknown'] as const).map(state =>
      <button key={state} className="cloud-button" aria-pressed={health === state} onClick={() => setHealth(health === state ? '' : state)}>
        {certificates.filter(cert => cert.health === state).length} {healthLabels[state]}
      </button>,
    )}</div>
    <div className="cloud-filters">
      <input type="search" aria-label={t('搜索证书', 'Search certificates')} placeholder={t('按名称、命名空间、域名或签发方搜索证书…', 'Search by name, namespace, domain, or issuer…')} value={search} onChange={event => setSearch(event.target.value)} />
    </div>
    {query.isPending ? <CloudLoading /> : filtered.length ? <div className="cloud-table-panel cloud-table-scroll">
      <table className="cloud-table">
        <thead><tr>
          <th>{t('证书', 'Certificate')}</th><th>{t('集群', 'Cluster')}</th><th>{t('命名空间', 'Namespace')}</th>
          <th>{t('域名', 'Domains')}</th><th>{t('签发方', 'Issuer')}</th><th>{t('到期时间', 'Expiry')}</th>
        </tr></thead>
        <tbody>{filtered.map(cert => <tr key={`${cert.cluster.id}:${cert.namespace}:${cert.source}:${cert.name}`}>
          <td><CloudLink href={resourceHref({ cluster_id: cert.cluster.id, kind: cert.source === 'tls-secret' ? 'Secret' : 'Certificate', group: cert.source === 'tls-secret' ? '' : 'cert-manager.io', namespace: cert.namespace, name: cert.name })}>{cert.name}</CloudLink></td>
          <td>{cert.cluster.name}</td><td>{cert.namespace}</td><td>{cert.domains}</td><td>{cert.issuer}</td>
          <td><Badge severity={cert.health === 'healthy' ? 'success' : cert.health === 'unhealthy' ? 'error' : cert.health === 'degraded' ? 'warning' : 'neutral'}>
            {cert.daysLeft === undefined ? t('未知', 'Unknown') : t(`${cert.daysLeft} 天`, `${cert.daysLeft} day${Math.abs(cert.daysLeft) === 1 ? '' : 's'}`)}
          </Badge></td>
        </tr>)}</tbody>
      </table>
    </div> : <div className="cloud-empty">
      <h2>{search || health ? t('没有符合筛选条件的证书', 'No certificates match these filters') : t('未返回证书', 'No certificates returned')}</h2>
      <p>{t('此清单仅包含当前 Kubernetes 权限允许读取的证书元数据。', 'This list reflects your Kubernetes permissions.')}</p>
    </div>}
  </div>
}

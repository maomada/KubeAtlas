import { useMemo, useState } from 'react'
import { ApplicationsView, Badge, ChecksView, IssuesView, type AppRow, type IssueResourceRef } from '@maomada/k8s-ui'
import type { AuditResponse, IssuesResponse } from '../api/client'
import { kindToPluralWithGroup } from '../utils/navigation'
import type { HubCluster } from './cloudApi'
import { clusterPage } from './cloudApi'
import { buildFleetApplications, type ApplicationTarget } from './cloudApplications'
import { useFleetQuery } from './cloudFleet'
import { FleetCoverage } from './CloudHome'
import { CloudDialog, CloudError, CloudLink, CloudLoading } from './CloudShared'
import { navigateCloud } from './cloudNavigation'

export function resourceHref(ref: IssueResourceRef) {
  if (!ref.cluster_id) return '/clusters'
  const query = new URLSearchParams({ resource: ref.namespace ? `${ref.namespace}/${ref.name}` : ref.name })
  if (ref.group) query.set('apiGroup', ref.group)
  return clusterPage(ref.cluster_id, `/resources/${kindToPluralWithGroup(ref.kind, ref.group ?? '')}?${query}`)
}

export function CloudApplications({ clusters }: { clusters: HubCluster[] }) {
  const query = useFleetQuery<{ applications: AppRow[] }>(clusters, '/applications')
  const { targets, entries } = useMemo(() => buildFleetApplications(query.data ?? []), [query.data])
  const [destinations, setDestinations] = useState<ApplicationTarget[]>()
  function select(key: string) {
    const options = targets.get(key)
    if (options?.length === 1) navigateCloud(options[0].href)
    else if (options?.length) setDestinations(options)
  }
  return <div className="cloud-fleet-pane"><FleetCoverage results={query.data} />{query.error && <CloudError error={query.error} retry={() => { void query.refetch() }} />}<ApplicationsView entries={entries} variant="fleet" loading={query.isPending} onSelect={select} description="Deployable software across the fleet — services, workers and jobs grouped by app/release evidence." emptySlot={<div className="cloud-empty"><h2>No application data returned</h2><p>Check cluster connections and resource permissions.</p></div>} />{destinations && <CloudDialog title="Choose an application instance" onClose={() => setDestinations(undefined)}><div className="cloud-form">{destinations.map(target => <CloudLink className="cloud-button" key={target.clusterId} href={target.href}>{target.clusterName} →</CloudLink>)}</div></CloudDialog>}</div>
}

export function CloudIssues({ clusters }: { clusters: HubCluster[] }) {
  const query = useFleetQuery<IssuesResponse>(clusters, '/issues')
  const [search, setSearch] = useState('')
  const [severity, setSeverity] = useState('')
  const [namespace, setNamespace] = useState('')
  const allIssues = useMemo(() => query.data?.flatMap(result => (result.data?.issues ?? []).map(issue => ({ ...issue, cluster_id: result.cluster.id }))) ?? [], [query.data])
  const issues = allIssues.filter(issue => (!severity || issue.severity === severity) && (!namespace || issue.namespace === namespace) && [issue.name, issue.namespace, issue.kind, issue.message].join(' ').toLowerCase().includes(search.toLowerCase()))
  const incomplete = query.data?.filter(result => result.data && (result.data.visibility?.state === 'degraded' || result.data.unchecked_sources?.length || (result.data.total_matched ?? 0) > (result.data.total ?? result.data.issues.length))) ?? []
  const anyData = !!query.data?.some(result => result.data)
  const complete = !!query.data?.length && query.data.every(result => result.data && !result.error) && !incomplete.length
  return <div className="cloud-page"><div className="cloud-page-heading"><div><h1>Issues</h1><p>What's broken right now in the selected clusters — a shared triage queue.</p></div><CloudLink className="cloud-button" href="/settings/organization/issues">Manage what's tracked</CloudLink></div><FleetCoverage results={query.data} />{query.error && <CloudError error={query.error} />}{incomplete.length > 0 && <p role="status" className="cloud-coverage">Issue sources are incomplete or truncated in {incomplete.map(result => result.cluster.name).join(', ')}. An empty queue is not an all-clear.</p>}
    <div className="cloud-filters"><input type="search" aria-label="Search issues" placeholder="Search issues…" value={search} onChange={event => setSearch(event.target.value)} /><select aria-label="Severity" value={severity} onChange={event => setSeverity(event.target.value)}><option value="">Severity: Any</option><option value="critical">Critical</option><option value="warning">Warning</option></select><select aria-label="Namespace" value={namespace} onChange={event => setNamespace(event.target.value)}><option value="">Namespace: Any</option>{Array.from(new Set(allIssues.map(issue => issue.namespace))).sort().filter(Boolean).map(value => <option key={value}>{value}</option>)}</select></div>
    {query.isPending ? <CloudLoading /> : issues.length ? <IssuesView issues={issues} anyData={anyData} onResourceClick={ref => navigateCloud(resourceHref(ref))} resourceHref={resourceHref} clusterLabel={issue => clusters.find(cluster => cluster.id === issue.cluster_id)?.name} /> : search || severity || namespace ? <div className="cloud-empty"><h2>No issues match these filters</h2><button className="cloud-button" onClick={() => { setSearch(''); setSeverity(''); setNamespace('') }}>Clear filters</button></div> : complete ? <IssuesView issues={[]} anyData /> : <div className="cloud-empty"><h2>Issue visibility is incomplete</h2><p>No issues were returned by the available sources. Resolve the coverage warnings before treating this as healthy.</p></div>}
  </div>
}

export function CloudChecks({ clusters }: { clusters: HubCluster[] }) {
  const query = useFleetQuery<AuditResponse>(clusters, '/audit')
  const rows = query.data ?? []
  const checks = rows.flatMap(result => (result.data?.groupedChecks ?? []).map(check => ({ ...check, subject: { ...check.subject, cluster_id: result.cluster.id }, representativeFinding: { ...check.representativeFinding, resource: { ...check.representativeFinding.resource, cluster_id: result.cluster.id } }, findings: check.findings.map(finding => ({ ...finding, resource: { ...finding.resource, cluster_id: result.cluster.id } })), environment: result.cluster.environment })))
  const catalog = Object.assign({}, ...rows.flatMap(result => result.data ? [result.data.checks] : []))
  const missing = rows.flatMap(result => result.data ? [...(result.data.missingInputs ?? []), ...(result.data.groupedChecks ? [] : [`${result.cluster.name}: grouped checks unavailable`])] : [`${result.cluster.name}: unavailable`])
  return <div className="cloud-page"><div className="cloud-page-heading"><div><h1>Checks</h1><p>Security, reliability and efficiency best practices in the selected clusters, grouped into a remediation queue.</p></div><CloudLink href="/settings/organization/checks" className="cloud-button">Manage what's tracked</CloudLink></div><FleetCoverage results={rows} />{query.error && <CloudError error={query.error} />}{query.isPending ? <CloudLoading /> : <ChecksView checks={checks} catalog={catalog} anyData={rows.some(result => result.data?.groupedChecks !== undefined)} evaluated={rows.reduce((sum, result) => sum + (result.data ? result.data.summary.passing + result.data.summary.warning + result.data.summary.danger : 0), 0)} missingInputs={missing} resourceHref={resourceHref} onResourceClick={ref => navigateCloud(resourceHref(ref))} clusterLabel={check => clusters.find(cluster => cluster.id === check.subject.cluster_id)?.name} clusterLabelById={id => clusters.find(cluster => cluster.id === id)?.name} />}</div>
}

interface Certificate {
  name: string; namespace: string; issuer: string; domains: string; expiry?: string; daysLeft?: number
  health: 'healthy' | 'degraded' | 'unhealthy' | 'unknown'; source: 'cert-manager' | 'tls-secret'
}

export function CloudCertificates({ clusters }: { clusters: HubCluster[] }) {
  const query = useFleetQuery<Certificate[]>(clusters, '/certificates')
  const [search, setSearch] = useState('')
  const [health, setHealth] = useState('')
  const certificates = (query.data ?? []).flatMap(result => (result.data ?? []).map(cert => ({ ...cert, cluster: result.cluster }))).sort((a, b) => (a.daysLeft ?? Infinity) - (b.daysLeft ?? Infinity))
  const filtered = certificates.filter(cert => (!health || cert.health === health) && [cert.name, cert.namespace, cert.issuer, cert.domains, cert.cluster.name].join(' ').toLowerCase().includes(search.toLowerCase()))
  return <div className="cloud-page"><div className="cloud-page-heading"><div><h1>Certs</h1><p>Certificates visible to your identity — cert-manager Certificates and TLS secrets — nearest expiry first.</p></div></div><FleetCoverage results={query.data} />{query.error && <CloudError error={query.error} />}<div className="cloud-filters">{(['unhealthy', 'degraded', 'healthy', 'unknown'] as const).map((state, index) => <button key={state} className="cloud-button" aria-pressed={health === state} onClick={() => setHealth(health === state ? '' : state)}>{certificates.filter(cert => cert.health === state).length} {['Expired / soon', 'This month', 'Healthy', 'Unknown'][index]}</button>)}</div><div className="cloud-filters"><input type="search" aria-label="Search certificates" placeholder="Search certs by name, namespace, domain or issuer…" value={search} onChange={event => setSearch(event.target.value)} /></div>{query.isPending ? <CloudLoading /> : filtered.length ? <div className="cloud-table-panel cloud-table-scroll"><table className="cloud-table"><thead><tr><th>Certificate</th><th>Cluster</th><th>Namespace</th><th>Domains</th><th>Issuer</th><th>Expiry</th></tr></thead><tbody>{filtered.map(cert => <tr key={`${cert.cluster.id}:${cert.namespace}:${cert.source}:${cert.name}`}><td><CloudLink href={resourceHref({ cluster_id: cert.cluster.id, kind: cert.source === 'tls-secret' ? 'Secret' : 'Certificate', group: cert.source === 'tls-secret' ? '' : 'cert-manager.io', namespace: cert.namespace, name: cert.name })}>{cert.name}</CloudLink></td><td>{cert.cluster.name}</td><td>{cert.namespace}</td><td>{cert.domains}</td><td>{cert.issuer}</td><td><Badge severity={cert.health === 'healthy' ? 'success' : cert.health === 'unhealthy' ? 'error' : cert.health === 'degraded' ? 'warning' : 'neutral'}>{cert.daysLeft === undefined ? 'Unknown' : `${cert.daysLeft} days`}</Badge></td></tr>)}</tbody></table></div> : <div className="cloud-empty"><h2>{search || health ? 'No certificates match these filters' : 'No certificates returned'}</h2><p>This inventory includes only certificate metadata your Kubernetes permissions allow you to read.</p></div>}</div>
}

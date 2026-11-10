import { useMemo, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Tooltip } from '@maomada/k8s-ui'
import { ArrowDownUp, KeyRound, Pencil, Plus, Search, Server, ShieldOff } from 'lucide-react'
import { cloudRequest, clusterPage, type HubCluster } from './cloudApi'
import { CloudDialog, CloudError, CloudLink, ClusterStatus, CopyButton, relativeTime } from './CloudShared'

interface IssuedAgent { cluster: HubCluster; token: string }
type Action = { kind: 'edit' | 'rotate' | 'revoke'; cluster: HubCluster }

export function CloudClusters({ clusters, canManage }: { clusters: HubCluster[]; canManage: boolean }) {
  const [filter, setFilter] = useState('')
  const [sort, setSort] = useState<'name' | 'state' | 'version' | 'connectedAt'>('name')
  const [reverse, setReverse] = useState(false)
  const [action, setAction] = useState<Action>()
  const [issued, setIssued] = useState<IssuedAgent>()
  const rows = useMemo(() => clusters.filter(cluster => [cluster.name, cluster.id, cluster.environment, cluster.region].some(value => value.toLowerCase().includes(filter.toLowerCase())))
    .sort((a, b) => {
      const value = (cluster: HubCluster) => sort === 'version' ? cluster.agent?.version ?? '' : sort === 'connectedAt' ? cluster.agent?.connectedAt ?? '' : cluster[sort]
      return value(a).localeCompare(value(b), undefined, { numeric: true }) * (reverse ? -1 : 1)
    }), [clusters, filter, sort, reverse])
  function order(key: typeof sort) { if (sort === key) setReverse(!reverse); else { setSort(key); setReverse(false) } }
  return <div className="cloud-page">
    <div className="cloud-page-heading"><div><h1>Clusters</h1><p>{clusters.length} {clusters.length === 1 ? 'cluster' : 'clusters'} <span className="cloud-connected">{clusters.filter(cluster => cluster.state === 'connected').length} connected</span></p></div>{canManage && <CloudLink className="cloud-button cloud-primary" href="/install"><Plus size={16} />Connect cluster</CloudLink>}</div>
    {!clusters.length ? <div className="cloud-empty"><Server size={32} /><h2>{canManage ? 'No clusters yet' : 'No clusters assigned'}</h2><p>{canManage ? 'Connect a cluster through an outbound Agent tunnel. Current state stays in your cluster.' : 'Ask an organization administrator to grant cluster access.'}</p>{canManage && <CloudLink href="/install" className="cloud-button cloud-primary"><Plus size={16} />Connect your first cluster</CloudLink>}</div> : <section className="cloud-table-panel">
      <div className="cloud-table-filter"><label className="cloud-search"><Search size={16} /><input type="search" value={filter} onChange={event => setFilter(event.target.value)} placeholder="Filter by name, id, or tag — e.g. prod" aria-label="Filter clusters" /></label><span>{rows.length} {rows.length === 1 ? 'cluster' : 'clusters'}</span></div>
      <div className="cloud-table-scroll"><table className="cloud-table"><thead><tr>{(['name', 'state', 'version', 'connectedAt'] as const).map((key, index) => <th key={key} aria-sort={sort === key ? reverse ? 'descending' : 'ascending' : 'none'}><button onClick={() => order(key)}>{['Name', 'Status', 'Version', 'Connected since'][index]}<ArrowDownUp size={12} /></button></th>)}<th><span className="sr-only">Actions</span></th></tr></thead><tbody>
        {rows.map(cluster => <tr key={cluster.id} data-connected={cluster.state === 'connected'}><td><CloudLink className="cloud-cluster-name" href={clusterPage(cluster.id)}>{cluster.name}</CloudLink><div className="cloud-muted">{cluster.id}{cluster.environment && ` · ${cluster.environment}`}</div></td><td><ClusterStatus cluster={cluster} /></td><td className="portal-mono">{cluster.agent?.version ?? 'Unknown'}</td><td><Tooltip content={cluster.agent?.connectedAt ?? 'Connection time unavailable'}><span>{relativeTime(cluster.agent?.connectedAt)}</span></Tooltip></td><td>{canManage && <div className="cloud-row-actions"><button onClick={() => setAction({ kind: 'rotate', cluster })}><KeyRound size={14} />{cluster.revokedAt ? 'Reconnect' : 'Rotate token'}</button><button onClick={() => setAction({ kind: 'edit', cluster })}><Pencil size={14} />Edit</button>{!cluster.revokedAt && <button onClick={() => setAction({ kind: 'revoke', cluster })}><ShieldOff size={14} />Revoke access</button>}</div>}</td></tr>)}
        {!rows.length && <tr><td colSpan={5} className="cloud-muted">No clusters match this filter.</td></tr>}
      </tbody></table></div>
    </section>}
    {canManage && action && <ClusterAction key={`${action.kind}:${action.cluster.id}`} action={action} onClose={() => setAction(undefined)} onIssued={value => { setIssued(value); setAction(undefined) }} />}
    {issued && <AgentToken issued={issued} onClose={() => setIssued(undefined)} />}
  </div>
}

function ClusterAction({ action, onClose, onIssued }: { action: Action; onClose: () => void; onIssued: (value: IssuedAgent) => void }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState(action.cluster.name)
  const [environment, setEnvironment] = useState(action.cluster.environment)
  const [region, setRegion] = useState(action.cluster.region)
  const [description, setDescription] = useState(action.cluster.description)
  const [confirmation, setConfirmation] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true); setError(undefined)
    try {
      const base = `/hub/clusters/${encodeURIComponent(action.cluster.id)}`
      if (action.kind === 'edit') await cloudRequest(base, { method: 'PUT', body: { id: action.cluster.id, name, environment, region, description } })
      else if (action.kind === 'rotate') onIssued(await cloudRequest<IssuedAgent>(`${base}/rotate`, { method: 'POST', body: {} }))
      else await cloudRequest(`${base}/revoke`, { method: 'POST', body: {} })
      await queryClient.invalidateQueries({ queryKey: ['portal', '/api', '/hub/clusters'] })
      onClose()
    } catch (failure) { setError(failure) } finally { setBusy(false) }
  }
  const title = action.kind === 'edit' ? 'Edit cluster' : action.kind === 'rotate' ? 'Rotate cluster token' : 'Revoke cluster access'
  return <CloudDialog title={title} onClose={onClose} busy={busy}><form onSubmit={submit} className="cloud-form">
    {error !== undefined && <CloudError error={error} />}
    {action.kind === 'edit' ? <><label>Name<input required maxLength={128} value={name} onChange={event => setName(event.target.value)} /></label><label>Tag<input maxLength={128} placeholder="e.g. production" value={environment} onChange={event => setEnvironment(event.target.value)} /></label><label>Region<input maxLength={128} value={region} onChange={event => setRegion(event.target.value)} /></label><label>Description<textarea maxLength={1024} value={description} onChange={event => setDescription(event.target.value)} /></label></> : <><p>{action.kind === 'rotate' ? 'The current Agent token will stop working immediately. Save the new token, update its mounted file and restart your Agent.' : 'This disconnects the Agent and revokes its token. The cluster record and workloads remain; this does not uninstall anything.'}</p><label>Type {action.cluster.name} to confirm<input autoComplete="off" value={confirmation} onChange={event => setConfirmation(event.target.value)} /></label></>}
    <footer><button type="button" className="cloud-button" disabled={busy} onClick={onClose}>Cancel</button><button className="cloud-button cloud-primary" disabled={busy || (action.kind !== 'edit' && confirmation !== action.cluster.name)}>{busy ? 'Saving…' : action.kind === 'edit' ? 'Save changes' : action.kind === 'rotate' ? 'Rotate token' : 'Revoke access'}</button></footer>
  </form></CloudDialog>
}

function AgentToken({ issued, onClose }: { issued: IssuedAgent; onClose: () => void }) {
  const command = [
    `KUBEATLAS_AGENT_HUB_URL='${window.location.origin}'`,
    `KUBEATLAS_AGENT_HUB_CLUSTER_ID='${issued.cluster.id}'`,
    'KUBEATLAS_AGENT_HUB_TOKEN_FILE=/var/run/secrets/kubeatlas/hub-token',
    './kubeatlas',
  ].join(' \\\n')
  return <CloudDialog title="Agent token — shown only once" onClose={onClose}><div className="cloud-form"><p>Store this token in a private file mounted into your Agent. The Hub stores only its digest; leaving this dialog discards the visible token.</p><label>Agent token<textarea readOnly spellCheck={false} autoComplete="off" value={issued.token} /></label><CopyButton value={issued.token} label="Copy token" /><p>Use the KubeAtlas build that includes self-hosted Hub mode. An upstream Radar image cannot connect to this Hub.</p><pre className="cloud-code">{command}</pre><CopyButton value={command} label="Copy startup command" /><p className="cloud-muted">HTTPS is required outside loopback. For a private CA, also set KUBEATLAS_AGENT_HUB_CA_FILE. Grant the Hub identity the intended Kubernetes RBAC permissions; connection alone does not grant access.</p><footer><button className="cloud-button cloud-primary" onClick={onClose}>I saved the token</button></footer></div></CloudDialog>
}

export function CloudInstall() {
  const queryClient = useQueryClient()
  const [id, setID] = useState('')
  const [name, setName] = useState('')
  const [environment, setEnvironment] = useState('')
  const [issued, setIssued] = useState<IssuedAgent>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  async function create(event: FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true); setError(undefined)
    try {
      setIssued(await cloudRequest<IssuedAgent>('/hub/clusters', { method: 'POST', body: { id, name, environment, region: '', description: '' } }))
      await queryClient.invalidateQueries({ queryKey: ['portal', '/api', '/hub/clusters'] })
    } catch (failure) { setError(failure) } finally { setBusy(false) }
  }
  return <div className="cloud-page cloud-narrow"><div className="cloud-page-heading"><div><h1>Connect a cluster</h1><p>Install an outbound Agent. No inbound Kubernetes endpoint or resource replication required.</p></div></div><section className="cloud-card"><h2>1. Register your cluster</h2><form className="cloud-form" onSubmit={create}>{error !== undefined && <CloudError error={error} />}<label>Name<input required maxLength={128} value={name} onChange={event => setName(event.target.value)} placeholder="Production EU" /></label><label>Cluster ID<input required pattern="[a-z0-9][a-z0-9-]{0,62}" maxLength={63} value={id} onChange={event => setID(event.target.value)} placeholder="production-eu" /><small>Stable routing ID: lowercase letters, numbers and hyphens.</small></label><label>Tag<input value={environment} maxLength={128} onChange={event => setEnvironment(event.target.value)} placeholder="prod" /></label><footer><CloudLink href="/clusters" className="cloud-button">Cancel</CloudLink><button className="cloud-button cloud-primary" disabled={busy || !!issued}>{busy ? 'Registering…' : 'Create Agent token'}</button></footer></form></section><section className="cloud-card"><h2>2. Start the Agent and grant access</h2><p>Use the private token file and startup command shown after registration. Configure Kubernetes RoleBindings for the identity asserted by your Hub. The cluster will show Connected once its authenticated tunnel is ready.</p><CloudLink href="/clusters" className="cloud-text-button">View connection status →</CloudLink></section>{issued && <AgentToken issued={issued} onClose={() => { setIssued(undefined); setID(''); setName(''); setEnvironment('') }} />}</div>
}

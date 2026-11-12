import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Tooltip } from '@maomada/k8s-ui'
import { ArrowDownUp, KeyRound, Pencil, Plus, Search, Server, ShieldOff } from 'lucide-react'
import { cloudRequest, clusterPage, type HubCluster } from './cloudApi'
import { CloudDialog, CloudError, CloudLink, ClusterStatus, CopyButton, relativeTime } from './CloudShared'
import { ConnectionProfileSelector, useConnectionProfiles } from './ConnectionProfiles'

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

function AgentToken({ issued, onClose, newInstall = false }: { issued: IssuedAgent; onClose: () => void; newInstall?: boolean }) {
  const profile = useConnectionProfiles()
  const [caFile, setCAFile] = useState('')
  const catalog = profile.connections?.catalog
  const command = profile.installAvailable && catalog?.version && catalog.installerSHA256 && catalog.chartSHA256 && catalog.imageDigest && profile.connections?.ownerUserID
    ? [
      '(',
      'set -eu',
      'umask 077',
      'mkdir -p "$HOME/.config/kubeatlas"',
      'chmod 700 "$HOME/.config/kubeatlas"',
      `TOKEN_FILE="$HOME/.config/kubeatlas/"${shellQuote(`${issued.cluster.id}.token`)}`,
      `CA_FILE=${shellQuote(caFile)}`,
      'read -r -s -p "Paste the one-time Hub Agent token: " KUBEATLAS_AGENT_TOKEN',
      'printf \'\\n\'',
      '[ -n "$KUBEATLAS_AGENT_TOKEN" ] || { printf \'Token is required\\n\' >&2; exit 1; }',
      'printf \'%s\\n\' "$KUBEATLAS_AGENT_TOKEN" > "$TOKEN_FILE"',
      'unset KUBEATLAS_AGENT_TOKEN',
      'chmod 600 "$TOKEN_FILE"',
      'CURL_CA=()',
      'INSTALLER_CA=()',
      'if [ -n "$CA_FILE" ]; then CURL_CA=(--cacert "$CA_FILE"); INSTALLER_CA=(--ca-file "$CA_FILE"); fi',
      'TMP="$(mktemp -d)"',
      'trap \'rm -rf -- "$TMP"\' EXIT',
      `HTTP_STATUS="$(curl "\${CURL_CA[@]}" --proto '=http,https' --tlsv1.2 --fail --silent --show-error --write-out '%{http_code}' --output "$TMP/installer.sh" ${shellQuote(`${profile.origin}/downloads/agent/${catalog.version}/installer.sh`)})"`,
      '[ "$HTTP_STATUS" = 200 ] || { printf \'Installer download returned HTTP %s\\n\' "$HTTP_STATUS" >&2; exit 1; }',
      `printf '%s  %s\\n' ${shellQuote(catalog.installerSHA256)} "$TMP/installer.sh" | sha256sum -c -`,
      'chmod 700 "$TMP/installer.sh"',
      `"$TMP/installer.sh" --hub-origin ${shellQuote(profile.origin)} --context "\${KUBE_CONTEXT:?Set KUBE_CONTEXT to the intended cluster}" --cluster-id ${shellQuote(issued.cluster.id)} --version ${shellQuote(catalog.version)} --image-digest ${shellQuote(catalog.imageDigest)} --chart-sha256 ${shellQuote(catalog.chartSHA256)} --owner-user-id ${shellQuote(profile.connections.ownerUserID)} --token-file "$TOKEN_FILE" "\${INSTALLER_CA[@]}"`,
      ')',
    ].join('\n')
    : ''
  const reconnectCommand = [
    'kubectl --context "$KUBE_CONTEXT" --namespace kubeatlas create secret generic kubeatlas-hub-token --from-file=token="$TOKEN_FILE" --dry-run=client -o yaml | kubectl --context "$KUBE_CONTEXT" apply -f -',
    'kubectl --context "$KUBE_CONTEXT" --namespace kubeatlas rollout restart deployment/kubeatlas',
  ].join('\n')
  return <CloudDialog title="Agent token — shown only once" onClose={onClose}><div className="cloud-form"><p>Hub 只保存 Token 摘要；关闭此窗口后无法再次读取原文。安装命令会在终端隐藏输入 Token，并以 0600 权限创建文件。</p><label>Agent token<textarea readOnly spellCheck={false} autoComplete="off" value={issued.token} /></label><CopyButton value={issued.token} label="复制 Token" />
    {newInstall ? <><ConnectionProfileSelector />{profile.installAvailable && command ? <><label>Optional private CA file<input autoComplete="off" value={caFile} onChange={event => setCAFile(event.target.value)} placeholder="/path/to/hub-ca.crt" /></label><p>先设置 <code>KUBE_CONTEXT</code>，运行以下命令并在提示时粘贴 Token。可选 CA 文件会同时用于安装器下载和 Agent 配置；不会使用不安全的 TLS 绕过。</p><pre className="cloud-code">{command}</pre><CopyButton value={command} label="复制安装命令" /></> : <p role="alert">{profile.error ? `连接配置读取失败：${profile.error}；不会生成安装命令。` : profile.loading ? '正在读取连接配置；暂不生成安装命令。' : '所选连接配置或匹配版本的 Hub 安装包不可用；不会生成安装命令。'}</p>}</> : <><p>轮换后，旧 Token 立即失效。安全地替换现有 Secret 文件后重启 Agent；以下示例要求显式设置 KUBE_CONTEXT 和 TOKEN_FILE。</p><pre className="cloud-code">{reconnectCommand}</pre><CopyButton value={reconnectCommand} label="复制重连命令" /></>}
    <p className="cloud-muted">HTTPS 需要与 IP SAN/DNS SAN 匹配的证书。Agent CA 只配置 Agent 到 Hub 的信任；节点镜像运行时必须单独信任 Hub Registry 的证书。Hub 所有者通过精确 User 身份获得集群管理员权限；Agent ServiceAccount 的 impersonate 权限同样高度敏感。</p><footer><button className="cloud-button cloud-primary" onClick={onClose}>我已安全保存 Token</button></footer></div></CloudDialog>
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

export function CloudInstall() {
  const queryClient = useQueryClient()
  const profile = useConnectionProfiles()
  const [id, setID] = useState('')
  const [name, setName] = useState('')
  const [environment, setEnvironment] = useState('')
  const [issued, setIssued] = useState<IssuedAgent>()
  const [registeredClusterID, setRegisteredClusterID] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  const [polling, setPolling] = useState(false)
  const [connectionState, setConnectionState] = useState<'registered' | 'checking' | 'connected' | 'timeout' | 'error'>('registered')
  useEffect(() => {
    if (!registeredClusterID || !polling) return
    const deadline = Date.now() + 120000
    const controller = new AbortController()
    let active = true
    let hadError = false
    let timer: number | undefined
    const deadlineTimer = window.setTimeout(() => {
      if (!active) return
      setConnectionState('error')
      setPolling(false)
      controller.abort()
    }, 120000)
    const check = async () => {
      if (!active) return
      if (Date.now() >= deadline) {
        setConnectionState(hadError ? 'error' : 'timeout')
        setPolling(false)
        return
      }
      try {
        const result = await cloudRequest<{ clusters: HubCluster[] }>('/hub/clusters', { signal: controller.signal })
        if (!active) return
        if (result.clusters.some(cluster => cluster.id === registeredClusterID && cluster.state === 'connected')) {
          setConnectionState('connected')
          setPolling(false)
          return
        }
      } catch {
        if (!active) return
        hadError = true
        if (Date.now() >= deadline || controller.signal.aborted) {
          setConnectionState('error')
          setPolling(false)
          return
        }
      }
      if (Date.now() >= deadline) {
        setConnectionState(hadError ? 'error' : 'timeout')
        setPolling(false)
        return
      }
      timer = window.setTimeout(() => { void check() }, 5000)
    }
    void check()
    return () => {
      active = false
      controller.abort()
      window.clearTimeout(deadlineTimer)
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [registeredClusterID, polling])
  async function create(event: FormEvent) {
    event.preventDefault()
    if (busy || !profile.installAvailable) return
    setBusy(true); setError(undefined)
    try {
      const result = await cloudRequest<IssuedAgent>('/hub/clusters', { method: 'POST', body: { id, name, environment, region: '', description: '' } })
      setIssued(result)
      setRegisteredClusterID(result.cluster.id)
      setConnectionState('registered')
      setPolling(false)
      await queryClient.invalidateQueries({ queryKey: ['portal', '/api', '/hub/clusters'] })
    } catch (failure) { setError(failure) } finally { setBusy(false) }
  }
  const connectionMessage = connectionState === 'registered' ? 'Agent registered; start the installer and check again.'
    : connectionState === 'checking' ? 'Checking the Agent connection…'
      : connectionState === 'connected' ? 'Connected'
        : connectionState === 'timeout' ? 'No connection was confirmed before the status check timed out. The current state may be unknown; retry the check.'
          : 'The connection status could not be checked. It is unknown; retry the check.'
  return <div className="cloud-page cloud-narrow">
    <div className="cloud-page-heading"><div><h1>Connect a cluster</h1><p>Install an outbound Agent. The Hub serves the installer, chart and pinned image.</p></div></div>
    <ConnectionProfileSelector />
    {profile.loading && <p role="status" className="cloud-card">正在读取 Hub 连接配置…</p>}
    {profile.error && <p role="alert" className="cloud-card">连接配置读取失败：{profile.error}。请在 Organization 设置中重试；不会回退到浏览器地址。</p>}
    {!profile.loading && !profile.error && !profile.installAvailable && <p role="status" className="cloud-card">所选地址尚未配置，或版本匹配的本地安装包不可用。请先在 Organization 设置中配置；不会回退到浏览器地址。</p>}
    <section className="cloud-card"><h2>1. Register your cluster</h2><form className="cloud-form" onSubmit={create}>{error !== undefined && <CloudError error={error} />}<label>Name<input required maxLength={128} value={name} onChange={event => setName(event.target.value)} placeholder="Production EU" /></label><label>Cluster ID<input required pattern="[a-z0-9][a-z0-9-]{0,62}" maxLength={63} value={id} onChange={event => setID(event.target.value)} placeholder="production-eu" /><small>Stable routing ID: lowercase letters, numbers and hyphens.</small></label><label>Tag<input value={environment} maxLength={128} onChange={event => setEnvironment(event.target.value)} placeholder="prod" /></label><footer><CloudLink href="/clusters" className="cloud-button">Cancel</CloudLink><button className="cloud-button cloud-primary" disabled={busy || !!issued || !profile.installAvailable}>{busy ? 'Registering…' : 'Create Agent token'}</button></footer></form></section>
    <section className="cloud-card"><h2>2. Install and verify the Agent</h2><p>Use an explicit kube context and the private token file shown after registration. A Helm rollout is not proof of connection; the directory reports Connected only after the authenticated Agent tunnel is established.</p>{registeredClusterID && <><p role="status">Cluster {registeredClusterID}: {connectionMessage}</p><button className="cloud-button" disabled={polling || connectionState === 'connected'} onClick={() => { setConnectionState('checking'); setPolling(true) }}>{polling ? 'Checking…' : connectionState === 'timeout' || connectionState === 'error' ? 'Retry status check' : 'Check connection status'}</button></>}<CloudLink href="/clusters" className="cloud-text-button">View connection status →</CloudLink></section>
    {issued && <AgentToken issued={issued} newInstall onClose={() => setIssued(undefined)} />}
  </div>
}

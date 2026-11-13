import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Tooltip } from '@maomada/k8s-ui'
import { ArrowDownUp, Pencil, Plus, Search, Server, ShieldOff } from 'lucide-react'
import { cloudRequest, clusterPage, type HubCluster } from './cloudApi'
import { CloudDialog, CloudError, CloudLink, ClusterStatus, CopyButton, relativeTime, UnencryptedHttpWarning } from './CloudShared'
import { ConnectionProfileSelector, useConnectionProfiles } from './ConnectionProfiles'
import { useCloudLocation } from './cloudNavigation'
import type { HubContext } from './CloudOrganization'

type Action = { kind: 'edit' | 'revoke'; cluster: HubCluster }

export function CloudClusters({ clusters, canManage }: { clusters: HubCluster[]; canManage: boolean }) {
  const [filter, setFilter] = useState('')
  const [sort, setSort] = useState<'name' | 'state' | 'version' | 'connectedAt'>('name')
  const [reverse, setReverse] = useState(false)
  const [action, setAction] = useState<Action>()
  const rows = useMemo(() => clusters.filter(cluster => [cluster.name, cluster.id, cluster.environment, cluster.region].some(value => value.toLowerCase().includes(filter.toLowerCase())))
    .sort((a, b) => {
      const value = (cluster: HubCluster) => sort === 'version' ? cluster.agent?.version ?? '' : sort === 'connectedAt' ? cluster.agent?.connectedAt ?? '' : cluster[sort]
      return value(a).localeCompare(value(b), undefined, { numeric: true }) * (reverse ? -1 : 1)
    }), [clusters, filter, sort, reverse])
  function order(key: typeof sort) { if (sort === key) setReverse(!reverse); else { setSort(key); setReverse(false) } }
  return <div className="cloud-page">
    <div className="cloud-page-heading"><div><h1>集群</h1><p>{clusters.length} 个集群 <span className="cloud-connected">{clusters.filter(cluster => cluster.state === 'connected').length} 个已连接</span></p></div>{canManage && <CloudLink className="cloud-button cloud-primary" href="/install"><Plus size={16} />连接集群</CloudLink>}</div>
    {!clusters.length ? <div className="cloud-empty"><Server size={32} /><h2>{canManage ? '暂无集群' : '尚未分配集群'}</h2><p>{canManage ? '通过出站 Agent 隧道连接集群；集群当前状态由实时目录提供。' : '请联系组织 Owner 或平台 Owner 授予组织访问权限。'}</p>{canManage && <CloudLink href="/install" className="cloud-button cloud-primary"><Plus size={16} />连接首个集群</CloudLink>}</div> : <section className="cloud-table-panel">
      <div className="cloud-table-filter"><label className="cloud-search"><Search size={16} /><input type="search" value={filter} onChange={event => setFilter(event.target.value)} placeholder="按名称、ID 或标签筛选，例如 prod" aria-label="筛选集群" /></label><span>{rows.length} 个集群</span></div>
      <div className="cloud-table-scroll"><table className="cloud-table"><thead><tr>{(['name', 'state', 'version', 'connectedAt'] as const).map((key, index) => <th key={key} aria-sort={sort === key ? reverse ? 'descending' : 'ascending' : 'none'}><button onClick={() => order(key)}>{['名称', '状态', '版本', '连接时间'][index]}<ArrowDownUp size={12} /></button></th>)}<th><span className="sr-only">操作</span></th></tr></thead><tbody>
        {rows.map(cluster => <tr key={cluster.id} data-connected={cluster.state === 'connected'}><td><CloudLink className="cloud-cluster-name" href={clusterPage(cluster.id)}>{cluster.name}</CloudLink><div className="cloud-muted">{cluster.id}{cluster.environment && ' · ' + cluster.environment}</div></td><td><ClusterStatus cluster={cluster} /></td><td className="portal-mono">{cluster.agent?.version ?? '未知'}</td><td><Tooltip content={cluster.agent?.connectedAt ?? '连接时间不可用'}><span>{relativeTime(cluster.agent?.connectedAt)}</span></Tooltip></td><td>{canManage && <div className="cloud-row-actions">{(cluster.revokedAt || cluster.state === 'revoked') && <CloudLink href={'/install?clusterId=' + encodeURIComponent(cluster.id)} className="cloud-text-button">重新配对</CloudLink>}<button onClick={() => setAction({ kind: 'edit', cluster })}><Pencil size={14} />编辑</button>{!cluster.revokedAt && cluster.state !== 'revoked' && <button onClick={() => setAction({ kind: 'revoke', cluster })}><ShieldOff size={14} />撤销访问</button>}</div>}</td></tr>)}
        {!rows.length && <tr><td colSpan={5} className="cloud-muted">没有符合筛选条件的集群。</td></tr>}
      </tbody></table></div>
    </section>}
    {canManage && action && <ClusterAction key={action.kind + ':' + action.cluster.id} action={action} onClose={() => setAction(undefined)} />}
  </div>
}

function ClusterAction({ action, onClose }: { action: Action; onClose: () => void }) {
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
      const base = '/hub/clusters/' + encodeURIComponent(action.cluster.id)
      if (action.kind === 'edit') await cloudRequest(base, { method: 'PUT', body: { id: action.cluster.id, name, environment, region, description } })
      else await cloudRequest(base + '/revoke', { method: 'POST', body: {} })
      await queryClient.invalidateQueries({ queryKey: ['portal', '/api', '/hub/clusters'] })
      onClose()
    } catch (failure) { setError(failure) } finally { setBusy(false) }
  }
  return <CloudDialog title={action.kind === 'edit' ? '编辑集群' : '撤销集群访问'} onClose={onClose} busy={busy}><form onSubmit={submit} className="cloud-form">
    {error !== undefined && <CloudError error={error} />}
    {action.kind === 'edit' ? <><label>集群名称<input required maxLength={128} value={name} onChange={event => setName(event.target.value)} /></label><label>标签<input maxLength={128} placeholder="例如 production" value={environment} onChange={event => setEnvironment(event.target.value)} /></label><label>区域<input maxLength={128} value={region} onChange={event => setRegion(event.target.value)} /></label><label>描述<textarea maxLength={1024} value={description} onChange={event => setDescription(event.target.value)} /></label></> : <><p>此操作会撤销 Agent 访问，但保留集群记录和工作负载，不会卸载任何组件。重新连接需要重新注册并明确审批。</p><label>输入 {action.cluster.name} 以确认<input autoComplete="off" value={confirmation} onChange={event => setConfirmation(event.target.value)} /></label></>}
    <footer><button type="button" className="cloud-button" disabled={busy} onClick={onClose}>取消</button><button className="cloud-button cloud-primary" disabled={busy || action.kind === 'revoke' && confirmation !== action.cluster.name}>{busy ? '正在保存…' : action.kind === 'edit' ? '保存更改' : '撤销访问'}</button></footer>
  </form></CloudDialog>
}

interface EnrollmentCommand { command: string; origin: string; profile: 'lan' | 'public'; profileRevision: number }

function expiryIsValid(value: EnrollmentLookup) {
  const expiry = Date.parse(value.expiresAt)
  return Number.isFinite(expiry) && expiry > Date.now()
}
interface EnrollmentLookup { id: string; userCode: string; contextName: string; clusterFingerprint: string; status: string; expiresAt: string }

function enrollmentStatusLabel(status: string) {
  switch (status) {
    case 'pending': return '待处理'
    case 'approved': return '已批准'
    case 'denied': return '已拒绝'
    case 'expired': return '已过期'
    case 'used': return '已使用'
    default: return '未知状态'
  }
}

export function CloudInstall({ hub, clusters, canManage, directoryError, onRefreshClusters }: {
  hub: HubContext; clusters: HubCluster[]; canManage: boolean; directoryError?: unknown; onRefreshClusters: () => void
}) {
  const queryClient = useQueryClient()
  const profile = useConnectionProfiles()
  const location = useCloudLocation()
  const commandRequest = useRef<AbortController | null>(null)
  const commandGeneration = useRef(0)
  const profileStateRef = useRef({ profile: profile.profile, revision: profile.profileRevision })
  const [command, setCommand] = useState<EnrollmentCommand>()
  const [commandRequestState, setCommandRequestState] = useState<{ profileRevision: number; busy: boolean }>()
  const [commandErrorState, setCommandErrorState] = useState<{ profileRevision: number; error: unknown }>()
  const [userCode, setUserCode] = useState('')
  const [lookup, setLookup] = useState<EnrollmentLookup>()
  const [lookupBusy, setLookupBusy] = useState(false)
  const [lookupError, setLookupError] = useState<unknown>()
  const [clusterName, setClusterName] = useState('')
  const [selectedClusterId, setSelectedClusterId] = useState(location.searchParams.get('clusterId') ?? '')
  const [organizationConfirmed, setOrganizationConfirmed] = useState(false)
  const [mutationBusy, setMutationBusy] = useState(false)
  const [mutationError, setMutationError] = useState<unknown>()
  const [mutationUncertain, setMutationUncertain] = useState(false)
  const [completion, setCompletion] = useState<'approved' | 'denied'>()
  const [expirySnapshot, setExpirySnapshot] = useState<{ id: string; now: number }>()

  useEffect(() => {
    profileStateRef.current = { profile: profile.profile, revision: profile.profileRevision }
    commandRequest.current?.abort()
    commandRequest.current = null
    commandGeneration.current++
    return () => commandRequest.current?.abort()
  }, [profile.profile, profile.profileRevision])

  const lookupId = lookup?.id
  useEffect(() => {
    if (!lookupId) return
    const update = () => setExpirySnapshot({ id: lookupId, now: Date.now() })
    const timeout = window.setTimeout(update, 0)
    const interval = window.setInterval(update, 1000)
    return () => { window.clearTimeout(timeout); window.clearInterval(interval) }
  }, [lookupId])

  const activeOrganization = hub.organizations.find(organization => organization.id === hub.activeOrganizationId)
  const reEnrollmentClusters = clusters.filter(cluster => cluster.organizationId === hub.activeOrganizationId && (cluster.state === 'revoked' || !!cluster.revokedAt))
  const selectedReEnrollment = reEnrollmentClusters.find(cluster => cluster.id === selectedClusterId)
  const commandBusy = commandRequestState?.profileRevision === profile.profileRevision && commandRequestState.busy
  const commandError = commandErrorState?.profileRevision === profile.profileRevision ? commandErrorState.error : undefined
  const commandVisible = command?.profileRevision === profile.profileRevision ? command : undefined

  async function generateCommand() {
    if (!canManage || !hub.activeOrganizationId || !profile.installAvailable || commandBusy) return
    const requestedProfile = profile.profile
    const requestedRevision = profile.profileRevision
    const generation = ++commandGeneration.current
    const controller = new AbortController()
    commandRequest.current?.abort()
    commandRequest.current = controller
    setCommandRequestState({ profileRevision: requestedRevision, busy: true })
    setCommandErrorState(undefined)
    setCommand(undefined)
    try {
      const result = await cloudRequest<{ command: string; origin: string }>('/hub/enrollments/command', { method: 'POST', body: { profile: requestedProfile }, signal: controller.signal })
      const currentProfile = profileStateRef.current
      if (generation !== commandGeneration.current || currentProfile.profile !== requestedProfile || currentProfile.revision !== requestedRevision) return
      if (typeof result.command !== 'string' || !result.command.trim() || typeof result.origin !== 'string' || !result.origin.trim()) throw new Error('Hub 返回的注册命令不完整。')
      if (/--(?:token|(?:agent|device|bootstrap|mcp)-token)(?:=|\s)|\b(?:TOKEN|(?:AGENT|DEVICE|BOOTSTRAP|MCP)_TOKEN)\s*=/i.test(result.command)) throw new Error('服务器返回的命令包含疑似凭据参数，未予显示。')
      const selectedOrigin = new URL(profile.origin)
      const resultOrigin = new URL(result.origin)
      if (resultOrigin.username || resultOrigin.password || resultOrigin.search || resultOrigin.hash || resultOrigin.pathname !== '/' || !['http:', 'https:'].includes(resultOrigin.protocol) || resultOrigin.origin !== selectedOrigin.origin) throw new Error('命令地址与当前选定的连接配置不匹配。')
      setCommand({ command: result.command, origin: result.origin, profile: requestedProfile, profileRevision: requestedRevision })
    } catch (failure) {
      if (!controller.signal.aborted && generation === commandGeneration.current) setCommandErrorState({ profileRevision: requestedRevision, error: failure })
    } finally {
      if (generation === commandGeneration.current) setCommandRequestState({ profileRevision: requestedRevision, busy: false })
    }
  }

  async function lookupEnrollment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canManage || !hub.activeOrganizationId || lookupBusy || mutationBusy || !userCode.trim()) return
    setLookupBusy(true); setLookupError(undefined); setLookup(undefined); setCompletion(undefined); setMutationError(undefined); setMutationUncertain(false); setOrganizationConfirmed(false)
    try {
      const result = await cloudRequest<EnrollmentLookup>('/hub/enrollments/lookup', { method: 'POST', body: { userCode: userCode.trim() } })
      if (typeof result.id !== 'string' || !result.id.trim() || typeof result.userCode !== 'string' || !result.userCode.trim() || typeof result.contextName !== 'string' || !result.contextName.trim() || typeof result.clusterFingerprint !== 'string' || !result.clusterFingerprint.trim() || typeof result.status !== 'string' || !result.status.trim() || typeof result.expiresAt !== 'string' || !result.expiresAt.trim() || !Number.isFinite(Date.parse(result.expiresAt))) throw new Error('配对查询信息不完整；必须包含上下文、指纹、状态和有效期。')
      setSelectedClusterId('')
      setExpirySnapshot({ id: result.id, now: Date.now() })
      setLookup({ id: result.id, userCode: result.userCode, contextName: result.contextName, clusterFingerprint: result.clusterFingerprint, status: result.status, expiresAt: result.expiresAt })
      setClusterName(result.contextName)
    } catch (failure) { setLookupError(failure) } finally { setLookupBusy(false) }
  }

  async function approveEnrollment() {
    if (!canManage || !hub.activeOrganizationId || !lookup || lookup.status !== 'pending' || !expiryIsValid(lookup) || mutationUncertain || !organizationConfirmed || !clusterName.trim() || mutationBusy) return
    setMutationBusy(true); setMutationError(undefined)
    try {
      const body = { userCode: lookup.userCode, organizationId: hub.activeOrganizationId, name: clusterName.trim(), ...(selectedReEnrollment ? { clusterId: selectedReEnrollment.id } : {}) }
      const result = await cloudRequest<{ id: string; status: string }>('/hub/enrollments/' + encodeURIComponent(lookup.id) + '/approve', { method: 'POST', body })
      if (result.status !== 'approved') throw new Error('未能确认配对批准结果。')
      setCompletion('approved')
      await queryClient.invalidateQueries({ queryKey: ['portal', '/api', '/hub/clusters'] })
    } catch (failure) { setMutationUncertain(true); setMutationError(failure) }
    finally { setMutationBusy(false) }
  }

  async function denyEnrollment() {
    if (!canManage || !hub.activeOrganizationId || !lookup || lookup.status !== 'pending' || !expiryIsValid(lookup) || mutationUncertain || mutationBusy) return
    setMutationBusy(true); setMutationError(undefined)
    try {
      await cloudRequest('/hub/enrollments/' + encodeURIComponent(lookup.id) + '/deny', { method: 'POST', body: { userCode: lookup.userCode } })
      setCompletion('denied')
    } catch (failure) { setMutationUncertain(true); setMutationError(failure) }
    finally { setMutationBusy(false) }
  }

  const validExpiry = !!lookup && expirySnapshot?.id === lookup.id && Number.isFinite(Date.parse(lookup.expiresAt)) && Date.parse(lookup.expiresAt) > expirySnapshot.now
  const canComplete = !!lookup && lookup.status === 'pending' && validExpiry && !completion

  return <div className="cloud-page cloud-narrow">
    <div className="cloud-page-heading"><div><h1>连接集群</h1><p>生成 Hub 提供的命令，再为当前组织审核并批准设备配对。</p></div></div>
    <section className="cloud-card"><h2>注册命令</h2><p>命令使用已配置的内网或公网连接配置，由 Hub 生成；浏览器不会自行拼装安装命令。</p>
      <ConnectionProfileSelector />
      {profile.loading && <p role="status">正在读取连接配置…</p>}
      {profile.error && <p role="alert">连接配置不可用：{profile.error}</p>}
      {!profile.loading && !profile.error && !profile.installAvailable && <p role="status">所选连接配置或引导目录不可用，无法生成命令。</p>}
      <button className="cloud-button cloud-primary" disabled={!canManage || !hub.activeOrganizationId || !profile.installAvailable || commandBusy} onClick={() => { void generateCommand() }}>{commandBusy ? '正在生成…' : '生成注册命令'}</button>
      {commandError !== undefined && <CloudError error={commandError} />}
      {commandVisible && <div className="cloud-form mt-4"><p>Hub 地址：<code className="portal-mono">{commandVisible.origin}</code></p><UnencryptedHttpWarning origin={commandVisible.origin} enrollment /><p className="text-warning-text">如需使用 HTTP 镜像仓库，操作员必须单独配置节点运行时。本 Portal 不会更改节点或容器运行时设置。</p><pre className="cloud-code">{commandVisible.command}</pre><CopyButton value={commandVisible.command} label="复制注册命令" /></div>}
    </section>

    <section className="cloud-card"><h2>设备配对审批</h2><p>输入集群显示的配对码。系统不会列出待处理设备；只查询你提交的配对码及其上下文。</p>
      <form className="cloud-form" onSubmit={lookupEnrollment}><label>配对码<input required maxLength={128} autoComplete="off" value={userCode} onChange={event => setUserCode(event.target.value)} /></label><button className="cloud-button" disabled={!canManage || !hub.activeOrganizationId || lookupBusy || mutationBusy || !userCode.trim()}>{lookupBusy ? '正在查询…' : '查询配对'}</button></form>
      {lookupError !== undefined && <CloudError error={lookupError} />}
      {lookup && <div className="cloud-card mt-4 p-4"><h3>配对审核</h3><dl className="cloud-identity-details mt-4"><dt>Kubernetes 上下文</dt><dd>{lookup.contextName}</dd><dt>kube-system 指纹</dt><dd className="portal-mono">{lookup.clusterFingerprint}</dd><dt>有效期至</dt><dd>{lookup.expiresAt}</dd><dt>状态</dt><dd>{enrollmentStatusLabel(lookup.status)}</dd></dl>
        <p>目标组织：<strong>{activeOrganization?.name ?? '不可用'}</strong>（{hub.activeOrganizationId ?? '无'}）。确认后只会在此组织内创建集群访问。</p>
        {canComplete && <div className="cloud-form mt-4"><label>集群名称<input required maxLength={128} value={clusterName} onChange={event => setClusterName(event.target.value)} /></label>
          {reEnrollmentClusters.length > 0 && <label>要重新配对的现有集群（可选）<select className="portal-control" value={selectedReEnrollment?.id ?? ''} onChange={event => setSelectedClusterId(event.target.value)}><option value="">创建新的集群记录</option>{reEnrollmentClusters.map(cluster => <option key={cluster.id} value={cluster.id}>{cluster.name} · {cluster.id}</option>)}</select></label>}
          <label className="cloud-confirm-access"><input type="checkbox" checked={organizationConfirmed} onChange={event => setOrganizationConfirmed(event.target.checked)} />我确认此配对请求属于 {activeOrganization?.name ?? '当前组织'}。</label>
          <div className="cloud-row-actions"><button type="button" className="cloud-button cloud-primary" disabled={mutationBusy || mutationUncertain || !organizationConfirmed || !clusterName.trim()} onClick={() => { void approveEnrollment() }}>{mutationBusy ? '正在提交…' : '批准配对'}</button><button type="button" className="cloud-button" disabled={mutationBusy || mutationUncertain} onClick={() => { void denyEnrollment() }}>拒绝配对</button></div>
        </div>}
        {mutationError !== undefined && <><CloudError error={mutationError} />{mutationUncertain && <p role="alert">系统不会自动重试配对操作；请重新查询配对码并核实状态后再决定是否重试。</p>}</>}
        {completion === 'approved' && <p role="status" className="mt-4">配对已批准。批准不代表 Agent 已安装或已连接；请在下方实时目录中查看状态。</p>}
        {completion === 'denied' && <p role="status" className="mt-4">配对已拒绝。</p>}
        {lookup.status !== 'pending' && <p role="status" className="mt-4">此配对请求当前不是待处理状态，无法批准或拒绝。</p>}
        {lookup.status === 'pending' && !validExpiry && <p role="status" className="mt-4">此配对请求已过期或有效期无效，无法批准或拒绝。</p>}
      </div>}
    </section>

    <section className="cloud-card"><header><h2>实时集群状态</h2><button className="cloud-button" onClick={onRefreshClusters}>刷新状态</button></header>
      <p>只有当前组织的实时集群目录明确返回已连接时，才显示“已连接”；不会仅凭提交的集群名称推断连接状态。</p>
      {directoryError !== undefined && <CloudError error={directoryError} retry={onRefreshClusters} />}
      {directoryError === undefined && !clusters.length && <p role="status">当前没有可用的集群状态。</p>}
      {directoryError === undefined && clusters.map(cluster => <div className="cloud-live-cluster" key={cluster.id}><span>{cluster.name} · {cluster.id}</span><ClusterStatus cluster={cluster} /></div>)}
    </section>
    <CloudLink href="/clusters" className="cloud-text-button">管理集群记录 →</CloudLink>
  </div>
}

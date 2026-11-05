import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { Layers, Plus, RefreshCw } from 'lucide-react'
import { Badge } from '@maomada/k8s-ui/components/ui/Badge'
import type { PortalCluster } from './model'
import { fleetRequest, resourceItems } from './fleet'
import type { AgentContext, ClusterTools, FleetDocument, FleetGroup, SnapshotContext } from './fleet'

const stateLabels: Record<string, string> = { current: '近期采集', stale: '快照过期', unavailable: '尚未同步' }
const relationLabels: Record<string, string> = { unknown: '证据不足', declared_membership_observed: '发现联邦成员声明', candidates_only: '发现待确认关联', declared: '声明匹配', candidate: '候选关联', ambiguous: '存在歧义', stale_evidence: '证据已过期', unresolved: '未解析', federation_membership: '联邦成员', declared_distribution: '分发声明', ip_reference: 'IP 引用' }
const date = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
const message = (error: unknown) => error instanceof Error ? error.message : '请求失败，请重试。'

interface DocumentView {
  cluster: SnapshotContext
  document: FleetDocument
  title: string
  payload: unknown
}

function GroupEditor({ group, candidates, busy, onSave, onCancel }: {
  group: FleetGroup; candidates: { id: string; name: string }[]; busy: boolean
  onSave: (group: FleetGroup) => void; onCancel: () => void
}) {
  const [id, setID] = useState(group.id)
  const [name, setName] = useState(group.name)
  const [members, setMembers] = useState(group.clusterIds)
  const choices = new Map(candidates.map(cluster => [cluster.id, cluster.name]))
  for (const member of members) if (!choices.has(member)) choices.set(member, '未同步成员')
  function submit(event: FormEvent) {
    event.preventDefault()
    onSave({ id, name: name.trim(), clusterIds: members })
  }
  return <form onSubmit={submit} className="mt-5 space-y-4 border-t border-theme-border pt-5" aria-label={group.id ? '编辑多云分组' : '新建多云分组'}>
    <h3 className="font-medium">{group.id ? '编辑多云分组' : '新建多云分组'}</h3>
    <label className="block text-sm">分组 ID<input className="portal-control mt-2 w-full" value={id} onChange={event => setID(event.target.value)} required pattern="[a-z0-9][a-z0-9-]{0,62}" maxLength={63} disabled={busy || Boolean(group.id)} placeholder="production" /></label>
    <label className="block text-sm">显示名称<input className="portal-control mt-2 w-full" value={name} onChange={event => setName(event.target.value)} required maxLength={256} disabled={busy} /></label>
    <fieldset disabled={busy}><legend className="mb-2 text-sm">成员集群</legend><div className="max-h-60 space-y-2 overflow-auto rounded-lg bg-theme-base p-3">
      {[...choices].map(([clusterID, label]) => <label key={clusterID} className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={members.includes(clusterID)} onChange={event => setMembers(event.target.checked ? [...members, clusterID] : members.filter(member => member !== clusterID))} /><span className="break-all">{label} <span className="portal-mono text-xs text-theme-text-tertiary">{clusterID}</span></span></label>)}
      {!choices.size && <p className="text-sm text-theme-text-secondary">尚无注册或同步集群，可先保存空分组。</p>}
    </div></fieldset>
    <div className="flex gap-2"><button className="btn-brand px-4 py-2 text-sm" disabled={busy}>保存分组</button><button type="button" className="portal-control" onClick={onCancel} disabled={busy}>取消</button></div>
  </form>
}

export function FleetWorkspace({ registeredClusters }: { registeredClusters: PortalCluster[] }) {
  const [inventory, setInventory] = useState<SnapshotContext[]>()
  const [groups, setGroups] = useState<FleetGroup[]>()
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [error, setError] = useState('')
  const [editor, setEditor] = useState<FleetGroup>()
  const [saving, setSaving] = useState(false)
  const [scope, setScope] = useState(() => {
    const cluster = new URLSearchParams(window.location.search).get('cluster')
    return cluster ? `cluster:${cluster}` : ''
  })
  const [context, setContext] = useState<AgentContext>()
  const [toolCatalog, setToolCatalog] = useState<ClusterTools[]>()
  const [toolError, setToolError] = useState('')
  const [view, setView] = useState<DocumentView>()
  const [contextBusy, setContextBusy] = useState(false)
  const sequence = useRef(0)

  useEffect(() => {
    let active = true
    Promise.all([fleetRequest<SnapshotContext[]>('/clusters'), fleetRequest<FleetGroup[]>('/fleets')]).then(([clusters, fleets]) => {
      if (active) { setInventory(clusters); setGroups(fleets); setLoadError('') }
    }).catch(cause => { if (active) setLoadError(message(cause)) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [revision])
  useEffect(() => () => { sequence.current++ }, [])

  function clearContext(nextScope = scope) {
    sequence.current++
    setScope(nextScope)
    setContext(undefined)
    setToolCatalog(undefined)
    setToolError('')
    setView(undefined)
    setContextBusy(false)
    setError('')
  }
  function reload() { setLoading(true); setRevision(value => value + 1) }
  async function preview(nextScope: string) {
    clearContext(nextScope)
    if (!nextScope) return
    const request = sequence.current
    setContextBusy(true)
    try {
      const [kind, id] = nextScope.split(':')
      const result = await fleetRequest<AgentContext>('/context', 'POST', kind === 'fleet' ? { fleetId: id } : { clusterId: id })
      if (request === sequence.current) setContext(result)
      void fleetRequest<ClusterTools[]>('/tools', 'POST', kind === 'fleet' ? { fleetId: id } : { clusterId: id }).then(catalog => {
        if (request === sequence.current) setToolCatalog(catalog)
      }).catch(cause => { if (request === sequence.current) setToolError(message(cause)) })
    } catch (cause) { if (request === sequence.current) setError(message(cause)) }
    finally { if (request === sequence.current) setContextBusy(false) }
  }
  async function save(group: FleetGroup) {
    if (!group.name) { setError('显示名称不能为空。'); return }
    setSaving(true)
    clearContext()
    try {
      await fleetRequest(`/fleets/${encodeURIComponent(group.id)}`, 'PUT', group)
      setEditor(undefined)
      reload()
      await preview(`fleet:${group.id}`)
    } catch (cause) { setError(message(cause)) }
    finally { setSaving(false) }
  }
  async function remove(group: FleetGroup) {
    if (!window.confirm(`删除分组 ${group.name}？集群快照不会被删除。`)) return
    setSaving(true)
    clearContext('')
    try {
      await fleetRequest(`/fleets/${encodeURIComponent(group.id)}`, 'DELETE')
      setEditor(undefined)
      reload()
    } catch (cause) { setError(message(cause)) }
    finally { setSaving(false) }
  }
  function showDocument(cluster: SnapshotContext, document: FleetDocument) {
    sequence.current++
    setContextBusy(false)
    setError('')
    setView({ cluster, document, title: document.tool, payload: document.error ? { error: document.error } : document.data })
  }
  async function resource(name: string, namespace: string) {
    if (!view) return
    const source = view
    const request = ++sequence.current
    setContextBusy(true)
    setError('')
    setView({ ...source, title: `get_resource · ${name}`, payload: null })
    try {
      const result = await fleetRequest<SnapshotContext>('/resource-context', 'POST', {
        clusterId: source.cluster.clusterId, kind: source.document.input.kind,
        group: source.document.input.group || '', namespace, name,
      })
      if (request === sequence.current) setView({ ...source, cluster: result, title: `get_resource · ${name}`, payload: result.documents[0]?.error ? { error: result.documents[0].error } : result.documents[0]?.data })
    } catch (cause) { if (request === sequence.current) setError(message(cause)) }
    finally { if (request === sequence.current) setContextBusy(false) }
  }

  const candidates = new Map(registeredClusters.map(cluster => [cluster.id, { id: cluster.id, name: cluster.name }]))
  for (const cluster of inventory || []) candidates.set(cluster.clusterId, { id: cluster.clusterId, name: cluster.name || cluster.clusterId })
  const selectedID = scope.startsWith('cluster:') ? scope.slice(8) : ''
  if (selectedID && !candidates.has(selectedID)) candidates.set(selectedID, { id: selectedID, name: selectedID })
  const sources = [...candidates.values()]
  const snapshotByID = new Map((inventory || []).map(cluster => [cluster.clusterId, cluster]))

  return <div className="space-y-6">
    <div className="flex flex-wrap items-end justify-between gap-4"><div><p className="portal-mono mb-2 text-xs text-accent-text">Portal / Fleet Manager</p><h1 className="text-3xl font-semibold">多云分组与 Agent 上下文</h1><p className="mt-3 text-sm text-theme-text-secondary">在门户内组织集群同步数据。单集群保留原 KubeAtlas 语义，多云按成员提供上下文。</p></div><button className="portal-control inline-flex items-center gap-2" disabled={loading || saving} onClick={() => { clearContext(); reload() }}><RefreshCw size={16} className={loading ? 'animate-spin' : ''} />刷新同步数据</button></div>
    <p className="rounded-lg border border-theme-border bg-theme-surface p-4 text-sm text-theme-text-secondary">功能开发阶段：沿用 Portal 登录与注册通道，尚未实现细粒度权限或租户隔离。快照不是实时查询，分组不代表跨云网络互通。</p>
    {loadError && <p role="alert" className="portal-card border-warning p-4 text-sm">{loadError} {inventory ? '以下列表为上次成功读取的记录。' : '尚未读取到同步数据。'}</p>}
    <div className="grid gap-3 sm:grid-cols-3">{[
      ['已同步集群', inventory?.length], ['多云分组', groups?.length], ['快照过期', inventory?.filter(cluster => cluster.state === 'stale').length],
    ].map(([label, value]) => <div key={label} className="portal-card p-5"><p className="text-sm text-theme-text-secondary">{label}</p><p className="mt-3 text-3xl font-semibold">{value ?? '—'}</p></div>)}</div>
    <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.4fr)_minmax(320px,1fr)]">
      <section className="portal-card min-w-0 p-5" aria-label="同步集群"><h2 className="text-lg font-semibold">集群数据源</h2><p className="mt-2 text-xs text-theme-text-tertiary">目录心跳和模型快照是两个状态；已注册但未同步的成员会明确显示。</p><div className="mt-4 overflow-auto"><table className="w-full text-left text-sm"><thead className="text-xs text-theme-text-tertiary"><tr><th className="p-2">集群 / ID</th><th className="p-2">快照状态</th><th className="p-2">采集时间</th><th className="p-2">上下文</th></tr></thead><tbody>{sources.map(cluster => {
        const snapshot = snapshotByID.get(cluster.id)
        return <tr key={cluster.id} className="border-t border-theme-border"><td className="p-2"><p className="font-medium">{cluster.name}</p><p className="portal-mono break-all text-xs text-theme-text-tertiary">{cluster.id}</p></td><td className="whitespace-nowrap p-2"><Badge severity={snapshot?.state === 'stale' ? 'warning' : 'neutral'}>{snapshot ? stateLabels[snapshot.state] || snapshot.state : '尚未同步'}</Badge></td><td className="p-2 text-xs text-theme-text-secondary">{snapshot ? date(snapshot.observedAt) : '—'}</td><td className="p-2"><button className="portal-control" disabled={!snapshot || saving} onClick={() => void preview(`cluster:${cluster.id}`)}>查看</button></td></tr>
      })}</tbody></table></div>{!sources.length && <p className="py-8 text-center text-sm text-theme-text-secondary">{loading ? '正在加载…' : '等待 KubeAtlas 注册或同步。'}</p>}</section>
      <section className="portal-card p-5" aria-label="多云分组"><div className="flex items-center justify-between gap-3"><h2 className="flex items-center gap-2 text-lg font-semibold"><Layers size={20} />多云分组</h2><button className="portal-control inline-flex items-center gap-1" disabled={saving} onClick={() => { setEditor({ id: '', name: '', clusterIds: [] }); setError('') }}><Plus size={16} />新建分组</button></div>
        {groups?.map(group => <article key={group.id} className="mt-4 rounded-lg border border-theme-border p-3"><div className="flex flex-wrap justify-between gap-2"><h3 className="font-medium">{group.name}</h3><code className="break-all text-xs text-theme-text-tertiary">{group.id}</code></div><p className="my-3 break-words text-xs text-theme-text-secondary">{group.clusterIds.join(' / ') || '空分组 · 无成员'}</p><div className="flex flex-wrap gap-2"><button className="portal-control" disabled={saving} onClick={() => void preview(`fleet:${group.id}`)}>预览上下文</button><button className="portal-control" disabled={saving} onClick={() => { setEditor(group); setError('') }}>编辑</button><button className="portal-control" disabled={saving} onClick={() => void remove(group)}>删除</button></div></article>)}
        {groups?.length === 0 && <p className="py-6 text-sm text-theme-text-secondary">尚无分组，单集群可直接查询上下文。</p>}
        {editor && <GroupEditor key={editor.id} group={editor} candidates={sources} busy={saving} onSave={group => void save(group)} onCancel={() => setEditor(undefined)} />}
      </section>
    </div>
    {error && <p role="alert" className="portal-card border-warning p-4 text-sm">{error}</p>}
    <section className="portal-card min-w-0 p-5" aria-label="Agent 上下文预览"><h2 className="text-lg font-semibold">Agent 上下文预览</h2><p className="mt-2 text-sm text-theme-text-secondary">各集群原始模型与关系独立保留，不合并同名资源。</p>
      <div className="mt-5 flex flex-wrap items-end gap-3"><label className="min-w-48 flex-1 text-sm">上下文范围<select className="portal-control mt-2 w-full" value={scope} disabled={saving} onChange={event => clearContext(event.target.value)}><option value="">选择单集群或多云分组</option><optgroup label="多云分组">{groups?.map(group => <option key={group.id} value={`fleet:${group.id}`}>{group.name} · {group.id}</option>)}</optgroup><optgroup label="单集群">{sources.map(cluster => <option key={cluster.id} value={`cluster:${cluster.id}`}>{cluster.name} · {cluster.id}</option>)}</optgroup></select></label><button className="btn-brand px-4 py-3 text-sm" disabled={!scope || contextBusy || saving} onClick={() => void preview(scope)}>生成上下文</button></div>
      {selectedID && inventory && !snapshotByID.has(selectedID) && <p className="mt-4 text-sm text-theme-text-secondary">此集群尚未同步模型快照；注册心跳不能代替上下文数据。</p>}
      {contextBusy && <p role="status" className="mt-4 text-sm text-theme-text-secondary">正在读取同步数据…</p>}
      {context ? <><p className="mt-5 text-sm text-theme-text-secondary">{context.mode === 'single' ? '单集群' : '多云分组'} · {context.clusters.length} 个成员</p><div className="mt-3 grid gap-5 lg:grid-cols-[260px_minmax(0,1fr)]"><div className="max-h-[560px] space-y-4 overflow-auto">{context.clusters.map(cluster => <div key={cluster.clusterId}><h3 className="break-words font-medium">{cluster.name || cluster.clusterId}</h3><p className="portal-mono mt-1 break-all text-xs text-theme-text-tertiary">{cluster.clusterId} · {stateLabels[cluster.state] || cluster.state}</p>{cluster.state === 'unavailable' && <p className="mt-2 text-sm text-theme-text-secondary">没有可用快照，不能视为无异常。</p>}{cluster.documents.map((document, index) => <button key={index} className="portal-control mt-2 block w-full break-words text-left text-xs" onClick={() => showDocument(cluster, document)}>{document.tool}{document.input.view ? ` / ${String(document.input.view)}` : document.input.kind ? ` / ${String(document.input.kind)}` : ''}{document.error ? ' · 读取失败' : ''}</button>)}</div>)}</div><div className="min-w-0"><div className="flex flex-wrap items-center justify-between gap-3"><h3 className="text-sm font-medium">{view?.title || '完整上下文 JSON'}</h3><button className="portal-control" onClick={() => { sequence.current++; setContextBusy(false); setView(undefined); setError('') }}>完整上下文 JSON</button></div>{view && <p className="mt-3 text-xs text-theme-text-secondary">{view.cluster.clusterId} · {stateLabels[view.cluster.state] || view.cluster.state} · 采集 {date(view.cluster.observedAt)}</p>}
      {view && <div className="mt-3 flex max-h-40 flex-wrap gap-2 overflow-auto">{resourceItems(view.document).map(item => <button key={`${item.namespace}/${item.name}`} className="portal-control text-xs" disabled={contextBusy} onClick={() => void resource(item.name, item.namespace)}>{item.namespace || '(集群级)'}/{item.name}</button>)}</div>}
      <pre tabIndex={0} className="portal-mono mt-4 max-h-[480px] overflow-auto whitespace-pre-wrap break-words rounded-lg border border-theme-border bg-theme-base p-4 text-xs leading-relaxed">{JSON.stringify(view ? view.payload : context, null, 2)}</pre><details className="mt-3 text-xs text-theme-text-secondary"><summary className="cursor-pointer">数据覆盖与限制</summary><ul className="mt-2 list-disc space-y-2 pl-5">{[...context.limitations, ...context.clusters.flatMap(cluster => cluster.limitations.map(limit => `${cluster.clusterId}: ${limit}`)), ...(view?.cluster.limitations || [])].map((limit, index) => <li key={index}>{limit}</li>)}</ul></details></div></div></> : !contextBusy && <p className="py-10 text-center text-sm text-theme-text-secondary">选择范围并生成上下文。这里只展示已同步的观测数据。</p>}
    </section>
    {context?.federation && <section className="portal-card p-5" aria-label="跨集群关系"><h2 className="text-lg font-semibold">跨集群关系 · {relationLabels[context.federation.state] || context.federation.state}</h2><p className="mt-2 text-sm text-theme-text-secondary">后台按当前分组自动关联 IP 引用、Karmada 成员身份及分发声明；没有证据时不自动判定为联邦。</p>{context.federation.truncated && <p className="mt-3 text-sm text-theme-text-secondary">关系或候选目标已截断，请缩小分组后继续查看。</p>}<div className="mt-4 space-y-3">{context.federation.relationships.map((relationship, index) => <details key={index} className="rounded-lg border border-theme-border p-3 text-sm"><summary className="cursor-pointer break-words">{relationship.source.clusterId} / {relationship.source.resource.name} → {relationship.targets.map(target => target.clusterId).join(', ') || '未解析目标'} · {relationLabels[relationship.type] || relationship.type} · {relationLabels[relationship.state] || relationship.state}</summary><pre className="portal-mono mt-3 overflow-auto whitespace-pre-wrap break-words text-xs">{JSON.stringify(relationship, null, 2)}</pre></details>)}{!context.federation.relationships.length && <p className="text-sm text-theme-text-secondary">尚无可关联证据，不代表集群之间不存在关系。</p>}</div><details className="mt-4 text-xs text-theme-text-secondary"><summary className="cursor-pointer">覆盖范围与判断限制</summary><pre className="portal-mono mt-3 overflow-auto whitespace-pre-wrap break-words">{JSON.stringify(context.federation.coverage, null, 2)}</pre><ul className="mt-3 list-disc space-y-2 pl-5">{context.federation.limitations.map(limit => <li key={limit}>{limit}</li>)}</ul></details></section>}
    {context && <section className="portal-card p-5" aria-label="统一 MCP 工具目录"><h2 className="text-lg font-semibold">统一 MCP 工具目录</h2><p className="mt-2 text-sm text-theme-text-secondary">采集端主动连接 Portal；Agent 通过 list_cluster_tools 发现原始工具及参数，通过 call_cluster_tool 指定 clusterId 调用。工具通道在线状态与快照新鲜度独立。</p>{toolError && <p role="alert" className="mt-3 text-sm">{toolError}</p>}{!toolCatalog && !toolError && <p className="mt-3 text-sm text-theme-text-secondary">正在读取工具通道…</p>}{toolCatalog?.map(cluster => <details key={cluster.clusterId} className="mt-3 rounded-lg border border-theme-border p-3"><summary className="cursor-pointer text-sm">{cluster.clusterId} · {cluster.state} · {cluster.tools.length} 个工具</summary><div className="mt-3 space-y-2">{cluster.tools.map(tool => <details key={tool.name} className="rounded-lg bg-theme-base p-3 text-xs"><summary className="cursor-pointer portal-mono">{tool.name}</summary><p className="mt-2 text-theme-text-secondary">{tool.description}</p><pre className="portal-mono mt-3 overflow-auto whitespace-pre-wrap break-words">{JSON.stringify(tool.inputSchema, null, 2)}</pre></details>)}</div></details>)}</section>}
    <section className="portal-card p-5" aria-label="Fleet 接入说明"><h2 className="font-semibold">同一个 Portal，统一 Agent 接入点</h2><p className="mt-2 text-sm text-theme-text-secondary">Agent 使用本门户的 <code>/mcp-fleet</code>，通过 get_agent_context 指定 clusterId 或 fleetId；资源快照通过 get_resource_context 获取，实时工具通过 call_cluster_tool 调用。沿用门户会话，不连接独立 Fleet 服务。</p><code className="portal-mono mt-3 block break-all text-xs">{window.location.origin}/mcp-fleet</code><p className="mt-3 text-sm text-theme-text-secondary">采集端复用 KUBEATLAS_PORTAL_URL、KUBEATLAS_PORTAL_CLUSTER_ID 和 KUBEATLAS_PORTAL_TOKEN 配置，设置 KUBEATLAS_FLEET_SYNC_ENABLED=true。只需采集端出站访问门户，无需中心直连客户端。完整参数见 docs/fleet.md。</p></section>
  </div>
}

import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Check, Copy, KeyRound, RefreshCw, ShieldCheck } from 'lucide-react'
import { Badge } from '@maomada/k8s-ui/components/ui/Badge'
import { Disclosure } from '@maomada/k8s-ui/components/ui/Disclosure'
import { copyText } from '@maomada/k8s-ui/utils/clipboard'
import { credentialClusters, credentialStatus, credentialsRequest, openCodeCloudConfig } from './credentials'
import type { CredentialCluster, IssuedCredential, MCPCredential } from './credentials'
import { ConnectionProfileSelector, useConnectionProfiles } from './ConnectionProfiles'

const statusLabels = { active: '有效', expired: '已过期', revoked: '已撤销', unknown: '有效期未知' }
const statusTones = { active: 'success', expired: 'warning', revoked: 'neutral', unknown: 'warning' } as const
const formatDate = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未知'
const errorMessage = (error: unknown) => error instanceof Error ? error.message : '请求失败，请重试。'

function CopyValue({ value, label }: { value: string; label: string }) {
  const [status, setStatus] = useState('')
  useEffect(() => {
    if (!status) return
    const timeout = window.setTimeout(() => setStatus(''), 4000)
    return () => window.clearTimeout(timeout)
  }, [status])
  return <div className="space-y-1">
    <button type="button" className="portal-control inline-flex items-center gap-2" onClick={async () => {
      try { setStatus(await copyText(value) ? '已复制' : '复制失败，请手动复制。') }
      catch { setStatus('复制失败，请手动复制。') }
    }}>{status === '已复制' ? <Check size={15} /> : <Copy size={15} />}{label}</button>
    {status && <p role="status" className="text-xs text-theme-text-secondary">{status}</p>}
  </div>
}

export function MCPCredentials({ readOnlyOnly = false, organizationId, organizationName }: {
  readOnlyOnly?: boolean; organizationId: string; organizationName: string
}) {
  const profile = useConnectionProfiles()
  const [clusters, setClusters] = useState<CredentialCluster[]>([])
  const [directoryError, setDirectoryError] = useState('')
  const [directoryLoading, setDirectoryLoading] = useState(true)
  const [credentials, setCredentials] = useState<MCPCredential[]>()
  const [loadError, setLoadError] = useState('')
  const [actionError, setActionError] = useState('')
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [label, setLabel] = useState('OpenCode')
  const [clusterIds, setClusterIds] = useState<string[]>([])
  const [readOnly, setReadOnly] = useState(true)
  const [ttlSeconds, setTTL] = useState(28800)
  const [issued, setIssued] = useState<IssuedCredential>()
  const [revokeID, setRevokeID] = useState('')
  const [now, setNow] = useState(NaN)
  const clock = useRef({ server: NaN, received: 0 })
  const availableIDs = new Set(clusters.map(cluster => cluster.id))
  const selectedIDs = clusterIds.filter(id => availableIDs.has(id))
  const clusterLabel = (id: string) => {
    const cluster = clusters.find(item => item.id === id)
    if (!cluster) return id
    return clusters.filter(item => item.name === cluster.name).length > 1 ? `${cluster.name} · ${id}` : cluster.name
  }

  useEffect(() => {
    const timer = window.setInterval(() => setNow(clock.current.server + Date.now() - clock.current.received), 10000)
    return () => window.clearInterval(timer)
  }, [])
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setDirectoryLoading(true)
    setClusters([])
    credentialClusters(controller.signal).then(data => {
      if (controller.signal.aborted) return
      setClusters(data)
      setDirectoryError('')
    }).catch(error => {
      if (!controller.signal.aborted) setDirectoryError(errorMessage(error))
    }).finally(() => { if (!controller.signal.aborted) setDirectoryLoading(false) })
    credentialsRequest<{ credentials: MCPCredential[]; serverTime: string }>('', 'GET', undefined, controller.signal).then(data => {
      if (controller.signal.aborted) return
      if (!Array.isArray(data.credentials) || !Number.isFinite(Date.parse(data.serverTime))) throw new Error('门户返回的凭据列表无效。')
      clock.current = { server: Date.parse(data.serverTime), received: Date.now() }
      setNow(clock.current.server)
      setCredentials(data.credentials)
      setLoadError('')
    }).catch(error => { if (!controller.signal.aborted) setLoadError(errorMessage(error)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [revision])

  async function create(event: FormEvent) {
    event.preventDefault()
    if (busy || issued || loading || directoryLoading || loadError || directoryError || !selectedIDs.length || !label.trim()) return
    setBusy('create')
    setActionError('')
    try {
      const result = await credentialsRequest<IssuedCredential>('', 'POST', { label: label.trim(), clusterIds: selectedIDs, readOnly: readOnlyOnly || readOnly, ttlSeconds })
      if (!result.token || !result.credential?.id) throw new Error('未收到完整凭据，请刷新列表核对。')
      setIssued(result)
      setRevision(value => value + 1)
    } catch (error) {
      setActionError(`${errorMessage(error)} 若请求中断，生成结果可能未确认；请刷新列表，撤销不再需要的凭据后重新生成。`)
    } finally { setBusy('') }
  }

  async function revoke(id: string) {
    setBusy(id)
    setActionError('')
    try {
      await credentialsRequest(`/${encodeURIComponent(id)}/revoke`, 'POST', {})
      if (issued?.credential.id === id) setIssued(undefined)
      setRevokeID('')
      setRevision(value => value + 1)
    } catch (error) { setActionError(`${errorMessage(error)} 撤销结果未确认，请刷新列表核对。`) }
    finally { setBusy('') }
  }

  let config = ''
  let endpointError = ''
  if (!profile.mcpURL) endpointError = '所选局域网/公网配置未填写 MCP 地址。'
  else { try { config = openCodeCloudConfig(profile.mcpURL) } catch (error) { endpointError = errorMessage(error) } }

  return <div className="space-y-6">
    <div className="cloud-page-heading"><div><h1>Agent</h1><p>仅可访问你在当前组织获授权的集群，操作受你的组织和集群权限约束。</p></div></div>
    {actionError && <p role="alert" className="portal-card border-warning p-4 text-sm">{actionError}</p>}
    {issued && <section className="portal-card border-accent p-5" aria-label="新生成的 MCP 凭据">
      <h2 className="flex items-center gap-2 font-semibold"><KeyRound size={19} className="text-accent-text" />凭据已生成 · 原文仅此次可见</h2>
      <p className="mt-2 text-sm text-theme-text-secondary">请保存 Token，关闭后无法再次查看。有效至 {formatDate(issued.credential.expiresAt)}。</p>
      <label className="mt-4 block text-sm">MCP Token<input aria-label="新生成的 MCP Token" type="password" readOnly autoComplete="off" spellCheck={false} className="portal-control portal-mono mt-2 w-full" value={issued.token} onFocus={event => event.target.select()} /></label>
      <div className="mt-4 flex flex-wrap items-start gap-3"><CopyValue value={issued.token} label="复制 Token" /><button type="button" className="portal-control" onClick={() => setIssued(undefined)}>已保存，关闭原文</button></div>
    </section>}
    <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-2">
      <section className="portal-card min-w-0 p-5 lg:p-6" aria-labelledby="create-credential-title">
        <h2 id="create-credential-title" className="flex items-center gap-2 text-lg font-semibold"><ShieldCheck size={20} className="text-accent-text" />生成访问凭据</h2>
        <p className="mt-3 text-sm text-theme-text-secondary">当前组织：<strong>{organizationName}</strong></p>
        <form onSubmit={create} className="mt-5 space-y-4">
          <fieldset disabled={Boolean(busy || issued || loading || directoryLoading || loadError || directoryError)} className="min-w-0 space-y-4">
            <legend className="sr-only">MCP 凭据配置</legend>
            <label className="block text-sm">名称<input required maxLength={100} value={label} onChange={event => setLabel(event.target.value)} className="portal-control mt-2 w-full" placeholder="例如：我的 OpenCode" /></label>
            <label className="block text-sm">有效期<select value={ttlSeconds} onChange={event => setTTL(Number(event.target.value))} className="portal-control mt-2 w-full"><option value={900}>15 分钟</option><option value={3600}>1 小时</option><option value={28800}>8 小时</option><option value={86400}>24 小时</option><option value={604800}>7 天</option></select></label>
            <fieldset className="min-w-0"><legend className="mb-2 text-sm">允许访问的集群 · 至少选择一个</legend>
              {directoryError && <p role="alert" className="mb-2 text-xs text-warning-text">集群目录读取失败：{directoryError}。请恢复连接后刷新。</p>}
              <div className="max-h-60 space-y-2 overflow-auto rounded-lg border border-theme-border bg-theme-base p-3">
                {clusters.map(cluster => <label key={cluster.id} className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={selectedIDs.includes(cluster.id)} onChange={event => setClusterIds(event.target.checked ? [...selectedIDs, cluster.id] : selectedIDs.filter(id => id !== cluster.id))} /><span className="min-w-0 break-words">{clusterLabel(cluster.id)}</span></label>)}
                {directoryLoading ? <p role="status" className="text-sm text-theme-text-secondary">正在读取 Hub 集群目录…</p> : !clusters.length && !directoryError && <p className="text-sm text-theme-text-secondary">尚无可选集群，请先在 Hub 中配置集群。</p>}
              </div>
            </fieldset>
            <label className="block text-sm">操作范围<select value={readOnlyOnly || readOnly ? 'read' : 'write'} disabled={readOnlyOnly} onChange={event => setReadOnly(event.target.value === 'read')} className="portal-control mt-2 w-full"><option value="read">只读 · 查询与诊断</option>{!readOnlyOnly && <option value="write">读写 · 查询与修改</option>}</select></label>
            <p className="text-xs text-theme-text-secondary">{readOnlyOnly || readOnly ? '仅查询所选集群。' : '可修改所选集群中你有权限的资源。'}</p>
            <button type="submit" className="btn-brand inline-flex items-center gap-2 px-4 py-2.5 text-sm" disabled={!selectedIDs.length || !label.trim() || Boolean(directoryError)}><KeyRound size={16} />{busy === 'create' ? '正在生成…' : '生成限时 Token'}</button>
          </fieldset>
          {issued && <p className="text-xs text-theme-text-secondary">请先保存并关闭上方原文，再生成其他凭据。</p>}
        </form>
      </section>
      <section className="portal-card min-w-0 p-5 lg:p-6" aria-labelledby="client-config-title">
        <h2 id="client-config-title" className="text-lg font-semibold">接入 OpenCode</h2>
        <ConnectionProfileSelector />
        <div className="cloud-endpoint"><code>{profile.mcpURL || '未配置 MCP 地址'}</code>{profile.mcpURL && <CopyValue value={profile.mcpURL} label="复制端点" />}</div>
        {endpointError ? <p role="alert" className="mt-2 text-sm text-warning-text">{endpointError}</p> : <>
          <Disclosure className="mt-5" summary="查看客户端配置" summaryClassName="py-2 text-sm text-accent-text">
          <p className="mt-3 text-sm text-theme-text-secondary">将 Token 填入环境变量 <code className="portal-mono">KUBEATLAS_MCP_TOKEN</code>，复制以下配置后重启客户端。</p>
          <pre className="portal-mono my-4 overflow-auto rounded-lg bg-theme-base p-4 text-xs leading-relaxed" aria-label="OpenCode 配置">{config}</pre>
          <CopyValue value={config} label="复制 OpenCode 配置" />
          </Disclosure>
        </>}
      </section>
    </div>
    <section className="portal-card p-5 lg:p-6" aria-label="已签发凭据">
      <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="text-lg font-semibold">已签发凭据</h2><button type="button" className="portal-control inline-flex items-center gap-2" disabled={loading || Boolean(busy)} onClick={() => setRevision(value => value + 1)}><RefreshCw size={15} className={loading ? 'animate-spin' : ''} />刷新列表</button></div>
      {loadError && <p role="alert" className="mt-4 text-sm text-warning-text">{loadError}{credentials ? ' 以下是上次读取的记录，当前状态未确认。' : ''}</p>}
      {!credentials && loading && <p role="status" className="py-8 text-sm text-theme-text-secondary">正在读取凭据…</p>}
      {credentials?.length === 0 && <p className="py-8 text-sm text-theme-text-secondary">尚未签发凭据。选择集群与有效期后生成第一个 Token。</p>}
      <div className="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-2">
        {credentials?.map(credential => {
          const status = credentialStatus(credential, now)
          return <article key={credential.id} className="rounded-lg border border-theme-border p-4" aria-label={`凭据 ${credential.label}`}>
            <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="break-all font-medium">{credential.label}</h3><div className="flex gap-2"><Badge tone={credential.readOnly ? 'accent1' : 'accent2'}>{credential.readOnly ? '只读' : '读写'}</Badge><Badge severity={loadError ? 'neutral' : statusTones[status]}>{loadError ? '状态待确认' : statusLabels[status]}</Badge></div></div>
            <dl className="mt-3 space-y-2 text-xs"><div><dt className="text-theme-text-tertiary">组织</dt><dd className="mt-1 break-all">{credential.organizationId === organizationId ? organizationName : credential.organizationId || '未知'}</dd></div><div><dt className="text-theme-text-tertiary">允许的集群</dt><dd className="mt-1 break-all">{credential.clusterIds.map(clusterLabel).join(' · ')}</dd></div><div><dt className="text-theme-text-tertiary">到期时间</dt><dd className="mt-1"><time dateTime={credential.expiresAt}>{formatDate(credential.expiresAt)}</time></dd></div></dl>
            {!credential.revokedAt && <div className="mt-4 border-t border-theme-border pt-3">{revokeID === credential.id ? <div className="space-y-2"><p className="text-xs text-warning-text">确认撤销？使用此凭据的客户端将无法发起新请求。</p><div className="flex gap-2"><button type="button" className="portal-control" disabled={Boolean(busy)} onClick={() => revoke(credential.id)}>确认撤销</button><button type="button" className="portal-control" disabled={Boolean(busy)} onClick={() => setRevokeID('')}>取消</button></div></div> : <button type="button" className="text-xs text-accent-text" disabled={Boolean(busy)} onClick={() => setRevokeID(credential.id)}>撤销凭据</button>}</div>}
          </article>
        })}
      </div>
    </section>
  </div>
}

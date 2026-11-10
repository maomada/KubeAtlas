import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Check, Copy, KeyRound, RefreshCw, ShieldCheck } from 'lucide-react'
import { Badge } from '@maomada/k8s-ui/components/ui/Badge'
import { credentialClusters, credentialStatus, credentialsRequest, openCodeFleetConfig } from './credentials'
import type { CredentialCluster, IssuedCredential, MCPCredential } from './credentials'

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
      try { await navigator.clipboard.writeText(value); setStatus('已复制') }
      catch { setStatus('复制失败，请手动选择文本；浏览器剪贴板需要 HTTPS。') }
    }}>{status === '已复制' ? <Check size={15} /> : <Copy size={15} />}{label}</button>
    {status && <p role="status" className="text-xs text-theme-text-secondary">{status}</p>}
  </div>
}

export function MCPCredentials({ readOnlyOnly = false }: { readOnlyOnly?: boolean }) {
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
  const [endpoint, setEndpoint] = useState(`${window.location.origin}/mcp-fleet`)
  const [now, setNow] = useState(NaN)
  const clock = useRef({ server: NaN, received: 0 })
  const availableIDs = new Set(clusters.map(cluster => cluster.id))
  const selectedIDs = clusterIds.filter(id => availableIDs.has(id))

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
  try { config = openCodeFleetConfig(endpoint) } catch (error) { endpointError = errorMessage(error) }

  return <div className="space-y-6">
    <div><p className="portal-mono mb-2 text-xs text-accent-text">统一 MCP · Streamable HTTP</p><h1 className="text-3xl font-semibold">MCP 访问凭据</h1><p className="mt-3 max-w-3xl text-sm leading-relaxed text-theme-text-secondary">在这里生成独立的限时 Token，不再复制网页登录 Cookie。凭据重启后仍有效，可限定集群和读写范围，并随时撤销。</p></div>
    {actionError && <p role="alert" className="portal-card border-warning p-4 text-sm">{actionError}</p>}
    {issued && <section className="portal-card border-accent p-5" aria-label="新生成的 MCP 凭据">
      <h2 className="flex items-center gap-2 font-semibold"><KeyRound size={19} className="text-accent-text" />凭据已生成 · 原文仅此次可见</h2>
      <p className="mt-2 text-sm text-theme-text-secondary">有效至 {formatDate(issued.credential.expiresAt)}。关闭、离开或刷新页面后无法再次读取原文；请存入客户端的安全环境变量，不要提交到仓库或聊天。</p>
      <label className="mt-4 block text-sm">MCP Token<input aria-label="新生成的 MCP Token" type="password" readOnly autoComplete="off" spellCheck={false} className="portal-control portal-mono mt-2 w-full" value={issued.token} onFocus={event => event.target.select()} /></label>
      <div className="mt-4 flex flex-wrap items-start gap-3"><CopyValue value={issued.token} label="复制 Token" /><button type="button" className="portal-control" onClick={() => setIssued(undefined)}>已保存，关闭原文</button></div>
    </section>}
    <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-2">
      <section className="portal-card min-w-0 p-5 lg:p-6" aria-labelledby="create-credential-title">
        <h2 id="create-credential-title" className="flex items-center gap-2 text-lg font-semibold"><ShieldCheck size={20} className="text-accent-text" />生成访问凭据</h2>
        <form onSubmit={create} className="mt-5 space-y-4">
          <fieldset disabled={Boolean(busy || issued || loading || directoryLoading || loadError || directoryError)} className="min-w-0 space-y-4">
            <label className="block text-sm">名称<input required maxLength={100} value={label} onChange={event => setLabel(event.target.value)} className="portal-control mt-2 w-full" placeholder="例如：我的 OpenCode" /></label>
            <label className="block text-sm">有效期<select value={ttlSeconds} onChange={event => setTTL(Number(event.target.value))} className="portal-control mt-2 w-full"><option value={900}>15 分钟</option><option value={3600}>1 小时</option><option value={28800}>8 小时</option><option value={86400}>24 小时</option><option value={604800}>7 天</option></select></label>
            <fieldset className="min-w-0"><legend className="mb-2 text-sm">允许访问的集群 · 至少选择一个</legend>
              {directoryError && <p role="alert" className="mb-2 text-xs text-warning-text">集群目录读取失败：{directoryError}。请恢复连接后刷新。</p>}
              <div className="max-h-60 space-y-2 overflow-auto rounded-lg border border-theme-border bg-theme-base p-3">
                {clusters.map(cluster => <label key={cluster.id} className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={selectedIDs.includes(cluster.id)} onChange={event => setClusterIds(event.target.checked ? [...selectedIDs, cluster.id] : selectedIDs.filter(id => id !== cluster.id))} /><span className="min-w-0 break-words">{cluster.name}<span className="portal-mono ml-2 text-xs text-theme-text-tertiary">{cluster.id}</span></span></label>)}
                {directoryLoading ? <p role="status" className="text-sm text-theme-text-secondary">正在读取 Hub 集群目录…</p> : !clusters.length && !directoryError && <p className="text-sm text-theme-text-secondary">尚无可选集群，请先在 Hub 中配置集群。</p>}
              </div>
            </fieldset>
            <label className="block text-sm">操作范围<select value={readOnlyOnly || readOnly ? 'read' : 'write'} disabled={readOnlyOnly} onChange={event => setReadOnly(event.target.value === 'read')} className="portal-control mt-2 w-full"><option value="read">只读 · 查询与诊断</option>{!readOnlyOnly && <option value="write">读写 · 允许来源提供的写操作</option>}</select></label>
            <p className="text-xs leading-relaxed text-theme-text-secondary">{readOnly ? '拒绝写工具和会创建资源的诊断选项。' : '这是可写凭据，Agent 可以修改所选真实集群中的资源。'} 实际操作仍受来源 MCP 模式及 Kubernetes 权限限制。</p>
            <button type="submit" className="btn-brand inline-flex items-center gap-2 px-4 py-2.5 text-sm" disabled={!selectedIDs.length || !label.trim() || Boolean(directoryError)}><KeyRound size={16} />{busy === 'create' ? '正在生成…' : '生成限时 Token'}</button>
          </fieldset>
          {issued && <p className="text-xs text-theme-text-secondary">请先保存并关闭上方原文，再生成其他凭据。</p>}
        </form>
      </section>
      <section className="portal-card min-w-0 p-5 lg:p-6" aria-labelledby="client-config-title">
        <h2 id="client-config-title" className="text-lg font-semibold">接入 OpenCode</h2>
        <p className="mt-3 text-sm leading-relaxed text-theme-text-secondary">在运行 OpenCode 的机器上安全设置环境变量 <code className="portal-mono">KUBEATLAS_MCP_TOKEN</code>，值为刚生成的 Token；重启客户端以加载新值。以下配置不包含凭据原文。</p>
        <label className="mt-4 block text-sm">聚合 MCP 地址<input type="url" value={endpoint} onChange={event => setEndpoint(event.target.value)} className="portal-control portal-mono mt-2 w-full" spellCheck={false} /></label>
        {endpointError ? <p role="alert" className="mt-2 text-sm text-warning-text">{endpointError}</p> : <>
          {endpoint.startsWith('http:') && <p role="status" className="mt-3 text-xs text-warning-text">HTTP 会明文传输 Token，仅用于可信内网。请使用部署时配置的 HTTP 入口，不能只将 HTTPS 地址替换协议。</p>}
          <pre className="portal-mono my-4 overflow-auto rounded-lg bg-theme-base p-4 text-xs leading-relaxed" aria-label="OpenCode 配置">{config}</pre>
          <CopyValue value={config} label="复制 OpenCode 配置" />
        </>}
        <div className="mt-5 space-y-2 border-t border-theme-border pt-4 text-xs leading-relaxed text-theme-text-secondary"><p>将配置合并到 OpenCode 的 <code className="portal-mono">mcp</code> 配置项。<code className="portal-mono">oauth: false</code> 不会关闭服务端认证；此入口不支持 OAuth 授权命令。</p><p>凭据失效会明确返回过期或撤销原因。有效凭据可通过刷新 API 轮换，最长延续至首次签发后的 30 天；旧 Token 随即失效，客户端需更新环境变量。到期或撤销后，请在此页面重新生成。</p><p className="font-medium text-theme-text-primary">凭据状态与刷新 API</p><p>使用当前 Bearer Token 请求 Portal 的 <code className="portal-mono">GET /api/mcp/credentials/current</code> 查看有效期，或 <code className="portal-mono">POST /api/mcp/credentials/refresh</code> 轮换。保存新响应中的 Token 后再更新客户端；OpenCode 不会自动调用此刷新接口。</p></div>
      </section>
    </div>
    <section className="portal-card p-5 lg:p-6" aria-label="已签发凭据">
      <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="text-lg font-semibold">已签发凭据</h2><button type="button" className="portal-control inline-flex items-center gap-2" disabled={loading || Boolean(busy)} onClick={() => setRevision(value => value + 1)}><RefreshCw size={15} className={loading ? 'animate-spin' : ''} />刷新列表</button></div>
      <p className="mt-2 text-xs text-theme-text-secondary">列表仅显示元数据，不保存 Token 原文。撤销不会停止已经发出的集群操作。</p>
      {loadError && <p role="alert" className="mt-4 text-sm text-warning-text">{loadError}{credentials ? ' 以下是上次读取的记录，当前状态未确认。' : ''}</p>}
      {!credentials && loading && <p role="status" className="py-8 text-sm text-theme-text-secondary">正在读取凭据…</p>}
      {credentials?.length === 0 && <p className="py-8 text-sm text-theme-text-secondary">尚未签发凭据。选择集群与有效期后生成第一个 Token。</p>}
      <div className="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-2">
        {credentials?.map(credential => {
          const status = credentialStatus(credential, now)
          return <article key={credential.id} className="rounded-lg border border-theme-border p-4" aria-label={`凭据 ${credential.label}`}>
            <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="break-all font-medium">{credential.label}</h3><div className="flex gap-2"><Badge tone={credential.readOnly ? 'accent1' : 'accent2'}>{credential.readOnly ? '只读' : '读写'}</Badge><Badge severity={loadError ? 'neutral' : statusTones[status]}>{loadError ? '状态待确认' : statusLabels[status]}</Badge></div></div>
            <dl className="mt-3 space-y-2 text-xs"><div><dt className="text-theme-text-tertiary">允许的集群</dt><dd className="portal-mono mt-1 break-all">{credential.clusterIds.join(' · ')}</dd></div><div><dt className="text-theme-text-tertiary">到期时间</dt><dd className="mt-1"><time dateTime={credential.expiresAt}>{formatDate(credential.expiresAt)}</time></dd></div><div><dt className="text-theme-text-tertiary">最迟可延续至</dt><dd className="mt-1">{formatDate(credential.refreshUntil)}</dd></div></dl>
            {!credential.revokedAt && <div className="mt-4 border-t border-theme-border pt-3">{revokeID === credential.id ? <div className="space-y-2"><p className="text-xs text-warning-text">确认撤销？使用此凭据的客户端将无法发起新请求。</p><div className="flex gap-2"><button type="button" className="portal-control" disabled={Boolean(busy)} onClick={() => revoke(credential.id)}>确认撤销</button><button type="button" className="portal-control" disabled={Boolean(busy)} onClick={() => setRevokeID('')}>取消</button></div></div> : <button type="button" className="text-xs text-accent-text" disabled={Boolean(busy)} onClick={() => setRevokeID(credential.id)}>撤销凭据</button>}</div>}
          </article>
        })}
      </div>
    </section>
  </div>
}

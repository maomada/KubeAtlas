import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Check, Copy, KeyRound, RefreshCw, ShieldCheck } from 'lucide-react'
import { Badge } from '@maomada/k8s-ui/components/ui/Badge'
import { useLocale } from '@maomada/k8s-ui/hooks/useLocale'
import { copyText } from '@maomada/k8s-ui/utils/clipboard'
import { credentialClusters, credentialStatus, credentialsRequest, mcpConnectionInfo } from './credentials'
import type { CredentialCluster, IssuedCredential, MCPCredential } from './credentials'
import { ConnectionProfileSelector, useConnectionProfiles } from './ConnectionProfiles'
import { cloudErrorMessage, LocalizedCloudError } from './cloudApi'

const statusTones = { active: 'success', expired: 'warning', revoked: 'neutral', unknown: 'warning' } as const
const requestError = (error: unknown) => error instanceof Error ? error : new LocalizedCloudError('请求失败，请重试。', 'Request failed. Try again.')

function CredentialExpiration({ credential }: { credential: MCPCredential }) {
  const { t, locale } = useLocale()
  if (credential.neverExpires === true) return t('永不过期', 'Never expires')
  const expiration = new Date(credential.expiresAt ?? '')
  if (!Number.isFinite(expiration.getTime())) return t('未知', 'Unknown')
  return <time dateTime={credential.expiresAt}>{expiration.toLocaleString(locale, { hour12: false })}</time>
}

function CopyValue({ value, label }: { value: string; label: string }) {
  const { t } = useLocale()
  const [status, setStatus] = useState<{ copied: boolean; repeat: boolean }>()
  const timeout = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(timeout.current), [])

  async function copy() {
    const copied = await copyText(value).catch(() => false)
    setStatus(current => ({ copied, repeat: current?.copied === copied && !current.repeat }))
    window.clearTimeout(timeout.current)
    timeout.current = window.setTimeout(() => setStatus(undefined), 4000)
  }

  const message = status?.copied ? t('已复制', 'Copied') : t('复制失败，请手动复制。', 'Copy failed. Copy the text manually.')
  return <div className="space-y-1">
    <button type="button" className="portal-control inline-flex min-h-6 min-w-6 items-center gap-2" onClick={copy}>
      {status?.copied ? <Check size={15} aria-hidden="true" /> : <Copy size={15} aria-hidden="true" />}{label}
    </button>
    <p role="status" aria-atomic="true" className="text-xs text-theme-text-secondary">{status && (status.repeat ? `${message}\u00a0` : message)}</p>
  </div>
}

export function MCPCredentials({ readOnlyOnly = false, organizationId, organizationName }: {
  readOnlyOnly?: boolean; organizationId: string; organizationName: string
}) {
  const { t } = useLocale()
  const profile = useConnectionProfiles()
  const [clusters, setClusters] = useState<CredentialCluster[]>([])
  const [directoryError, setDirectoryError] = useState<Error>()
  const [directoryLoading, setDirectoryLoading] = useState(true)
  const [credentials, setCredentials] = useState<MCPCredential[]>()
  const [loadError, setLoadError] = useState<Error>()
  const [actionError, setActionError] = useState<{ action: 'create' | 'revoke'; error: Error }>()
  const [revision, setRevision] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [label, setLabel] = useState('')
  const [clusterIds, setClusterIds] = useState<string[]>([])
  const [readOnly, setReadOnly] = useState(true)
  const [ttlSeconds, setTTL] = useState(28800)
  const [issued, setIssued] = useState<IssuedCredential>()
  const [revokeID, setRevokeID] = useState('')
  const [now, setNow] = useState(NaN)
  const clock = useRef({ server: NaN, received: 0 })
  const confirmedRevokedIDs = useRef(new Set<string>())
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
      setDirectoryError(undefined)
    }).catch(error => {
      if (!controller.signal.aborted) setDirectoryError(requestError(error))
    }).finally(() => { if (!controller.signal.aborted) setDirectoryLoading(false) })
    credentialsRequest<{ credentials: MCPCredential[]; serverTime: string }>('', 'GET', undefined, controller.signal).then(data => {
      if (controller.signal.aborted) return
      if (!Array.isArray(data.credentials) || !Number.isFinite(Date.parse(data.serverTime))) {
        throw new LocalizedCloudError('门户返回的凭据列表无效。', 'The Hub returned an invalid credential list.')
      }
      clock.current = { server: Date.parse(data.serverTime), received: Date.now() }
      setNow(clock.current.server)
      setCredentials(data.credentials.filter(credential => !credential.revokedAt && !confirmedRevokedIDs.current.has(credential.id)))
      setIssued(current => current && data.credentials.some(credential => credential.id === current.credential.id && credential.revokedAt) ? undefined : current)
      setLoadError(undefined)
    }).catch(error => { if (!controller.signal.aborted) setLoadError(requestError(error)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [revision])

  async function create(event: FormEvent) {
    event.preventDefault()
    if (busy || issued || loading || directoryLoading || loadError || directoryError || !selectedIDs.length || !label.trim()) return
    setBusy('create')
    setActionError(undefined)
    try {
      const validity = ttlSeconds === 0 ? { neverExpires: true } : { ttlSeconds }
      const result = await credentialsRequest<IssuedCredential>('', 'POST', { label: label.trim(), clusterIds: selectedIDs, readOnly: readOnlyOnly || readOnly, ...validity })
      if (!result.token || !result.credential?.id) {
        throw new LocalizedCloudError('未收到完整凭据，请刷新列表核对。', 'Credential response incomplete. Refresh the list to check.')
      }
      setIssued(result)
      setRevision(value => value + 1)
    } catch (error) {
      setActionError({ action: 'create', error: requestError(error) })
    } finally { setBusy('') }
  }

  async function revoke(id: string) {
    setBusy(id)
    setActionError(undefined)
    try {
      await credentialsRequest(`/${encodeURIComponent(id)}/revoke`, 'POST', {})
      confirmedRevokedIDs.current.add(id)
      setCredentials(current => current?.filter(credential => credential.id !== id))
      if (issued?.credential.id === id) setIssued(undefined)
      setRevokeID('')
      setRevision(value => value + 1)
    } catch (error) { setActionError({ action: 'revoke', error: requestError(error) }) }
    finally { setBusy('') }
  }

  let connectionInfo = ''
  let endpointError = ''
  if (!profile.mcpURL) endpointError = t('所选局域网/公网配置未填写 MCP 地址。', 'The selected connection profile has no MCP address.')
  else { try { connectionInfo = mcpConnectionInfo(profile.mcpURL, issued?.token ?? '<Token>') } catch (error) { endpointError = cloudErrorMessage(requestError(error), t) } }
  const statusLabels = {
    active: t('有效', 'Active'),
    expired: t('已过期', 'Expired'),
    revoked: t('已撤销', 'Revoked'),
    unknown: t('有效期未知', 'Expiry unknown'),
  }

  return <div className="space-y-6">
    <div className="cloud-page-heading"><div><h1>Agent</h1><p>{t('仅可访问你在当前组织获授权的集群，操作受你的组织和集群权限约束。', 'Access is limited to authorized clusters in this organization and governed by your organization and cluster permissions.')}</p></div></div>
    {actionError && <p role="alert" className="portal-card border-warning p-4 text-sm">
      {cloudErrorMessage(actionError.error, t)}
      {actionError.action === 'create'
        ? t(' 若请求中断，生成结果可能未确认；请刷新列表，撤销不再需要的凭据后重新生成。', ' The request may have created a credential. Refresh the list and revoke any unwanted credential before trying again.')
        : t(' 撤销结果未确认，请刷新列表核对。', ' Refresh the list to check whether the credential was revoked.')}
    </p>}
    {issued && <section className="portal-card border-accent p-5" aria-label={t('新生成的 MCP 凭据', 'New MCP credential')}>
      <h2 className="flex items-center gap-2 font-semibold"><KeyRound size={19} className="text-accent-text" />{t('凭据已生成 · 原文仅此次可见', 'Credential created · Token shown once')}</h2>
      <p className="mt-2 text-sm text-theme-text-secondary">
        {t('请保存 Token，关闭后无法再次查看。有效期：', 'Save this token before closing. Validity: ')}
        <CredentialExpiration credential={issued.credential} />{t('。', '.')}
      </p>
      <label className="mt-4 block text-sm">MCP Token<input aria-label={t('新生成的 MCP Token', 'New MCP token')} type="password" readOnly autoComplete="off" spellCheck={false} className="portal-control portal-mono mt-2 w-full" value={issued.token} onFocus={event => event.target.select()} /></label>
      <div className="mt-4 flex flex-wrap items-start gap-3">
        {!endpointError && <CopyValue value={connectionInfo} label={t('复制连接信息', 'Copy connection details')} />}
        <CopyValue value={issued.token} label={t('复制 Token', 'Copy token')} />
        <button type="button" className="portal-control" onClick={() => setIssued(undefined)}>{t('已保存，关闭原文', 'Close token')}</button>
      </div>
    </section>}
    <div className="grid grid-cols-1 items-start gap-6 md:grid-cols-2">
      <section className="portal-card min-w-0 p-5 lg:p-6" aria-labelledby="create-credential-title">
        <h2 id="create-credential-title" className="flex items-center gap-2 text-lg font-semibold"><ShieldCheck size={20} className="text-accent-text" />{t('生成访问凭据', 'Create access credential')}</h2>
        <p className="mt-3 text-sm text-theme-text-secondary">{t('当前组织：', 'Current organization: ')}<strong>{organizationName}</strong></p>
        <form onSubmit={create} className="mt-5 space-y-4">
          <fieldset disabled={Boolean(busy || issued || loading || directoryLoading || loadError || directoryError)} className="min-w-0 space-y-4">
            <legend className="sr-only">{t('MCP 凭据配置', 'MCP credential settings')}</legend>
            <label className="block text-sm">{t('名称', 'Name')}<input required maxLength={100} value={label} onChange={event => setLabel(event.target.value)} className="portal-control mt-2 w-full" placeholder={t('输入凭据名称', 'Enter a credential name')} /></label>
            <label className="block text-sm">{t('有效期', 'Validity')}
              <select value={ttlSeconds} onChange={event => setTTL(Number(event.target.value))} className="portal-control mt-2 w-full">
                <option value={900}>{t('15 分钟', '15 minutes')}</option>
                <option value={3600}>{t('1 小时', '1 hour')}</option>
                <option value={28800}>{t('8 小时', '8 hours')}</option>
                <option value={86400}>{t('24 小时', '24 hours')}</option>
                <option value={604800}>{t('7 天', '7 days')}</option>
                <option value={0}>{t('永不过期', 'Never expires')}</option>
              </select>
            </label>
            <fieldset className="min-w-0"><legend className="mb-2 text-sm">{t('允许访问的集群 · 至少选择一个', 'Allowed clusters · Select at least one')}</legend>
              {directoryError && <p role="alert" className="mb-2 text-xs text-warning-text">
                {t('集群目录读取失败：', 'Could not load the cluster directory: ')}{cloudErrorMessage(directoryError, t)}{t('。请恢复连接后刷新。', ' Refresh once the connection is restored.')}
              </p>}
              <div className="max-h-60 space-y-2 overflow-auto rounded-lg border border-theme-border bg-theme-base p-3">
                {clusters.map(cluster => <label key={cluster.id} className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={selectedIDs.includes(cluster.id)} onChange={event => setClusterIds(event.target.checked ? [...selectedIDs, cluster.id] : selectedIDs.filter(id => id !== cluster.id))} /><span className="min-w-0 break-words">{clusterLabel(cluster.id)}</span></label>)}
                {directoryLoading ? <p role="status" className="text-sm text-theme-text-secondary">{t('正在读取 Hub 集群目录…', 'Loading the cluster directory…')}</p>
                  : !clusters.length && !directoryError && <p className="text-sm text-theme-text-secondary">{t('尚无可选集群，请先在 Hub 中配置集群。', 'No clusters available. Connect a cluster to the Hub first.')}</p>}
              </div>
            </fieldset>
            <label className="block text-sm">{t('操作范围', 'Access level')}
              <select value={readOnlyOnly || readOnly ? 'read' : 'write'} disabled={readOnlyOnly} onChange={event => setReadOnly(event.target.value === 'read')} className="portal-control mt-2 w-full">
                <option value="read">{t('只读 · 查询与诊断', 'Read-only · Query and diagnose')}</option>
                {!readOnlyOnly && <option value="write">{t('读写 · 查询与修改', 'Read and write · Query and modify')}</option>}
              </select>
            </label>
            <p className="text-xs text-theme-text-secondary">{readOnlyOnly || readOnly ? t('仅查询所选集群。', 'Query the selected clusters.') : t('可修改所选集群中你有权限的资源。', 'Modify resources you have permission to access in the selected clusters.')}</p>
            <button type="submit" className="btn-brand inline-flex items-center gap-2 px-4 py-2.5 text-sm" disabled={!selectedIDs.length || !label.trim() || Boolean(directoryError)}><KeyRound size={16} />{busy === 'create' ? t('正在生成…', 'Creating…') : t('生成 Token', 'Create token')}</button>
          </fieldset>
          {issued && <p className="text-xs text-theme-text-secondary">{t('请先保存并关闭上方原文，再生成其他凭据。', 'Save and close the token above before creating another credential.')}</p>}
        </form>
      </section>
      <section className="portal-card min-w-0 p-5 lg:p-6" aria-labelledby="client-config-title">
        <h2 id="client-config-title" className="text-lg font-semibold">{t('连接 AI Agent', 'Connect an AI agent')}</h2>
        <ConnectionProfileSelector />
        <div className="cloud-endpoint"><code>{profile.mcpURL || t('未配置 MCP 地址', 'MCP address unavailable')}</code>{!endpointError && <CopyValue value={profile.mcpURL} label={t('复制地址', 'Copy address')} />}</div>
        {endpointError ? <p role="alert" className="mt-2 text-sm text-warning-text">{endpointError}</p> : <>
          <dl className="mt-5 space-y-3 text-sm">
            <div className="flex flex-wrap justify-between gap-2"><dt className="text-theme-text-secondary">{t('传输协议', 'Transport')}</dt><dd>Streamable HTTP</dd></div>
            <div className="flex flex-wrap justify-between gap-2"><dt className="text-theme-text-secondary">{t('认证方式', 'Authentication')}</dt><dd>Bearer Token</dd></div>
          </dl>
          <p className="mt-5 text-sm text-theme-text-secondary">{t('生成凭据后，复制连接信息交给你的 AI Agent。', 'Create a credential, then copy the connection details to your AI agent.')}</p>
        </>}
      </section>
    </div>
    <section className="portal-card p-5 lg:p-6" aria-label={t('已签发凭据', 'Issued credentials')}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">{t('已签发凭据', 'Issued credentials')}</h2>
        <button type="button" className="portal-control inline-flex items-center gap-2" disabled={loading || Boolean(busy)} onClick={() => setRevision(value => value + 1)}>
          <RefreshCw size={15} className={loading ? 'animate-spin' : ''} />{t('刷新列表', 'Refresh list')}
        </button>
      </div>
      {loadError && <p role="alert" className="mt-4 text-sm text-warning-text">
        {cloudErrorMessage(loadError, t)}{credentials && t(' 以下是上次读取的记录，当前状态未确认。', ' Showing the last available records. Current status is unconfirmed.')}
      </p>}
      {!credentials && loading && <p role="status" className="py-8 text-sm text-theme-text-secondary">{t('正在读取凭据…', 'Loading credentials…')}</p>}
      {credentials?.length === 0 && <p className="py-8 text-sm text-theme-text-secondary">{t('暂无凭据。', 'No credentials yet.')}</p>}
      <div className="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-2">
        {credentials?.map(credential => {
          const status = credentialStatus(credential, now)
          return <article key={credential.id} className="rounded-lg border border-theme-border p-4" aria-label={t(`凭据 ${credential.label}`, `Credential ${credential.label}`)}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="break-all font-medium">{credential.label}</h3>
              <div className="flex gap-2">
                <Badge tone={credential.readOnly ? 'accent1' : 'accent2'}>{credential.readOnly ? t('只读', 'Read-only') : t('读写', 'Read and write')}</Badge>
                <Badge severity={loadError ? 'neutral' : statusTones[status]}>{loadError ? t('状态待确认', 'Status unconfirmed') : statusLabels[status]}</Badge>
              </div>
            </div>
            <dl className="mt-3 space-y-2 text-xs">
              <div><dt className="text-theme-text-tertiary">{t('组织', 'Organization')}</dt><dd className="mt-1 break-all">{credential.organizationId === organizationId ? organizationName : credential.organizationId || t('未知', 'Unknown')}</dd></div>
              <div><dt className="text-theme-text-tertiary">{t('允许的集群', 'Allowed clusters')}</dt><dd className="mt-1 break-all">{credential.clusterIds.map(clusterLabel).join(' · ')}</dd></div>
              <div><dt className="text-theme-text-tertiary">{t('到期时间', 'Expiry')}</dt><dd className="mt-1"><CredentialExpiration credential={credential} /></dd></div>
            </dl>
            {!credential.revokedAt && <div className="mt-4 border-t border-theme-border pt-3">
              {revokeID === credential.id ? <div className="space-y-2">
                <p className="text-xs text-warning-text">{t('确认撤销？使用此凭据的客户端将无法发起新请求。', 'Revoke this credential? Future requests from clients using it will be denied.')}</p>
                <div className="flex gap-2">
                  <button type="button" className="portal-control" disabled={Boolean(busy)} onClick={() => revoke(credential.id)}>{t('确认撤销', 'Confirm revocation')}</button>
                  <button type="button" className="portal-control" disabled={Boolean(busy)} onClick={() => setRevokeID('')}>{t('取消', 'Cancel')}</button>
                </div>
              </div> : <button type="button" className="text-xs text-accent-text" disabled={Boolean(busy)} onClick={() => setRevokeID(credential.id)}>{t('撤销凭据', 'Revoke credential')}</button>}
            </div>}
          </article>
        })}
      </div>
    </section>
  </div>
}

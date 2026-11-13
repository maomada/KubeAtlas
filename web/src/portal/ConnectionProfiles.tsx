import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { cloudRequest } from './cloudApi'

export type ConnectionProfile = 'lan' | 'public'

export interface HubConnections {
  lanURL: string
  publicURL: string
  ownerUserID: string
  catalogAvailable: boolean
  bootstrapAvailable?: boolean
  catalog: {
    available: boolean
    version?: string
    installerSHA256?: string
    chartSHA256?: string
    chartFile?: string
    imageRepository?: string
    imageDigest?: string
  }
}

interface ConnectionContextValue {
  profile: ConnectionProfile
  profileRevision: number
  selectProfile: (profile: ConnectionProfile) => void
  connections?: HubConnections
  origin: string
  mcpURL: string
  installAvailable: boolean
  loading: boolean
  error: string
  refresh: () => Promise<void>
  save: (lanURL: string, publicURL: string) => Promise<void>
}

const preferenceKey = 'kubeatlas.hub.connection-profile'
const ConnectionContext = createContext<ConnectionContextValue | undefined>(undefined)

function storedProfile(): ConnectionProfile {
  try {
    return window.localStorage.getItem(preferenceKey) === 'public' ? 'public' : 'lan'
  } catch {
    return 'lan'
  }
}

export function ConnectionProfilesProvider({ children }: { children: ReactNode }) {
  const [profile, setProfile] = useState<ConnectionProfile>(storedProfile)
  const [profileRevision, setProfileRevision] = useState(0)
  const [connections, setConnections] = useState<HubConnections>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const refreshSequence = useRef(0)
  const profileRef = useRef(profile)
  const refresh = useCallback(async () => {
    const sequence = ++refreshSequence.current
    setLoading(true)
    try {
      const response = await cloudRequest<HubConnections>('/hub/connections')
      if (sequence !== refreshSequence.current) return
      setConnections(response)
      setError('')
    } catch (failure) {
      if (sequence !== refreshSequence.current) return
      setConnections(undefined)
      setError(failure instanceof Error ? failure.message : '连接配置读取失败。')
    } finally {
      if (sequence === refreshSequence.current) setLoading(false)
    }
  }, [])
  useEffect(() => {
    let cancelled = false
    void Promise.resolve().then(() => { if (!cancelled) void refresh() })
    return () => { cancelled = true }
  }, [refresh])
  const selectProfile = useCallback((next: ConnectionProfile) => {
    if (profileRef.current !== next) setProfileRevision(revision => revision + 1)
    profileRef.current = next
    setProfile(next)
    try { window.localStorage.setItem(preferenceKey, next) } catch { /* the current page still keeps the selection */ }
  }, [])
  const save = useCallback(async (lanURL: string, publicURL: string) => {
    refreshSequence.current++
    const response = await cloudRequest<HubConnections>('/hub/connections', { method: 'PUT', body: { lanURL, publicURL } })
    setConnections(response)
    setError('')
  }, [])
  const origin = profile === 'lan' ? connections?.lanURL ?? '' : connections?.publicURL ?? ''
  const value = useMemo<ConnectionContextValue>(() => ({
    profileRevision,
    profile, selectProfile, connections, origin, mcpURL: origin ? `${origin}/mcp` : '',
    installAvailable: !!origin && !loading && !error && connections?.catalogAvailable === true && connections.catalog?.available === true && connections.bootstrapAvailable !== false,
    loading, error, refresh, save,
  }), [profile, profileRevision, selectProfile, connections, origin, loading, error, refresh, save])
  return <ConnectionContext.Provider value={value}>{children}</ConnectionContext.Provider>
}

export function useConnectionProfiles() {
  const value = useContext(ConnectionContext)
  if (!value) throw new Error('ConnectionProfilesProvider is missing')
  return value
}

export function ConnectionProfileSelector() {
  const { profile, selectProfile } = useConnectionProfiles()
  return <fieldset className="cloud-connection-selector" aria-label="连接配置">
    <legend>连接配置</legend>
    <button className="cloud-button" type="button" aria-pressed={profile === 'lan'} onClick={() => selectProfile('lan')}>局域网</button>
    <button className="cloud-button" type="button" aria-pressed={profile === 'public'} onClick={() => selectProfile('public')}>公网</button>
  </fieldset>
}

export function ConnectionSettingsEditor({ canManage }: { canManage: boolean }) {
  const { connections, loading, error, refresh, save } = useConnectionProfiles()
  const profileKey = `${connections?.lanURL ?? ''}\0${connections?.publicURL ?? ''}`
  const [draft, setDraft] = useState({ key: '', lanURL: '', publicURL: '' })
  const lanURL = draft.key === profileKey ? draft.lanURL : connections?.lanURL ?? ''
  const publicURL = draft.key === profileKey ? draft.publicURL : connections?.publicURL ?? ''
  const [busy, setBusy] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!canManage || busy) return
    setBusy(true); setSaveError(''); setSaved(false)
    try { await save(lanURL.trim(), publicURL.trim()); setSaved(true) }
    catch (failure) { setSaveError(failure instanceof Error ? failure.message : '保存失败，请重试。') }
    finally { setBusy(false) }
  }
  return <section className="cloud-card" aria-labelledby="hub-connections-heading">
    <header><h2 id="hub-connections-heading">Hub 连接地址</h2></header>
    <p>这些地址用于 Agent 安装、节点镜像拉取和 MCP。请配置集群节点与客户端均可访问且证书受信任的根地址；不会自动使用浏览器地址。</p>
    {loading && <p role="status">正在读取连接地址…</p>}
    {error && <p role="alert">{error} <button type="button" onClick={() => { void refresh() }}>重试</button></p>}
    <form className="cloud-form" onSubmit={submit}>
      <label>局域网根地址<input type="url" autoComplete="url" placeholder="http://192.0.2.10" value={lanURL} disabled={!canManage || busy || loading || !!error} onChange={event => setDraft({ key: profileKey, lanURL: event.target.value, publicURL })} /></label>
      <label>公网根地址<input type="url" autoComplete="url" placeholder="https://hub.example.com" value={publicURL} disabled={!canManage || busy || loading || !!error} onChange={event => setDraft({ key: profileKey, lanURL, publicURL: event.target.value })} /></label>
      <small>请配置节点和客户端均可访问的根地址：HTTPS 域名或 IP 必须使用有效且受信任的证书。HTTP 仅支持服务器明确启用 --insecure-cookie 后的字面 IP 地址，且只限可信网络；不支持 HTTP 域名，也不会在 TLS 验证失败时回退。本机回环地址可用于开发。</small>
      <small>HTTP 会暴露密码、会话 Cookie、Token 和下载内容。HTTP 镜像仓库需要操作员手动配置节点容器运行时；本 Portal 不配置私有 CA 或节点信任。</small>
      {saveError && <p role="alert">{saveError}</p>}
      {saved && <p role="status">连接地址已保存；现有 Agent 不会自动重连或升级。</p>}
      {canManage && <footer><button className="cloud-button cloud-primary" disabled={busy || loading}>{busy ? '保存中…' : '保存连接地址'}</button></footer>}
    </form>
  </section>
}

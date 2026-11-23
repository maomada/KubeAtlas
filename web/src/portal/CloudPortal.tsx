import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode } from 'react'
import { flushSync } from 'react-dom'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { Activity, AlertTriangle, Bot, Boxes, ChevronDown, ChevronsLeft, ClipboardCheck, Clock, Coins, ExternalLink, Gauge, GitBranch, HelpCircle, Home, Layers, LogOut, Menu, Moon, Network, Package, Plus, Server, Settings, ShieldCheck, Sparkles, Sun } from 'lucide-react'
import { ClusterSwitcher, FilterLocationProvider, NamespacePicker, Tooltip, type FilterLocation } from '@maomada/k8s-ui'
import { useLocale, type Translate } from '@maomada/k8s-ui/hooks/useLocale'
import { Omnibar, type OmnibarHandle } from '../components/ui/Omnibar'
import type { CommandItem } from '../components/ui/command-items'
import { refreshAfterNamespaceSwitch, type NamespaceScope, type SearchResult } from '../api/client'
import type { APIResource } from '../types'
import type { AgentsResponse } from '../api/diagnose'
import { namespaceFilterDisabled } from '../utils/namespaceFilter'
import { cloudRequest, clusterBase, clusterPage, clusterView, selectedCluster, useCloudQuery, type HubCluster } from './cloudApi'
import { useFleetQuery } from './cloudFleet'
import { CloudDialog, CloudError, CloudLink, CloudLoading } from './CloudShared'
import { CloudClusters, CloudInstall } from './CloudClusters'
import { CloudHome } from './CloudHome'
import { CloudOrganization, CloudMembers, CloudSSO, DisabledMembership, OrganizationSelection, PendingMembership, canManageOrganization, logoutPortal, type HubContext, type HubMember } from './CloudOrganization'
import { CloudApplications, CloudCertificates, CloudChecks, CloudIssues, resourceHref } from './CloudFleetViews'
import { MCPCredentials } from './MCPCredentials'
import { CloudPlatform } from './CloudPlatform'
import { replacePortalLocation } from './portalNavigation'
import { cloudViewHref, navigateCloud, useCloudLocation } from './cloudNavigation'
import { usePortalTheme } from './usePortalTheme'
import { ConnectionProfilesProvider } from './ConnectionProfiles'
import { PortalLanguageSelect } from './PortalLanguageSelect'
import { probePortalSession } from './auth'
import { Login } from './Login'
import { PortalLanding, Register } from './Registration'
import { Setup } from './Setup'
import { hubYamlDraftScope, reconcileHubYamlDrafts, subscribeToHubYamlDraftInvalidation, type HubYamlDraftInvalidationEvent } from './hubYamlDrafts'

const NativeKubeAtlas = lazy(() => import('../KubeAtlasApp').then(module => ({ default: module.KubeAtlasApp })))

interface NavItem { label: string; path: string; icon: ComponentType<{ size?: number }>; cluster?: boolean }
function navigationItems(t: Translate): NavItem[] {
  return [
    { label: t('首页', 'Home'), path: '/', icon: Home },
    { label: t('问题', 'Issues'), path: '/issues', icon: AlertTriangle },
    { label: t('应用', 'Applications'), path: '/applications', icon: Boxes },
    { label: 'GitOps', path: '/gitops', icon: GitBranch, cluster: true },
    { label: t('证书', 'Certificates'), path: '/certs', icon: ShieldCheck },
    { label: t('检查', 'Checks'), path: '/checks', icon: ClipboardCheck },
    { label: t('资源', 'Resources'), path: '/resources', icon: Layers, cluster: true },
    { label: t('拓扑', 'Topology'), path: '/topology', icon: Network, cluster: true },
    { label: t('流量', 'Traffic'), path: '/traffic', icon: Activity, cluster: true },
    { label: t('成本', 'Cost'), path: '/cost', icon: Coins, cluster: true },
    { label: t('容量', 'Capacity'), path: '/capacity', icon: Gauge, cluster: true },
    { label: t('时间线', 'Timeline'), path: '/timeline', icon: Clock, cluster: true },
    { label: 'Helm', path: '/helm', icon: Package, cluster: true },
  ]
}

export function PortalEntry() {
  const { t } = useLocale()
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } }))
  const [sessionInvalidation, setSessionInvalidation] = useState<HubYamlDraftInvalidationEvent>()
  useEffect(() => subscribeToHubYamlDraftInvalidation(event => {
    if (event.type === 'purge') {
      flushSync(() => setSessionInvalidation(event))
    } else {
      setSessionInvalidation(event)
    }
  }), [])
  if (sessionInvalidation?.type === 'logout-failed') return <div className="cloud-page"><CloudError error={sessionInvalidation.error} retry={() => { void logoutPortal(t).catch(() => {}) }} /></div>
  if (sessionInvalidation?.type === 'purge-failed') return <div className="cloud-page"><CloudError error={sessionInvalidation.error} /></div>
  if (sessionInvalidation?.type === 'purge' || sessionInvalidation?.type === 'purge-cleared') {
    const label = sessionInvalidation.type === 'purge-cleared'
      ? t('其他标签页中的会话已更改；已清除已保存的 YAML 草稿。', 'The session changed in another tab. Saved YAML drafts were cleared.')
      : sessionInvalidation.source === 'local'
        ? t('正在退出并清除本地 YAML 草稿…', 'Signing out and clearing local YAML drafts…')
        : t('其他标签页中的会话已更改；正在锁定 YAML 编辑…', 'The session changed in another tab. Locking YAML editing…')
    return <div className="cloud-page cloud-empty"><CloudLoading label={label} /><button className="cloud-button" onClick={() => window.location.reload()}>{t('刷新页面', 'Refresh page')}</button></div>
  }
  return <QueryClientProvider client={client}><PortalRoutes queryClient={client} /></QueryClientProvider>
}

function PortalRoutes({ queryClient }: { queryClient: QueryClient }) {
  const { t } = useLocale()
  usePortalTheme()
  const location = useCloudLocation()
  useEffect(() => {
    if (location.pathname === '/settings/platform') navigateCloud(`/platform${location.search}${location.hash}`, true)
  }, [location.pathname, location.search, location.hash])
  if (location.pathname === '/settings/platform') return <CloudLoading label={t('正在打开平台管理…', 'Opening platform administration…')} />
  if (location.pathname === '/login') return <Login />
  if (location.pathname === '/register') return <Register />
  if (location.pathname === '/setup') return <Setup />
  return <PortalMode path={location.pathname} queryClient={queryClient} />
}

function PortalMode({ path, queryClient }: { path: string; queryClient: QueryClient }) {
  const { t } = useLocale()
  const session = useQuery({
    queryKey: ['portal-session'], queryFn: ({ signal }) => probePortalSession(signal, t),
    retry: false, refetchOnWindowFocus: false, staleTime: 0,
  })
  const mode = useCloudQuery<HubContext>('/hub', '/api', session.data === 'authenticated')
  const [switching, setSwitching] = useState(false)
  const [shellAuthorizationContext, setShellAuthorizationContext] = useState<string>()
  const [reconciledDraftContext, setReconciledDraftContext] = useState<string>()
  const [draftStorageErrorContext, setDraftStorageErrorContext] = useState<string>()
  const [draftReconcileRetry, setDraftReconcileRetry] = useState(0)
  const nativeClusterId = selectedCluster(path)
  const nativeClusterScope = nativeClusterId ? JSON.stringify({ clusterId: nativeClusterId, apiBase: clusterBase(nativeClusterId) }) : undefined
  // Keep native query data isolated when the route selects a different cluster.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const nativeClient = useMemo(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }), [nativeClusterId])
  const authorityHub = isHubContext(mode.data) ? mode.data : undefined
  const hub = authorityHub?.enabled === true ? authorityHub : undefined
  const hasActiveShell = !!hub && (hub.member?.status === 'active' || hub.platformOwner) && hub.organizations.some(item => item.id === hub.activeOrganizationId)
  const shouldTrackAuthorizationContext = hasActiveShell || !!hub && hub.platformOwner && path === '/platform'
  const currentAuthorizationContext = authorityHub ? authorizationContextKey(authorityHub) : undefined
  const draftAuthority = currentAuthorizationContext === undefined ? undefined : JSON.stringify({ authorization: currentAuthorizationContext, enabled: authorityHub?.enabled })
  const draftReconcileKey = draftAuthority === undefined ? undefined : JSON.stringify([draftAuthority, nativeClusterScope ?? null])
  const authorizationContextChanged = currentAuthorizationContext !== undefined && shellAuthorizationContext !== undefined && currentAuthorizationContext !== shellAuthorizationContext
  useEffect(() => {
    if (session.data !== 'authenticated' || mode.isPending || !draftAuthority || !draftReconcileKey) return
    if (reconcileHubYamlDrafts(draftAuthority, nativeClusterScope)) {
      setReconciledDraftContext(draftReconcileKey)
      setDraftStorageErrorContext(undefined)
    } else {
      setDraftStorageErrorContext(draftReconcileKey)
    }
  }, [draftAuthority, draftReconcileKey, draftReconcileRetry, mode.isPending, nativeClusterScope, session.data])
  useEffect(() => {
    if (session.data === 'unauthenticated' && path !== '/') replacePortalLocation('/login')
  }, [session.data, path])
  useEffect(() => {
    if (!shouldTrackAuthorizationContext || shellAuthorizationContext !== undefined || currentAuthorizationContext === undefined) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setShellAuthorizationContext(currentAuthorizationContext)
  }, [currentAuthorizationContext, shouldTrackAuthorizationContext, shellAuthorizationContext])
  useEffect(() => {
    if (!authorizationContextChanged) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setSwitching(true)
    void (async () => {
      await Promise.all([queryClient.cancelQueries(), nativeClient.cancelQueries()])
      queryClient.clear()
      nativeClient.clear()
      setShellAuthorizationContext(currentAuthorizationContext)
      replacePortalLocation('/')
    })()
  }, [authorizationContextChanged, currentAuthorizationContext, nativeClient, queryClient])
  if (session.isPending) return <CloudLoading label={t('正在检查门户会话…', 'Checking portal session…')} />
  if (session.error) return <div className="cloud-page"><CloudError error={session.error} retry={() => { void session.refetch() }} /></div>
  if (session.data === 'unauthenticated') return path === '/' ? <PortalLanding /> : <CloudLoading label={t('正在跳转至登录页…', 'Redirecting to sign in…')} />
  if (switching) return <CloudLoading label={t('正在切换组织…', 'Switching organization…')} />
  if (mode.isPending) return <CloudLoading label={t('正在读取门户数据…', 'Loading portal data…')} />
  if (mode.error || !isHubContext(mode.data)) return <div className="cloud-page"><CloudError error={mode.error ?? new Error(t('门户返回的数据无效', 'The portal returned invalid data'))} retry={() => { void mode.refetch() }} /></div>
  const currentHub = mode.data
  if (draftReconcileKey && draftStorageErrorContext === draftReconcileKey) return <div className="cloud-page"><CloudError error={new Error(t('无法隔离此 Portal 会话的 YAML 草稿。', 'Unable to isolate YAML drafts for this portal session.'))} retry={() => setDraftReconcileRetry(value => value + 1)} /></div>
  if (session.data === 'authenticated' && draftReconcileKey && reconciledDraftContext !== draftReconcileKey) return <CloudLoading label={t('正在隔离门户 YAML 草稿…', 'Isolating portal YAML drafts…')} />
  if (!currentHub.enabled) return <div className="cloud-page"><CloudError error={new Error(t('门户未启用', 'The portal is disabled'))} retry={() => { void mode.refetch() }} /></div>
  if (authorizationContextChanged) return <CloudLoading label={t('门户权限已更改，正在清除旧缓存…', 'Portal permissions changed. Clearing cached data…')} />
  if (currentHub.member?.status === 'pending') return <PendingMembership member={currentHub.member} retry={() => { void mode.refetch() }} />
  if (currentHub.member?.status === 'disabled') return <DisabledMembership retry={() => { void mode.refetch() }} />
  const activeOrganization = currentHub.organizations.find(organization => organization.id === currentHub.activeOrganizationId)
  const nativeYamlDraftScope = nativeClusterId && nativeClusterScope && draftAuthority ? hubYamlDraftScope(draftAuthority, nativeClusterId, clusterBase(nativeClusterId)) : undefined
  const nativeYamlReadOnly = !currentHub.platformOwner && (
    currentHub.member?.role === 'viewer' || activeOrganization?.role === 'viewer' ||
    activeOrganization?.role !== currentHub.member?.role
  )
  if ((currentHub.member?.status === 'active' || currentHub.platformOwner) && activeOrganization) return <ConnectionProfilesProvider key={currentHub.activeOrganizationId}><CloudShell hub={currentHub} queryClient={queryClient} nativeClient={nativeClient} yamlDraftScope={nativeYamlDraftScope} yamlReadOnly={nativeYamlReadOnly} onSwitchOrganization={async (organizationId, currentNativeClient) => {
    await cloudRequest('/hub/organization/select', { method: 'POST', body: { organizationId } })
    flushSync(() => setSwitching(true))
    await queryClient.cancelQueries()
    await currentNativeClient.cancelQueries()
    queryClient.clear()
    currentNativeClient.clear()
    replacePortalLocation('/')
  }} /></ConnectionProfilesProvider>
  if (currentHub.platformOwner && path === '/platform') return <PlatformOnly hub={currentHub} />
  return <OrganizationSelection organizations={currentHub.organizations} activeOrganizationId={currentHub.activeOrganizationId} platformOwner={currentHub.platformOwner} />
}

function isHubContext(value: unknown): value is HubContext {
  if (!value || typeof value !== 'object') return false
  const hub = value as Record<string, unknown>
  const role = (value: unknown) => value === 'owner' || value === 'member' || value === 'viewer'
  if (typeof hub.enabled !== 'boolean' || typeof hub.userId !== 'string' || typeof hub.platformOwner !== 'boolean') return false
  if (!hub.userId.trim()) return false
  if (!isCanonicalAuthorizationRevision(hub.authorizationRevision)) return false
  if (hub.activeOrganizationId !== null && typeof hub.activeOrganizationId !== 'string') return false
  if (!Array.isArray(hub.organizations) || !hub.organizations.every(item => item && typeof item.id === 'string' && typeof item.name === 'string' && role(item.role))) return false
  if (hub.member !== undefined && hub.member !== null) {
    if (typeof hub.member !== 'object') return false
    const member = hub.member as HubMember
    if (!['active', 'pending', 'disabled'].includes(member.status ?? '')) return false
    if (member.role !== undefined && !role(member.role)) return false
    if (member.status === 'active' && hub.activeOrganizationId !== null && !hub.platformOwner && !role(member.role)) return false
  }
  return true
}

function isCanonicalAuthorizationRevision(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const canonical = /^(0|[1-9]\d*)$/.exec(value)
  if (!canonical || canonical[0] !== value) return false
  return value.length < 20 || value.length === 20 && value <= '18446744073709551615'
}

function authorizationContextKey(hub: HubContext): string {
  const organizations = hub.organizations
    .map(({ id, role }) => ({ id, role }))
    .sort((left, right) => left.id.localeCompare(right.id))
  const activeOrganizationRole = organizations.find(item => item.id === hub.activeOrganizationId)?.role ?? null
  return JSON.stringify({
    userId: hub.userId,
    authorizationRevision: hub.authorizationRevision,
    memberId: hub.member?.id ?? null,
    memberUserId: hub.member?.userId ?? null,
    accountStatus: hub.member?.status ?? null,
    memberRole: hub.member?.role ?? null,
    activeOrganizationId: hub.activeOrganizationId,
    activeOrganizationRole,
    organizations,
    platformOwner: hub.platformOwner,
  })
}

function PlatformOnly({ hub }: { hub: HubContext }) {
  const { t } = useLocale()
  const [error, setError] = useState<unknown>()
  useEffect(() => { document.title = t('平台管理 — KubeAtlas', 'Platform administration — KubeAtlas') }, [t])
  return <div className="cloud-platform-only"><header className="cloud-topbar"><CloudLink href="/" className="cloud-brand"><span className="cloud-brand-mark"><Layers size={21} /></span><strong>KubeAtlas</strong><small>HUB</small></CloudLink><span>{t('平台管理', 'Platform administration')}</span><button className="cloud-button" onClick={() => { void logoutPortal(t).catch(setError) }}>{t('退出登录', 'Sign out')}</button></header>{error !== undefined && <CloudError error={error} />}<main className="cloud-content"><CloudPlatform platformOwner={hub.platformOwner} /></main></div>
}

function ClusterNamespacePicker({ id, client, connected, view, search, pending, onApply, onError }: {
  id: string
  client: QueryClient
  connected: boolean
  view: string
  search: string
  pending: boolean
  onApply: (namespaces: string[]) => void
  onError: (error: unknown) => void
}) {
  const { t } = useLocale()
  const namespace = useQuery<NamespaceScope>({
    queryKey: ['namespace-scope'],
    queryFn: ({ signal }) => cloudRequest('/cluster/namespace-scope', { base: clusterBase(id), signal }),
    enabled: connected,
    staleTime: 30000,
  }, client)
  const apiResources = useQuery<APIResource[]>({
    queryKey: ['api-resources'],
    queryFn: ({ signal }) => cloudRequest('/api-resources', { base: clusterBase(id), signal }),
    enabled: connected,
    staleTime: 5 * 60 * 1000,
  }, client)
  const filter = namespaceFilterDisabled(view.split('/')[1], view, search, apiResources.data)
  useEffect(() => { onError(namespace.error) }, [namespace.error, onError])
  return <NamespacePicker scope={namespace.data} loading={namespace.isPending} pending={pending} disabled={filter.disabled} disabledTooltip={filter.tooltip} onApply={onApply} variant="segment" label={t('命名空间', 'Namespace')} />
}

function InvestigationGate({ id, children }: { id: string; children: ReactNode }) {
  const { t } = useLocale()
  const agents = useCloudQuery<AgentsResponse>('/agents', clusterBase(id))
  if (agents.isPending) return <CloudLoading label={t('正在检查 AI 调查功能…', 'Checking AI investigations…')} />
  if (agents.error) return <CloudError error={agents.error} retry={() => { void agents.refetch() }} />
  if (agents.data?.eligible !== true) return <div className="cloud-page cloud-empty"><Sparkles size={30} /><h1>{t('AI 调查功能不可用', 'AI investigations are unavailable')}</h1><p>{t('此集群当前部署模式未声明支持内置调查功能。仅安装本地 Agent CLI 并不能启用通过已认证 Hub 使用的调查功能。', 'This cluster does not advertise support for built-in investigations through the authenticated Hub.')}</p><CloudLink href="/agents" className="cloud-button">{t('为外部 AI 客户端配置限定范围的 MCP 访问', 'Configure scoped MCP access for an external AI client')}</CloudLink></div>
  return children
}

function CloudShell({ hub, queryClient, nativeClient, yamlDraftScope, yamlReadOnly, onSwitchOrganization }: {
  hub: HubContext
  queryClient: QueryClient
  nativeClient: QueryClient
  yamlDraftScope?: string
  yamlReadOnly: boolean
  onSwitchOrganization: (organizationId: string, nativeClient: QueryClient) => Promise<void>
}) {
  const { t } = useLocale()
  const navigation = navigationItems(t)
  const { userId, member, platformOwner } = hub
  const canManage = canManageOrganization(member, platformOwner, hub.activeOrganizationId)
  const location = useCloudLocation()
  const directory = useCloudQuery<{ clusters: HubCluster[] }>('/hub/clusters')
  const clusters = directory.data?.clusters ?? []
  const id = selectedCluster(location.pathname)
  const cluster = clusters.find(item => item.id === id)
  const view = id ? clusterView(location.pathname) : location.pathname
  const [menuOpen, setMenuOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [panel, setPanel] = useState<'help' | 'account'>()
  const [dark, setDark] = usePortalTheme()
  const [actionError, setActionError] = useState<unknown>()
  const [namespaceError, setNamespaceError] = useState<{ client: QueryClient; error: unknown }>()
  const [pendingNamespace, setPendingNamespace] = useState(false)
  const [organizationSwitchPending, setOrganizationSwitchPending] = useState(false)
  const [organizationSwitchError, setOrganizationSwitchError] = useState<unknown>()
  const searchRef = useRef<OmnibarHandle>(null)
  const reportNamespaceError = useCallback((error: unknown) => {
    setNamespaceError(error == null ? undefined : { client: nativeClient, error })
  }, [nativeClient])
  const chosenID = id ?? location.searchParams.get('cluster') ?? ''
  const fleetScope = chosenID ? clusters.filter(item => item.id === chosenID) : clusters
  const missingSelection = chosenID && !clusters.some(item => item.id === chosenID)

  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      if (target?.closest('.monaco-editor, .xterm')) return
      const typing = target?.closest('input, textarea, select, [contenteditable="true"]')
      const slash = event.key === '/' && !typing && !event.ctrlKey && !event.metaKey && !event.altKey
      if ((event.key === 'k' && (event.ctrlKey || event.metaKey)) || slash) {
        event.preventDefault(); event.stopPropagation(); searchRef.current?.focus()
      }
    }
    const closePanels = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setPanel(undefined); setMenuOpen(false) }
    }
    // Embedded views also bind / and can asynchronously steal focus.
    window.addEventListener('keydown', openSearch, true)
    window.addEventListener('keydown', closePanels)
    return () => {
      window.removeEventListener('keydown', openSearch, true)
      window.removeEventListener('keydown', closePanels)
    }
  }, [])
  useEffect(() => { setMenuOpen(false); setPanel(undefined); setActionError(undefined) }, [location.pathname])
  useEffect(() => () => { void nativeClient.cancelQueries(); nativeClient.clear() }, [nativeClient])

  const target = (item: NavItem) => cloudViewHref(item.path, !!item.cluster, clusters.find(item => item.id === chosenID)?.id, location.search)
  const commands: CommandItem[] = navigation.map(item => ({ id: item.path, label: item.label, category: 'Views', action: () => navigateCloud(target(item)) }))
  const filterLocation: FilterLocation = {
    searchParams: location.searchParams,
    update: (updater, options) => {
      const current = new URL(window.location.href)
      current.search = updater(current.searchParams).toString()
      navigateCloud(current.pathname + current.search, options?.replace)
    },
  }
  const selectCluster = (selected: string) => {
    if (id) navigateCloud(selected ? clusterPage(selected, view) : '/')
    else {
      const query = new URLSearchParams(location.searchParams)
      if (selected) query.set('cluster', selected); else query.delete('cluster')
      navigateCloud(`${location.pathname}${query.size ? `?${query}` : ''}`)
    }
  }
  async function applyNamespace(namespaces: string[]) {
    if (!id || pendingNamespace) return
    setPendingNamespace(true); setActionError(undefined)
    try {
      const scope = await cloudRequest<NamespaceScope>('/cluster/namespace', { base: clusterBase(id), method: 'POST', body: { namespaces } })
      nativeClient.setQueryData(['namespace-scope'], scope)
      await refreshAfterNamespaceSwitch(nativeClient, scope)
    } catch (error) {
      void nativeClient.invalidateQueries({ queryKey: ['namespace-scope'] })
      setActionError(error)
    } finally { setPendingNamespace(false) }
  }
  async function logout() {
    try { await logoutPortal(t); queryClient.clear(); nativeClient.clear() }
    catch (error) { setActionError(error) }
  }
  async function switchOrganization(organizationId: string) {
    if (organizationSwitchPending || organizationId === hub.activeOrganizationId) return
    setOrganizationSwitchPending(true); setOrganizationSwitchError(undefined)
    try { await onSwitchOrganization(organizationId, nativeClient) }
    catch (error) { setOrganizationSwitchError(error); setOrganizationSwitchPending(false) }
  }

  const investigationView = view === '/investigations' || view.startsWith('/investigations/')
  const title = investigationView ? t('AI 调查', 'AI Investigations') : navigation.find(item => item.path === view || item.path !== '/' && view.startsWith(item.path + '/'))?.label ?? (view === '/platform' ? t('平台管理', 'Platform administration') : view.startsWith('/settings') ? t('设置', 'Settings') : view === '/agents' ? 'Agent' : view === '/install' ? t('连接集群', 'Connect cluster') : t('集群', 'Clusters'))
  useEffect(() => { document.title = `${cluster ? cluster.name + ' · ' : ''}${title} — KubeAtlas` }, [title, cluster])
  const isHome = location.pathname === '/'
  const search = <FleetSearch refHandle={searchRef} clusters={clusters} commands={commands} hero={isHome} />
  const nativePath = id && !!cluster && cluster.state === 'connected'
  let content
  if (view === '/platform') content = <CloudPlatform platformOwner={platformOwner} />
  else if (directory.isPending) content = <CloudLoading label={t('正在读取集群目录…', 'Loading clusters…')} />
  else if (directory.error) content = <CloudError error={directory.error} retry={() => { void directory.refetch() }} />
  else if (location.pathname.startsWith('/c/') && !id || missingSelection) content = <CloudError error={new Error(t('所选集群无效或不存在', 'The selected cluster is invalid or no longer exists'))} />
  else if (id && !nativePath) content = <div className="cloud-page"><CloudError error={new Error(cluster?.revokedAt ? t('Agent 访问已撤销。重新连接前必须明确重新配对。', 'Agent access was revoked. Pair the Agent again to reconnect.') : t('集群当前离线。实时数据不可用；不会以快照代替。', 'The cluster is offline. Live data is unavailable.'))} /><CloudLink href="/clusters" className="cloud-button">{t('管理集群', 'Manage clusters')}</CloudLink></div>
  else if (nativePath) content = yamlDraftScope
    ? <Suspense fallback={<CloudLoading label={t('正在打开集群实时视图…', 'Opening the live cluster view…')} />}><NativeKubeAtlas key={id} apiBase={clusterBase(id)} basename={`/c/${id}`} queryClient={nativeClient} yamlDraftScope={yamlDraftScope} yamlReadOnly={yamlReadOnly} navSlots={{ embedded: true, clusterDisplayName: cluster.name, fleetTakeoverHref: target => target === 'gitops' ? undefined : `/${target}?cluster=${encodeURIComponent(id)}`, onHostNavigate: navigateCloud }} /></Suspense>
    : <CloudLoading label={t('正在验证门户 YAML 草稿权限…', 'Checking YAML draft access…')} />
  else if (view === '/' && !canManage && !clusters.length) content = <div className="cloud-page cloud-empty"><ShieldCheck size={32} /><h1>{t('尚未分配集群', 'No clusters assigned')}</h1><p>{t('请联系组织管理员授予集群访问权限。当前没有可用的集群健康评估。', 'Ask your organization administrator for cluster access. Cluster health is currently unavailable.')}</p><CloudLink className="cloud-button" href="/settings/organization">{t('组织详情', 'Organization details')}</CloudLink></div>
  else if (view === '/') content = <CloudHome clusters={fleetScope} search={search} />
  else if (view === '/clusters') content = <CloudClusters clusters={clusters} canManage={canManage} />
  else if (view === '/install') content = canManage ? <CloudInstall hub={hub} clusters={clusters} canManage={canManage} directoryError={directory.error ?? undefined} onRefreshClusters={() => { void directory.refetch() }} /> : <CloudError error={new Error(t('集群配对需要组织 Owner，或当前组织中的平台 Owner。', 'Pairing a cluster requires an organization owner or a platform owner in the current organization.'))} />
  else if (view === '/applications') content = <CloudApplications clusters={fleetScope} />
  else if (view === '/issues') content = <CloudIssues clusters={fleetScope} />
  else if (view === '/checks') content = <CloudChecks clusters={fleetScope} />
  else if (view === '/certs') content = <CloudCertificates clusters={fleetScope} />
  else if (view === '/agents' || view === '/mcp-access') content = <CloudAgents hub={hub} readOnlyOnly={member?.role === 'viewer'} />
  else if (view.startsWith('/settings')) content = <CloudSettings hub={hub} path={view} dark={dark} onTheme={() => setDark(!dark)} onLogout={() => { void logout() }} />
  else if (navigation.some(item => item.path === view && item.cluster) || view === '/investigations') content = <SelectLiveCluster clusters={fleetScope} view={view} />
  else content = <CloudPageNotFound />

  if (nativePath && investigationView) content = <InvestigationGate key={id} id={id}>{content}</InvestigationGate>

  return <FilterLocationProvider value={filterLocation}><><div className={`cloud-shell ${collapsed ? 'cloud-collapsed' : ''} ${menuOpen ? 'cloud-menu-open' : ''}`} aria-busy={organizationSwitchPending} inert={organizationSwitchPending || undefined}>
    {menuOpen && <button className="cloud-nav-backdrop" aria-label={t('关闭导航', 'Close navigation')} onClick={() => setMenuOpen(false)} />}
    <aside className="cloud-sidebar" aria-label={t('主导航', 'Main navigation')}>
      <CloudLink href="/" className="cloud-brand" aria-label={t('KubeAtlas 首页', 'KubeAtlas home')}><span className="cloud-brand-mark"><Layers size={21} /></span><strong>KubeAtlas</strong><small>HUB</small></CloudLink>
      <nav aria-label={t('主导航', 'Main navigation')}>{navigation.map(item => <CloudLink key={item.path} href={target(item)} aria-label={item.label} title={collapsed ? item.label : undefined} className={`cloud-nav-item ${item.path === '/resources' ? 'cloud-nav-separated' : ''}`} aria-current={view === item.path || item.path !== '/' && view.startsWith(item.path + '/') ? 'page' : undefined}><item.icon size={17} /><span>{item.label}</span></CloudLink>)}</nav>
      <div className="cloud-sidebar-bottom">
        <CloudLink href="/clusters" className="cloud-nav-item" aria-label={t('集群', 'Clusters')} aria-current={view === '/clusters' ? 'page' : undefined}><Server size={17} /><span>{t('集群', 'Clusters')}</span><small>{clusters.length}</small></CloudLink>
        <CloudLink href="/agents" className="cloud-nav-item" aria-label="Agent" aria-current={view === '/agents' || view === '/mcp-access' ? 'page' : undefined}><Bot size={17} /><span>Agent</span></CloudLink>
        <CloudLink href="/settings" className="cloud-nav-item" aria-label={t('设置', 'Settings')} aria-current={view.startsWith('/settings') ? 'page' : undefined}><Settings size={17} /><span>{t('设置', 'Settings')}</span></CloudLink>
        {platformOwner && <CloudLink href="/platform" className="cloud-nav-item" aria-label={t('平台管理', 'Platform administration')} aria-current={view === '/platform' ? 'page' : undefined}><ShieldCheck size={17} /><span>{t('平台管理', 'Platform administration')}</span></CloudLink>}
        <div className="cloud-sidebar-footer"><button aria-label={dark ? t('切换浅色主题', 'Switch to light theme') : t('切换深色主题', 'Switch to dark theme')} onClick={() => setDark(!dark)}>{dark ? <Sun size={17} /> : <Moon size={17} />}</button><button aria-label={collapsed ? t('展开导航', 'Expand navigation') : t('收起导航', 'Collapse navigation')} onClick={() => setCollapsed(!collapsed)}><ChevronsLeft size={17} /></button></div>
      </div>
    </aside>
    <div className="cloud-main"><header className="cloud-topbar">
      <button className="cloud-mobile-toggle cloud-icon-button" aria-label={t('打开导航', 'Open navigation')} onClick={() => setMenuOpen(true)}><Menu size={20} /></button>
      <div className="cloud-topbar-left cloud-scope"><OrganizationSwitcher organizations={hub.organizations} activeOrganizationId={hub.activeOrganizationId} busy={organizationSwitchPending} error={organizationSwitchError} onSelect={switchOrganization} /><ClusterSwitcher currentId={chosenID} currentName={clusters.find(item => item.id === chosenID)?.name ?? t('全部集群', 'All clusters')} items={[{ id: '', name: t('全部集群', 'All clusters') }, ...clusters.map(item => ({ id: item.id, name: item.name, status: item.state === 'connected' ? 'healthy' as const : 'unknown' as const, secondary: item.environment, disabled: item.state === 'revoked', title: item.state === 'revoked' ? t('Agent 访问已撤销', 'Agent access revoked') : undefined }))]} onSelect={item => selectCluster(item.id)} label={t('集群', 'Cluster')} footerSlot={<CloudLink href="/clusters">{t('管理集群 →', 'Manage clusters →')}</CloudLink>} />{id && <ClusterNamespacePicker key={id} id={id} client={nativeClient} connected={cluster?.state === 'connected'} view={view} search={location.search} pending={pendingNamespace} onApply={values => { void applyNamespace(values) }} onError={reportNamespaceError} />}</div>
      {!isHome && <div className="cloud-topbar-search">{search}</div>}
      <div className="cloud-topbar-actions">
        <button className="cloud-icon-button" aria-label={t('帮助', 'Help')} onClick={() => setPanel('help')}><HelpCircle size={18} /></button>
        <Tooltip content={userId || t('账户', 'Account')} position="bottom"><button className="cloud-account" aria-label={t('账户菜单', 'Account menu')} onClick={() => setPanel(panel === 'account' ? undefined : 'account')}>{userId?.slice(0, 1).toUpperCase() || 'K'}<ChevronDown size={12} /></button></Tooltip>
      </div>
    </header>
      {actionError !== undefined && <CloudError error={actionError} />}{namespaceError?.client === nativeClient && <CloudError error={namespaceError.error} />}
      <main className={nativePath ? 'cloud-content cloud-native' : 'cloud-content'}>{content}</main>
    </div>
    {panel === 'help' && <CloudDialog title={t('帮助', 'Help')} onClose={() => setPanel(undefined)}><div className="cloud-form"><p>{t('这是自托管 KubeAtlas Hub。集群当前状态通过各集群主动连接的 Agent 查询。', 'This is a self-hosted KubeAtlas Hub. Live cluster data comes from each connected Agent.')}</p><a className="cloud-button" target="_blank" rel="noopener noreferrer" href="https://github.com/maomada/KubeAtlas/tree/main/docs">{t('项目文档', 'Project documentation')}<ExternalLink size={14} /></a><p>{t('键盘快捷键：Ctrl/Cmd+K 或 / 打开集群搜索。按 Escape 关闭菜单。', 'Press Ctrl/Cmd+K or / to open cluster search. Press Escape to close the menu.')}</p></div></CloudDialog>}
    {panel === 'account' && <CloudDialog title={t('账户', 'Account')} onClose={() => setPanel(undefined)}><div className="cloud-form">{member && <strong>{member.displayName} · {member.role}</strong>}<code className="cloud-account-identity">{userId}</code><p>{t('当前为已认证的 Portal 会话。MCP Token 有独立有效期，不与此会话共用。', 'You are signed in to the portal. MCP credentials have their own lifetime.')}</p><CloudLink href="/settings" className="cloud-button">{t('偏好设置', 'Preferences')}</CloudLink><button className="cloud-button" onClick={() => { void logout() }}><LogOut size={16} />{t('退出登录', 'Sign out')}</button></div></CloudDialog>}
  </div>{organizationSwitchPending && <div className="cloud-switching-overlay" role="status"><CloudLoading label={t('正在切换组织并清除缓存…', 'Switching organization and clearing cached data…')} /></div>}</></FilterLocationProvider>
}

function OrganizationSwitcher({ organizations, activeOrganizationId, busy, error, onSelect }: {
  organizations: HubContext['organizations']; activeOrganizationId: string | null; busy: boolean; error?: unknown; onSelect: (id: string) => void
}) {
  const { t } = useLocale()
  return <div className="cloud-org-switcher"><label className="sr-only" htmlFor="portal-organization-switcher">{t('组织', 'Organization')}</label><select id="portal-organization-switcher" aria-label={t('切换组织', 'Switch organization')} className="portal-control" value={activeOrganizationId ?? ''} disabled={busy || organizations.length < 2} onChange={event => onSelect(event.target.value)}>{organizations.map(organization => <option key={organization.id} value={organization.id}>{organization.name}</option>)}</select>{error !== undefined && <span role="alert" className="cloud-org-switcher-error">{error instanceof Error ? error.message : t('组织切换失败', 'Could not switch organization')}</span>}</div>
}

function FleetSearch({ clusters, commands, hero, refHandle }: { clusters: HubCluster[]; commands: CommandItem[]; hero?: boolean; refHandle: React.RefObject<OmnibarHandle | null> }) {
  const { t } = useLocale()
  const [search, setSearch] = useState({ query: '', open: false })
  const query = useFleetQuery<SearchResult>(clusters, `/search?${new URLSearchParams({ q: search.query, limit: '20', include: 'none', context: 'summary' })}`, search.open && search.query.trim().length >= 2)
  const onQueryChange = useCallback((value: string, open: boolean) => setSearch(previous => previous.query === value && previous.open === open ? previous : { query: value, open }), [])
  const results = query.data ?? []
  const failed = results.filter(result => result.error)
  const hits = results.flatMap(result => (result.data?.hits ?? []).map(hit => ({ ...hit, cluster: result.cluster.id, clusterName: result.cluster.name }))).sort((a, b) => b.score - a.score)
  const incomplete = results.some(result => result.error || result.data?.access_incomplete || result.data?.incomplete_findings)
  return <Omnibar ref={refHandle} onOpenResource={hit => navigateCloud(resourceHref({ ...hit, cluster_id: hit.cluster }))} commandItems={commands} onQueryChange={onQueryChange} searchData={query.data ? { hits, total: hits.length, total_matched: results.reduce((total, result) => total + (result.data?.total_matched ?? 0), 0), access_incomplete: incomplete } : undefined} isFetching={query.isFetching} isError={!!query.error || !!results.length && failed.length === results.length} note={failed.length ? { text: t(`未搜索：${failed.map(result => result.cluster.name).join('、')}`, `Skipped: ${failed.map(result => result.cluster.name).join(', ')}`) } : undefined} placeholder={t('搜索所有集群中的资源…', 'Search resources across clusters…')} size={hero ? 'hero' : 'default'} launcherShowsViews={false} />
}

function SelectLiveCluster({ clusters, view }: { clusters: HubCluster[]; view: string }) {
  const { t } = useLocale()
  const name = view === '/investigations' ? t('AI 调查', 'AI Investigations') : navigationItems(t).find(item => item.path === view)?.label ?? t('集群视图', 'Cluster view')
  return <div className="cloud-page"><div className="cloud-page-heading"><div><h1>{name}</h1><p>{t(`选择集群以打开“${name}”实时工作区。`, `Select a cluster to open the ${name} workspace.`)}</p></div></div><div className="cloud-cluster-picker-grid">{clusters.map(cluster => <CloudLink className="cloud-card" key={cluster.id} href={clusterPage(cluster.id, view)}><Server size={20} /><h2>{cluster.name}</h2><p>{cluster.state === 'connected' ? t('打开实时工作区 →', 'Open live workspace →') : t('不可用 — Agent 尚未连接', 'Unavailable — Agent is disconnected')}</p></CloudLink>)}</div>{!clusters.length && <CloudLink className="cloud-button cloud-primary" href="/install"><Plus size={16} />{t('连接集群', 'Connect cluster')}</CloudLink>}</div>
}

function CloudPageNotFound() {
  const { t } = useLocale()
  return <div className="cloud-page cloud-empty"><h1>{t('页面不存在', 'Page not found')}</h1><CloudLink href="/" className="cloud-button">{t('返回首页', 'Back to home')}</CloudLink></div>
}

function CloudAgents({ hub, readOnlyOnly = false }: { hub: HubContext; readOnlyOnly?: boolean }) {
  const { t } = useLocale()
  const organizationName = hub.organizations.find(organization => organization.id === hub.activeOrganizationId)?.name ?? hub.activeOrganizationId ?? t('未知', 'Unknown')
  return <div className="cloud-page cloud-agents"><MCPCredentials readOnlyOnly={readOnlyOnly} organizationId={hub.activeOrganizationId ?? ''} organizationName={organizationName} /></div>
}

function CloudSettings({ path, dark, onTheme, onLogout, hub }: { path: string; dark: boolean; onTheme: () => void; onLogout: () => void; hub: HubContext }) {
  const { t } = useLocale()
  const settingGroups = [
    { id: 'personal', label: t('个人', 'Personal'), items: [['', t('偏好设置', 'Preferences')], ['/sessions', t('会话', 'Sessions')]] },
    { id: 'organization', label: t('组织', 'Organization'), items: [
      ['/organization', t('概览', 'Overview')], ['/organization/members', t('成员', 'Members')],
      ['/organization/agent-access', 'MCP'], ['/organization/sso', t('单点登录', 'Single sign-on')],
    ] },
  ]
  const member = hub.member
  let content: ReactNode
  if (path === '/settings') content = <div className="cloud-page">
    <div className="cloud-page-heading">
      <div><h1>{t('偏好设置', 'Preferences')}</h1><p>{t('自定义此浏览器中的工作区。', 'Customize your workspace in this browser.')}</p></div>
    </div>
    <section className="cloud-card">
      <h2>{t('外观', 'Appearance')}</h2>
      <div className="cloud-preference">
        <div><strong>{t('主题', 'Theme')}</strong><p>{t('Portal 与嵌入式集群视图共用此主题。', 'The portal and embedded cluster views share this theme.')}</p></div>
        <button className="cloud-button" onClick={onTheme}>{dark ? <Sun size={16} /> : <Moon size={16} />}{dark ? t('切换为浅色', 'Switch to light') : t('切换为深色', 'Switch to dark')}</button>
      </div>
      <div className="cloud-preference">
        <PortalLanguageSelect />
      </div>
    </section>
  </div>
  else if (path === '/settings/sessions') content = <div className="cloud-page"><h1>{t('会话', 'Sessions')}</h1><section className="cloud-card"><h2>{t('当前浏览器会话', 'Current browser session')}</h2><p>{t('退出登录会撤销此会话并关闭其实时数据流，但不会撤销独立的 MCP 凭据。', 'Signing out ends this session and closes its live streams. Independent MCP credentials remain active.')}</p><button onClick={onLogout} className="cloud-button">{t('退出登录', 'Sign out')}</button></section></div>
  else if (path === '/settings/organization') content = <CloudOrganization member={member} platformOwner={hub.platformOwner} activeOrganizationId={hub.activeOrganizationId} />
  else if (path === '/settings/organization/members') content = <CloudMembers member={member} platformOwner={hub.platformOwner} activeOrganizationId={hub.activeOrganizationId} />
  else if (path === '/settings/organization/sso') content = <CloudSSO />
  else if (path === '/settings/organization/agent-access') content = <CloudAgents hub={hub} readOnlyOnly={member?.role === 'viewer'} />
  else content = <CloudPageNotFound />
  return <div className="cloud-settings"><aside><h1>{t('设置', 'Settings')}</h1>{settingGroups.map(group => <section key={group.id}><h2>{group.label}</h2>{group.items.map(([suffix, label]) => <CloudLink key={suffix} href={`/settings${suffix}`} aria-current={path === `/settings${suffix}` ? 'page' : undefined}>{label}</CloudLink>)}</section>)}</aside><div className="cloud-settings-main">{content}</div></div>
}

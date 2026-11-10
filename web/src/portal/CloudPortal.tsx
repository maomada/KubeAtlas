import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ComponentType, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { Activity, AlertTriangle, Bell, Bot, Boxes, ChevronDown, ChevronsLeft, ClipboardCheck, Clock, Coins, ExternalLink, Gauge, GitBranch, HelpCircle, Home, Layers, LogOut, Menu, Moon, Network, Package, Plus, Server, Settings, ShieldCheck, Sparkles, Sun } from 'lucide-react'
import { ClusterSwitcher, FilterLocationProvider, NamespacePicker, Tooltip, type FilterLocation } from '@maomada/k8s-ui'
import { Omnibar, type OmnibarHandle } from '../components/ui/Omnibar'
import type { CommandItem } from '../components/ui/command-items'
import { refreshAfterNamespaceSwitch, type NamespaceScope, type SearchResult } from '../api/client'
import type { APIResource } from '../types'
import type { AgentsResponse } from '../api/diagnose'
import { namespaceFilterDisabled } from '../utils/namespaceFilter'
import { CloudRequestError, cloudRequest, clusterBase, clusterPage, clusterView, selectedCluster, useCloudQuery, type HubCluster } from './cloudApi'
import { useFleetQuery } from './cloudFleet'
import { CloudDialog, CloudError, CloudLink, CloudLoading, CopyButton } from './CloudShared'
import { CloudClusters, CloudInstall } from './CloudClusters'
import { CloudHome } from './CloudHome'
import { CloudApplications, CloudCertificates, CloudChecks, CloudIssues, resourceHref } from './CloudFleetViews'
import { MCPCredentials } from './MCPCredentials'
import { credentialStatus, type MCPCredential } from './credentials'
import { Portal } from './Portal'
import { cloudViewHref, navigateCloud, useCloudLocation } from './cloudNavigation'

const NativeRadar = lazy(() => import('../RadarApp').then(module => ({ default: module.RadarApp })))
const managementClient = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })

interface NavItem { label: string; path: string; icon: ComponentType<{ size?: number }>; cluster?: boolean }
const navigation: NavItem[] = [
  { label: 'Home', path: '/', icon: Home },
  { label: 'Issues', path: '/issues', icon: AlertTriangle },
  { label: 'Applications', path: '/applications', icon: Boxes },
  { label: 'Alerts', path: '/alerts', icon: Bell },
  { label: 'GitOps', path: '/gitops', icon: GitBranch, cluster: true },
  { label: 'Certs', path: '/certs', icon: ShieldCheck },
  { label: 'Checks', path: '/checks', icon: ClipboardCheck },
  { label: 'Resources', path: '/resources', icon: Layers, cluster: true },
  { label: 'Topology', path: '/topology', icon: Network, cluster: true },
  { label: 'Traffic', path: '/traffic', icon: Activity, cluster: true },
  { label: 'Cost', path: '/cost', icon: Coins, cluster: true },
  { label: 'Capacity', path: '/capacity', icon: Gauge, cluster: true },
  { label: 'Timeline', path: '/timeline', icon: Clock, cluster: true },
  { label: 'Helm', path: '/helm', icon: Package, cluster: true },
]

export function PortalEntry() {
  return <QueryClientProvider client={managementClient}><PortalMode /></QueryClientProvider>
}

function PortalMode() {
  const mode = useCloudQuery<{ enabled: boolean; userId: string }>('/hub')
  if (mode.isPending) return <CloudLoading label="Loading Portal…" />
  if ((mode.error instanceof CloudRequestError && mode.error.status === 404) || mode.data?.enabled === false) return <Portal />
  if (mode.error || mode.data?.enabled !== true) return <div className="cloud-page"><CloudError error={mode.error ?? new Error('Invalid Hub availability response')} retry={() => { void mode.refetch() }} /></div>
  return <CloudShell userId={mode.data.userId} />
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
  return <NamespacePicker scope={namespace.data} loading={namespace.isPending} pending={pending} disabled={filter.disabled} disabledTooltip={filter.tooltip} onApply={onApply} variant="segment" label="Namespace" />
}

function InvestigationGate({ id, children }: { id: string; children: ReactNode }) {
  const agents = useCloudQuery<AgentsResponse>('/agents', clusterBase(id))
  if (agents.isPending) return <CloudLoading label="Checking investigation availability…" />
  if (agents.error) return <CloudError error={agents.error} retry={() => { void agents.refetch() }} />
  if (agents.data?.eligible !== true) return <div className="cloud-page cloud-empty"><Sparkles size={30} /><h1>AI investigations unavailable</h1><p>This cluster does not advertise support for built-in investigations in its current deployment mode. Installing a local agent CLI alone does not enable investigations through an authenticated Hub.</p><CloudLink href="/agents" className="cloud-button">Connect an external AI client with scoped MCP access</CloudLink></div>
  return children
}

function CloudShell({ userId }: { userId: string }) {
  const location = useCloudLocation()
  const directory = useCloudQuery<{ clusters: HubCluster[] }>('/hub/clusters')
  const credentials = useCloudQuery<{ credentials: MCPCredential[]; serverTime: string }>('/mcp/credentials')
  const clusters = directory.data?.clusters ?? []
  const id = selectedCluster(location.pathname)
  const cluster = clusters.find(item => item.id === id)
  const view = id ? clusterView(location.pathname) : location.pathname
  const [menuOpen, setMenuOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [panel, setPanel] = useState<'help' | 'account' | 'notifications' | 'setup'>()
  const [dark, setDark] = useState(() => { try { return localStorage.getItem('radar-theme') === 'dark' } catch { return false } })
  const [actionError, setActionError] = useState<unknown>()
  const [namespaceError, setNamespaceError] = useState<{ client: QueryClient; error: unknown }>()
  const [pendingNamespace, setPendingNamespace] = useState(false)
  const searchRef = useRef<OmnibarHandle>(null)
  // Recreate the native cache on cluster changes to avoid cross-cluster data.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const nativeClient = useMemo(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }), [id])
  const reportNamespaceError = useCallback((error: unknown) => {
    setNamespaceError(error == null ? undefined : { client: nativeClient, error })
  }, [nativeClient])
  const chosenID = id ?? location.searchParams.get('cluster') ?? ''
  const fleetScope = chosenID ? clusters.filter(item => item.id === chosenID) : clusters
  const missingSelection = chosenID && !clusters.some(item => item.id === chosenID)
  const credentialTime = Date.parse(credentials.data?.serverTime ?? '')
  const setupDone = Number(clusters.some(item => item.state === 'connected')) + Number(credentials.data?.credentials.some(credential => credentialStatus(credential, credentialTime) === 'active') ?? false)

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
    try { localStorage.setItem('radar-theme', dark ? 'dark' : 'light') } catch { /* Browser preferences are optional. */ }
    window.dispatchEvent(new StorageEvent('storage', { key: 'radar-theme', newValue: dark ? 'dark' : 'light' }))
  }, [dark])
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      const typing = (event.target as HTMLElement | null)?.closest('input, textarea, [contenteditable="true"]')
      if ((event.key === 'k' && (event.ctrlKey || event.metaKey)) || (event.key === '/' && !typing)) { event.preventDefault(); searchRef.current?.focus() }
      if (event.key === 'Escape') { setPanel(undefined); setMenuOpen(false) }
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
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
    try { await cloudRequest('/auth/logout', { method: 'POST', body: {} }); managementClient.clear(); nativeClient.clear(); window.location.replace('/login') }
    catch (error) { setActionError(error) }
  }

  const investigationView = view === '/investigations' || view.startsWith('/investigations/')
  const title = investigationView ? 'AI investigations' : navigation.find(item => item.path === view || item.path !== '/' && view.startsWith(item.path + '/'))?.label ?? (view.startsWith('/settings') ? 'Settings' : view === '/agents' ? 'Agents' : view === '/install' ? 'Connect cluster' : 'Clusters')
  useEffect(() => { document.title = `${cluster ? cluster.name + ' · ' : ''}${title} — KubeAtlas` }, [title, cluster])
  const isHome = location.pathname === '/'
  const search = <FleetSearch refHandle={searchRef} clusters={clusters} commands={commands} hero={isHome} />
  const nativePath = id && !!cluster && cluster.state === 'connected'
  let content
  if (directory.isPending) content = <CloudLoading label="Loading cluster directory…" />
  else if (directory.error) content = <CloudError error={directory.error} retry={() => { void directory.refetch() }} />
  else if (location.pathname.startsWith('/c/') && !id || missingSelection) content = <CloudError error={new Error('Invalid or unknown cluster selection')} />
  else if (id && !nativePath) content = <div className="cloud-page"><CloudError error={new Error(cluster?.revokedAt ? 'Agent access is revoked. Rotate its token to reconnect.' : 'Cluster is offline. Live data is unavailable; no snapshot is substituted.')} /><CloudLink href="/clusters" className="cloud-button">Manage cluster</CloudLink></div>
  else if (nativePath) content = <Suspense fallback={<CloudLoading label="Opening live cluster…" />}><NativeRadar key={id} apiBase={clusterBase(id)} basename={`/c/${id}`} queryClient={nativeClient} navSlots={{ embedded: true, fleetTakeoverHref: target => target === 'gitops' ? undefined : `/${target}?cluster=${encodeURIComponent(id)}`, onHostNavigate: navigateCloud }} /></Suspense>
  else if (view === '/') content = <CloudHome clusters={fleetScope} search={search} />
  else if (view === '/clusters') content = <CloudClusters clusters={clusters} />
  else if (view === '/install') content = <CloudInstall />
  else if (view === '/applications') content = <CloudApplications clusters={fleetScope} />
  else if (view === '/issues') content = <CloudIssues clusters={fleetScope} />
  else if (view === '/checks') content = <CloudChecks clusters={fleetScope} />
  else if (view === '/certs') content = <CloudCertificates clusters={fleetScope} />
  else if (view === '/agents' || view === '/mcp-access') content = <CloudAgents />
  else if (view.startsWith('/settings')) content = <CloudSettings path={view} dark={dark} onTheme={() => setDark(!dark)} onLogout={() => { void logout() }} />
  else if (navigation.some(item => item.path === view && item.cluster) || view === '/investigations') content = <SelectLiveCluster clusters={fleetScope} view={view} />
  else content = <CloudCapabilityGap title={view === '/alerts' ? 'Alerts' : 'Page unavailable'} description={view === '/alerts' ? 'Organization alert rules, delivery integrations and notification history are not implemented in this self-hosted Hub yet. The live Issues queue is available; it is not a substitute for alert delivery.' : 'This route is not implemented in the self-hosted Portal.'} />

  if (nativePath && investigationView) content = <InvestigationGate key={id} id={id}>{content}</InvestigationGate>

  return <FilterLocationProvider value={filterLocation}><div className={`cloud-shell ${collapsed ? 'cloud-collapsed' : ''} ${menuOpen ? 'cloud-menu-open' : ''}`}>
    {menuOpen && <button className="cloud-nav-backdrop" aria-label="Close navigation" onClick={() => setMenuOpen(false)} />}
    <aside className="cloud-sidebar" aria-label="Primary navigation">
      <CloudLink href="/" className="cloud-brand" aria-label="KubeAtlas Home"><span className="cloud-brand-mark"><Layers size={21} /></span><strong>KubeAtlas</strong><small>HUB</small></CloudLink>
      <nav aria-label="Main navigation">{navigation.map((item, index) => <CloudLink key={item.path} href={target(item)} aria-label={item.label} title={collapsed ? item.label : undefined} className={`cloud-nav-item ${index === 7 ? 'cloud-nav-separated' : ''}`} aria-current={view === item.path || item.path !== '/' && view.startsWith(item.path + '/') ? 'page' : undefined}><item.icon size={17} /><span>{item.label}</span></CloudLink>)}</nav>
      <div className="cloud-sidebar-bottom">
        <CloudLink href="/clusters" className="cloud-nav-item" aria-label="Clusters" aria-current={view === '/clusters' ? 'page' : undefined}><Server size={17} /><span>Clusters</span><small>{clusters.length}</small></CloudLink>
        <CloudLink href="/agents" className="cloud-nav-item" aria-label="Agents" aria-current={view === '/agents' || view === '/mcp-access' ? 'page' : undefined}><Bot size={17} /><span>Agents</span></CloudLink>
        <CloudLink href="/settings" className="cloud-nav-item" aria-label="Settings" aria-current={view.startsWith('/settings') ? 'page' : undefined}><Settings size={17} /><span>Settings</span></CloudLink>
        <button className="cloud-setup" onClick={() => setPanel('setup')}><span>Setup</span><small>{credentials.isSuccess ? `${setupDone} of 2 done` : 'Checking access…'}</small><span className="cloud-setup-bar"><i style={{ width: `${setupDone * 50}%` }} /></span></button>
        <div className="cloud-sidebar-footer"><button aria-label={dark ? 'Use light theme' : 'Use dark theme'} onClick={() => setDark(!dark)}>{dark ? <Sun size={17} /> : <Moon size={17} />}</button><button aria-label={collapsed ? 'Expand navigation' : 'Collapse navigation'} onClick={() => setCollapsed(!collapsed)}><ChevronsLeft size={17} /></button></div>
      </div>
    </aside>
    <div className="cloud-main"><header className="cloud-topbar">
      <button className="cloud-mobile-toggle cloud-icon-button" aria-label="Open navigation" onClick={() => setMenuOpen(true)}><Menu size={20} /></button>
      <div className="cloud-topbar-left cloud-scope"><ClusterSwitcher currentId={chosenID} currentName={clusters.find(item => item.id === chosenID)?.name ?? 'All clusters'} items={[{ id: '', name: 'All clusters' }, ...clusters.map(item => ({ id: item.id, name: item.name, status: item.state === 'connected' ? 'healthy' as const : 'unknown' as const, secondary: item.environment, disabled: item.state === 'revoked', title: item.state === 'revoked' ? 'Agent access revoked' : undefined }))]} onSelect={item => selectCluster(item.id)} label="Cluster" footerSlot={<CloudLink href="/clusters">Manage clusters →</CloudLink>} />{id && <ClusterNamespacePicker key={id} id={id} client={nativeClient} connected={cluster?.state === 'connected'} view={view} search={location.search} pending={pendingNamespace} onApply={values => { void applyNamespace(values) }} onError={reportNamespaceError} />}</div>
      {!isHome && <div className="cloud-topbar-search">{search}</div>}
      <div className="cloud-topbar-actions">
        <CloudLink href={target({ path: '/investigations', cluster: true, label: 'AI investigations', icon: Sparkles })} className="cloud-icon-button" aria-label="AI investigations" title="AI investigations"><Sparkles size={18} /></CloudLink>
        <button className="cloud-icon-button" aria-label="Notifications" onClick={() => setPanel('notifications')}><Bell size={18} /></button>
        <button className="cloud-icon-button" aria-label="Help" onClick={() => setPanel('help')}><HelpCircle size={18} /></button>
        <Tooltip content={userId || 'Account'} position="bottom"><button className="cloud-account" aria-label="Account menu" onClick={() => setPanel(panel === 'account' ? undefined : 'account')}>{userId?.slice(0, 1).toUpperCase() || 'K'}<ChevronDown size={12} /></button></Tooltip>
      </div>
    </header>
      {actionError !== undefined && <CloudError error={actionError} />}{namespaceError?.client === nativeClient && <CloudError error={namespaceError.error} />}
      <main className={nativePath ? 'cloud-content cloud-native' : 'cloud-content'}>{content}</main>
    </div>
    {panel === 'help' && <CloudDialog title="Help" onClose={() => setPanel(undefined)}><div className="cloud-form"><p>Self-hosted KubeAtlas Hub. Current state is queried through each cluster's outbound Agent connection.</p><a className="cloud-button" target="_blank" rel="noopener noreferrer" href="https://github.com/maomada/KubeAtlas/tree/main/docs">Project documentation<ExternalLink size={14} /></a><p>Keyboard shortcuts: Ctrl/Cmd+K or / opens fleet search. Escape closes menus.</p></div></CloudDialog>}
    {panel === 'notifications' && <CloudDialog title="Notifications" onClose={() => setPanel(undefined)}><div className="cloud-form"><p>Notification delivery and history are not configured in this self-hosted Hub. This is not an empty inbox confirmation.</p><CloudLink href="/issues" className="cloud-button" onClick={() => setPanel(undefined)}>View live issues</CloudLink></div></CloudDialog>}
    {panel === 'account' && <CloudDialog title="Account" onClose={() => setPanel(undefined)}><div className="cloud-form"><strong className="cloud-account-identity">{userId}</strong><p>Authenticated Portal session. MCP tokens have their own expiry and do not share this session.</p><CloudLink href="/settings" className="cloud-button">Preferences</CloudLink><button className="cloud-button" onClick={() => { void logout() }}><LogOut size={16} />Sign out</button></div></CloudDialog>}
    {panel === 'setup' && <CloudDialog title="Set up your self-hosted Hub" onClose={() => setPanel(undefined)}><div className="cloud-form"><CloudLink href="/install" className="cloud-button"><Server size={16} />1. Connect a cluster</CloudLink><CloudLink href="/agents" className="cloud-button"><Bot size={16} />2. Create scoped MCP access</CloudLink><p>Cluster connection is verified live. Token client use must be checked in your chosen MCP client; creating a token alone is not a verified agent connection.</p></div></CloudDialog>}
  </div></FilterLocationProvider>
}

function FleetSearch({ clusters, commands, hero, refHandle }: { clusters: HubCluster[]; commands: CommandItem[]; hero?: boolean; refHandle: React.RefObject<OmnibarHandle | null> }) {
  const [search, setSearch] = useState({ query: '', open: false })
  const query = useFleetQuery<SearchResult>(clusters, `/search?${new URLSearchParams({ q: search.query, limit: '20', include: 'none', context: 'summary' })}`, search.open && search.query.trim().length >= 2)
  const onQueryChange = useCallback((value: string, open: boolean) => setSearch(previous => previous.query === value && previous.open === open ? previous : { query: value, open }), [])
  const results = query.data ?? []
  const failed = results.filter(result => result.error)
  const hits = results.flatMap(result => (result.data?.hits ?? []).map(hit => ({ ...hit, cluster: result.cluster.id, clusterName: result.cluster.name }))).sort((a, b) => b.score - a.score)
  const incomplete = results.some(result => result.error || result.data?.access_incomplete || result.data?.incomplete_findings)
  return <Omnibar ref={refHandle} onOpenResource={hit => navigateCloud(resourceHref({ ...hit, cluster_id: hit.cluster }))} commandItems={commands} onQueryChange={onQueryChange} searchData={query.data ? { hits, total: hits.length, total_matched: results.reduce((total, result) => total + (result.data?.total_matched ?? 0), 0), access_incomplete: incomplete } : undefined} isFetching={query.isFetching} isError={!!query.error || !!results.length && failed.length === results.length} note={failed.length ? { text: `Not searched: ${failed.map(result => result.cluster.name).join(', ')}` } : undefined} placeholder="Search resources across your fleet…" size={hero ? 'hero' : 'default'} launcherShowsViews={false} />
}

function SelectLiveCluster({ clusters, view }: { clusters: HubCluster[]; view: string }) {
  const name = view === '/investigations' ? 'AI investigations' : navigation.find(item => item.path === view)?.label ?? 'Cluster view'
  return <div className="cloud-page"><div className="cloud-page-heading"><div><h1>{name}</h1><p>Select a cluster to open its live {name.toLowerCase()} workspace.</p></div></div><div className="cloud-cluster-picker-grid">{clusters.map(cluster => <CloudLink className="cloud-card" key={cluster.id} href={clusterPage(cluster.id, view)}><Server size={20} /><h2>{cluster.name}</h2><p>{cluster.state === 'connected' ? 'Open live workspace →' : 'Unavailable — Agent is not connected'}</p></CloudLink>)}</div>{!clusters.length && <CloudLink className="cloud-button cloud-primary" href="/install"><Plus size={16} />Connect a cluster</CloudLink>}</div>
}

function CloudCapabilityGap({ title, description }: { title: string; description: string }) {
  return <div className="cloud-page"><div className="cloud-page-heading"><div><h1>{title}</h1><p>Not implemented in this Hub</p></div></div><section className="cloud-card"><h2>Capability unavailable</h2><p>{description}</p><CloudLink href="/clusters" className="cloud-text-button">View real cluster connections →</CloudLink></section></div>
}

function CloudAgents() {
  const endpoint = `${window.location.origin}/mcp-fleet`
  return <div className="cloud-page cloud-agents"><div className="cloud-page-heading"><div><h1>Agents</h1><p>Connect an AI agent to your Kubernetes fleet through one MCP endpoint.</p></div></div><section className="cloud-card"><h2><Bot size={18} />Connect an AI agent</h2><p>Choose Streamable HTTP in your MCP client. Generate a scoped token below, then use its OpenCode configuration. OAuth is not implemented; keep OAuth disabled.</p><div className="cloud-endpoint"><code>{endpoint}</code><CopyButton value={endpoint} label="Copy endpoint" /></div><p className="cloud-muted">The source cluster still checks Kubernetes RBAC for every user. A writable token does not grant cluster-admin permissions.</p></section><MCPCredentials /></div>
}

const settingGroups = [
  { label: 'Personal', items: [['', 'Preferences'], ['/sessions', 'Sessions'], ['/security', 'Security']] },
  { label: 'Organization', items: [['/organization', 'Overview'], ['/organization/members', 'Members'], ['/organization/agent-access', 'AI & MCP'], ['/organization/enrollment-keys', 'Enrollment keys'], ['/organization/activity', 'Activity'], ['/organization/issues', 'Issues'], ['/organization/checks', 'Checks'], ['/organization/sso', 'SSO & directory sync']] },
  { label: 'Advanced', items: [['/organization/notifications', 'Notifications'], ['/organization/integrations', 'Integrations']] },
]

function CloudSettings({ path, dark, onTheme, onLogout }: { path: string; dark: boolean; onTheme: () => void; onLogout: () => void }) {
  const selected = settingGroups.flatMap(group => group.items).find(([suffix]) => path === `/settings${suffix}`)
  return <div className="cloud-settings"><aside><h1>Settings</h1>{settingGroups.map(group => <section key={group.label}><h2>{group.label}</h2>{group.items.map(([suffix, label]) => <CloudLink key={suffix} href={`/settings${suffix}`} aria-current={path === `/settings${suffix}` ? 'page' : undefined}>{label}</CloudLink>)}</section>)}</aside><div className="cloud-settings-main">{path === '/settings' ? <div className="cloud-page"><div className="cloud-page-heading"><div><h1>Preferences</h1><p>Personalize this browser's workspace.</p></div></div><section className="cloud-card"><h2>Appearance</h2><div className="cloud-preference"><div><strong>Theme</strong><p>Shared by Portal and embedded cluster views.</p></div><button className="cloud-button" onClick={onTheme}>{dark ? <Sun size={16} /> : <Moon size={16} />}{dark ? 'Switch to light' : 'Switch to dark'}</button></div></section></div> : path === '/settings/sessions' ? <div className="cloud-page"><h1>Sessions</h1><section className="cloud-card"><h2>Current browser session</h2><p>Sign out revokes this session and closes its live streams. This does not revoke independent MCP credentials.</p><button onClick={onLogout} className="cloud-button">Sign out</button></section></div> : path === '/settings/organization/agent-access' ? <CloudAgents /> : <CloudCapabilityGap title={selected?.[1] ?? 'Settings'} description="This organization-level control plane is not implemented yet. No settings, memberships or audit history are fabricated here. Cluster-local controls remain subject to the source's Kubernetes RBAC." />}</div></div>
}

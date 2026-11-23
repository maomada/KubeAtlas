import { useLocale, type Translate } from '@maomada/k8s-ui/hooks/useLocale'
import { useState, type FormEvent } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { Badge } from '@maomada/k8s-ui'
import { Building2, Search, ShieldCheck, Users } from 'lucide-react'
import { cloudRequest, useCloudQuery } from './cloudApi'
import { CloudDialog, CloudError, CloudLink, CloudLoading } from './CloudShared'
import { invalidateHubYamlDrafts, reportHubYamlLogoutFailure } from './hubYamlDrafts'
import { ThemeToggle } from './ThemeToggle'
import { ConnectionSettingsEditor } from './ConnectionProfiles'
import { replacePortalLocation } from './portalNavigation'

export type OrganizationRole = 'owner' | 'member' | 'viewer'

const organizationNameQueryPaths = ['/hub', '/hub/organization', '/hub/organizations'] as const
const memberMutationQueryPaths = [
  '/hub', '/hub/organization', '/hub/organizations', '/hub/members', '/hub/clusters',
  '/hub/platform/users', '/hub/platform/clusters', '/mcp/credentials',
] as const

function invalidatePortalQueries(client: QueryClient, paths: readonly string[]) {
  return Promise.all(paths.map(path => client.invalidateQueries({ queryKey: ['portal', '/api', path] })))
}

export interface HubOrganization {
  id: string
  name: string
  role?: OrganizationRole
}

export interface HubMember {
  id: string
  userId?: string
  displayName: string
  email?: string
  role?: OrganizationRole
  status?: 'active' | 'pending' | 'disabled'
}

export interface HubContext {
  enabled: boolean
  userId: string
  authorizationRevision: string
  member?: HubMember | null
  platformOwner: boolean
  activeOrganizationId: string | null
  organizations: HubOrganization[]
}

interface Organization { id: string; name: string }
interface SSOConfiguration {
  enabled: boolean
  issuer?: string
  clientId?: string
  redirectUrl?: string
  provisioning: 'approval'
  directorySync: boolean
  backchannelLogout: boolean
}

export function canManageOrganization(member?: HubMember | null, platformOwner = false, activeOrganizationId?: string | null) {
  return !!activeOrganizationId && (member?.status === 'active' && member.role === 'owner' || platformOwner)
}

export async function logoutPortal(t: Translate = zh => zh) {
  const draftsCleared = invalidateHubYamlDrafts()
  let logoutCompleted = false
  try {
    const result = await cloudRequest<{ logoutUrl: string } | undefined>('/auth/logout', { method: 'POST', body: {} })
    logoutCompleted = true
    if (!draftsCleared) {
      throw new Error(t('服务器已完成退出登录，但未能删除已保存的 YAML 草稿。此标签页仍保持锁定。', 'The server signed you out, but saved YAML drafts could not be deleted. This tab stays locked.'))
    }
    replacePortalLocation(result?.logoutUrl ?? '/login')
  } catch (error) {
    const failure = !draftsCleared && !logoutCompleted
      ? new Error(t(`无法删除已保存的 YAML 草稿；服务器退出登录也失败：${error instanceof Error ? error.message : String(error)}`, `Saved YAML drafts could not be deleted and the server could not sign you out: ${error instanceof Error ? error.message : String(error)}`), { cause: error })
      : error
    reportHubYamlLogoutFailure(failure)
    throw failure
  }
}

export function PendingMembership({ member, retry }: { member: HubMember; retry: () => void }) {
  const { t } = useLocale()
  const [error, setError] = useState<unknown>()
  return <div className="cloud-page cloud-empty"><ShieldCheck size={36} /><h1>{t('等待平台 Owner 审批', 'Awaiting platform owner approval')}</h1>
    <p>{t('账号等待平台 Owner 审批。此状态不代表身份或邮箱已验证。', 'A platform owner must approve this account. The approval process does not verify your identity or email.')}</p>
    <strong className="cloud-account-identity">{member.displayName}</strong>
    <p>{t('审批完成前，不会加载集群目录、连接、MCP 凭据或原生工作区数据。', 'Cluster directories, connections, MCP credentials, and workspace data are available after approval.')}</p>
    {error !== undefined && <CloudError error={error} />}
    <div className="cloud-row-actions"><ThemeToggle /><button className="cloud-button" onClick={retry}>{t('重新检查审批', 'Check approval')}</button><button className="cloud-button" onClick={() => { void logoutPortal(t).catch(setError) }}>{t('退出登录', 'Sign out')}</button></div>
  </div>
}

export function DisabledMembership({ retry }: { retry: () => void }) {
  const { t } = useLocale()
  const [error, setError] = useState<unknown>()
  return <div className="cloud-page cloud-empty"><ShieldCheck size={36} /><h1>{t('账号已停用', 'Account disabled')}</h1><p>{t('此账号无法访问组织资源。如有疑问，请联系平台 Owner。', 'Contact a platform owner to restore this account\'s access to organization resources.')}</p>
    {error !== undefined && <CloudError error={error} />}
    <div className="cloud-row-actions"><ThemeToggle /><button className="cloud-button" onClick={retry}>{t('重新检查账号状态', 'Check account status')}</button><button className="cloud-button" onClick={() => { void logoutPortal(t).catch(setError) }}>{t('退出登录', 'Sign out')}</button></div>
  </div>
}

export function OrganizationSelection({ organizations, activeOrganizationId, platformOwner }: {
  organizations: HubOrganization[]; activeOrganizationId: string | null; platformOwner: boolean
}) {
  const { t } = useLocale()
  const client = useQueryClient()
  const [organizationId, setOrganizationId] = useState(activeOrganizationId ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  async function select(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || !organizations.some(organization => organization.id === organizationId)) return
    setBusy(true); setError(undefined)
    try {
      await cloudRequest('/hub/organization/select', { method: 'POST', body: { organizationId } })
      await client.cancelQueries()
      client.clear()
      replacePortalLocation('/')
    } catch (failure) { setError(failure); setBusy(false) }
  }
  return <div className="cloud-page cloud-narrow">
    <div className="cloud-page-heading"><div><h1>{t('选择组织', 'Select an organization')}</h1><p>{t('选择有效组织后才会加载组织数据。', 'Select an organization to load its data.')}</p></div></div>
    {organizations.length ? <section className="cloud-card"><form className="cloud-form" onSubmit={select} aria-busy={busy}>
      <label>{t('组织', 'Organization')}<select className="portal-control" required value={organizationId} disabled={busy} onChange={event => setOrganizationId(event.target.value)}><option value="">{t('选择组织', 'Select an organization')}</option>{organizations.map(organization => <option key={organization.id} value={organization.id}>{organization.name} · {organization.role === 'owner' ? t('组织 Owner', 'Organization owner') : organization.role === 'member' ? t('成员', 'Member') : t('只读成员', 'Viewer')}</option>)}</select></label>
      <button className="cloud-button cloud-primary" disabled={busy || !organizationId}>{busy ? t('正在切换…', 'Switching…') : t('进入组织', 'Open organization')}</button>
    </form></section> : <section className="cloud-card"><h2>{t('暂无组织成员资格', 'No organization memberships')}</h2><p>{t('当前没有可用组织。请联系平台 Owner 添加或审批成员资格。', 'Ask a platform owner to add or approve an organization membership.')}</p></section>}
    {platformOwner && <CloudLink href="/platform" className="cloud-button mt-4">{t('平台管理', 'Platform administration')}</CloudLink>}
    {error !== undefined && <CloudError error={error} />}
    <div className="mt-5 flex items-center gap-3"><ThemeToggle /><button className="cloud-button" onClick={() => { void logoutPortal(t).catch(setError) }}>{t('退出登录', 'Sign out')}</button></div>
  </div>
}

export function CloudOrganization({ member, platformOwner = false, activeOrganizationId = null }: {
  member?: HubMember | null; platformOwner?: boolean; activeOrganizationId?: string | null
}) {
  const { t } = useLocale()
  const organization = useCloudQuery<Organization>('/hub/organization')
  if (organization.isPending) return <CloudLoading label={t('正在读取组织信息…', 'Loading organization…')} />
  if (organization.error) return <CloudError error={organization.error} retry={() => { void organization.refetch() }} />
  if (!organization.data) return null
  const canManage = canManageOrganization(member, platformOwner, activeOrganizationId)
  return <div className="cloud-page cloud-narrow">
    <div className="cloud-page-heading"><div><h1>{t('组织设置', 'Organization settings')}</h1><p>{t('管理当前自托管 Hub 组织。', 'Manage this self-hosted Hub organization.')}</p></div><Badge tone="structural">{organization.data.name}</Badge></div>
    <section className="cloud-card"><header><h2><Building2 size={17} /> {t('组织详情', 'Organization details')}</h2></header>
      <OrganizationName key={organization.data.name} organization={organization.data} editable={canManage} />
    </section>
    <section className="cloud-card"><header><h2><Users size={17} /> {t('成员与角色', 'Members and roles')}</h2></header>
      <p>{t('你的组织角色：', 'Your organization role: ')}{member?.role === 'owner' ? t('组织 Owner', 'Organization owner') : member?.role === 'member' ? t('成员', 'Member') : member?.role === 'viewer' ? t('只读成员', 'Viewer') : t('平台 Owner', 'Platform owner')}</p>
      <p>{t('组织 Owner 可管理组织设置和成员角色，相当于 Kubernetes 管理员（不等同于 cluster-admin）；成员可编辑；只读成员仅可查看。集群资源权限由服务器端 Kubernetes RBAC 强制执行。', 'Organization owners manage settings and member roles and have the Kubernetes admin role. Members can edit, and viewers have read-only access. Kubernetes RBAC enforces access to cluster resources.')}</p>
      {canManage && <CloudLink href="/settings/organization/members" className="cloud-button">{t('管理成员', 'Manage members')}</CloudLink>}
    </section>
    <ConnectionSettingsEditor canManage={platformOwner} />
    <section className="cloud-card"><h2>{t('组织数据范围', 'Organization data')}</h2><p>{t('每个组织拥有独立的成员、集群目录和 MCP 凭据范围。', 'Each organization has its own members, cluster directory, and MCP credential scope.')}</p></section>
  </div>
}

function OrganizationName({ organization, editable }: { organization: Organization; editable: boolean }) {
  const { t } = useLocale()
  const client = useQueryClient()
  const [name, setName] = useState(organization.name)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  async function save(event: FormEvent) {
    event.preventDefault()
    if (busy || !editable) return
    setBusy(true); setError(undefined)
    try {
      const updated = await cloudRequest<Organization>('/hub/organization', { method: 'PUT', body: { name: name.trim() } })
      client.setQueryData(['portal', '/api', '/hub/organization'], updated)
      await invalidatePortalQueries(client, organizationNameQueryPaths)
    } catch (failure) { setError(failure) } finally { setBusy(false) }
  }
  return <form className="cloud-form" onSubmit={save}>
    <label>{t('组织名称', 'Organization name')}<input value={name} maxLength={128} required disabled={!editable || busy} onChange={event => setName(event.target.value)} /></label>
    <small>{t('组织 ID：', 'Organization ID: ')}{organization.id}</small>
    {error !== undefined && <CloudError error={error} />}
    {editable && <footer><button className="cloud-button cloud-primary" disabled={busy || !name.trim() || name.trim() === organization.name}>{busy ? t('正在保存…', 'Saving…') : t('保存更改', 'Save changes')}</button></footer>}
  </form>
}

export function CloudMembers({ member, platformOwner = false, activeOrganizationId = null }: {
  member?: HubMember | null; platformOwner?: boolean; activeOrganizationId?: string | null
}) {
  const { t } = useLocale()
  const manage = canManageOrganization(member, platformOwner, activeOrganizationId)
  const client = useQueryClient()
  const members = useCloudQuery<{ members: HubMember[] }>('/hub/members', '/api', manage)
  const [filter, setFilter] = useState('')
  const [roles, setRoles] = useState<Record<string, OrganizationRole>>({})
  const [busy, setBusy] = useState('')
  const [error, setError] = useState<unknown>()
  const [removing, setRemoving] = useState<HubMember>()
  if (!manage) return <div className="cloud-page"><CloudError error={new Error(t('仅组织 Owner，或已选择组织的 平台 Owner，可以管理成员。', 'Membership management requires an organization owner or a platform owner with an active organization selected.'))} /></div>
  const rows = members.data?.members.filter(item => `${item.displayName} ${item.email ?? ''} ${item.userId ?? ''} ${item.role}`.toLowerCase().includes(filter.toLowerCase())) ?? []

  async function saveRole(item: HubMember) {
    const role = roles[item.id] ?? item.role
    if (busy || role === item.role) return
    setBusy(item.id); setError(undefined)
    try {
      const result = await cloudRequest<{ member: HubMember }>(`/hub/members/${encodeURIComponent(item.id)}`, { method: 'PUT', body: { role } })
      client.setQueryData<{ members: HubMember[] }>(['portal', '/api', '/hub/members'], previous => previous ? { members: previous.members.map(existing => existing.id === item.id ? result.member : existing) } : previous)
      await invalidatePortalQueries(client, memberMutationQueryPaths)
      setRoles(current => { const next = { ...current }; delete next[item.id]; return next })
    } catch (failure) { setError(failure) } finally { setBusy('') }
  }

  async function removeMember() {
    if (!removing || busy) return
    setBusy(removing.id); setError(undefined)
    try {
      await cloudRequest(`/hub/members/${encodeURIComponent(removing.id)}`, { method: 'DELETE' })
      client.setQueryData<{ members: HubMember[] }>(['portal', '/api', '/hub/members'], previous => previous ? { members: previous.members.filter(existing => existing.id !== removing.id) } : previous)
      await invalidatePortalQueries(client, memberMutationQueryPaths)
      setRemoving(undefined)
    } catch (failure) { setError(failure) } finally { setBusy('') }
  }

  return <div className="cloud-page">
    <div className="cloud-page-heading"><div><h1>{t('成员管理', 'Members')}</h1><p>{t('管理当前组织的成员角色或移除成员。', 'Change roles or remove members from this organization.')}</p></div><Badge tone="structural">{members.data ? t(`${members.data.members.length} 位成员`, `${members.data.members.length} ${members.data.members.length === 1 ? 'member' : 'members'}`) : t('成员', 'Members')}</Badge></div>
    <section className="cloud-card"><h2>{t('组织角色', 'Organization role')}</h2><p>{t('组织 Owner 可管理组织设置和成员角色，相当于 Kubernetes 管理员（不等同于 cluster-admin）；成员可编辑；查看者只读。集群资源权限由服务器端 Kubernetes RBAC 强制执行。最后一位组织 Owner 由服务器保护。', 'Organization owners manage settings and member roles and have the Kubernetes admin role. Members can edit, and viewers have read-only access. Kubernetes RBAC enforces resource access. The server protects the last organization owner.')}</p></section>
    {error !== undefined && <CloudError error={error} />}
    {members.isPending ? <CloudLoading label={t('正在读取成员…', 'Loading members…')} /> : members.error ? <CloudError error={members.error} retry={() => { void members.refetch() }} /> : <section className="cloud-table-panel">
      <div className="cloud-table-filter"><label className="cloud-search"><Search size={16} /><input type="search" placeholder={t('搜索成员', 'Search members')} aria-label={t('搜索成员', 'Search members')} value={filter} onChange={event => setFilter(event.target.value)} /></label><span>{t(`${rows.length} 个结果`, `${rows.length} ${rows.length === 1 ? 'result' : 'results'}`)}</span></div>
      <div className="cloud-table-scroll"><table className="cloud-table cloud-members-table"><thead><tr><th>{t('成员', 'Member')}</th><th>{t('组织角色', 'Organization role')}</th><th>{t('操作', 'Actions')}</th></tr></thead><tbody>
        {rows.map(item => <tr key={item.id}><td><strong>{item.displayName}</strong>{item.id === member?.id && <small> {t('· 你', '· you')}</small>}<div className="cloud-member-id portal-mono">{item.email ?? item.userId ?? item.id}</div></td><td><select aria-label={t(`角色：${item.displayName}`, `Role: ${item.displayName}`)} className="portal-control" value={roles[item.id] ?? item.role} disabled={busy === item.id} onChange={event => setRoles(current => ({ ...current, [item.id]: event.target.value as OrganizationRole }))}><option value="owner">{t('组织 Owner', 'Organization owner')}</option><option value="member">{t('成员', 'Member')}</option><option value="viewer">{t('只读成员', 'Viewer')}</option></select></td><td><div className="cloud-row-actions"><button className="cloud-button" disabled={busy === item.id || (roles[item.id] ?? item.role) === item.role} onClick={() => { void saveRole(item) }}>{busy === item.id ? t('正在保存…', 'Saving…') : t('保存角色', 'Save role')}</button><button className="cloud-button" disabled={!!busy} onClick={() => { setRemoving(item); setError(undefined) }}>{t('移除', 'Remove')}</button></div></td></tr>)}
      </tbody></table></div>
    </section>}
    {removing && <CloudDialog title={t('移除组织成员', 'Remove organization member')} busy={!!busy} onClose={() => { if (!busy) setRemoving(undefined) }}><div className="cloud-form"><p>{t('确定从此组织移除', 'Remove')} {removing.displayName}{t('？移除会撤销该组织范围内的会话和凭据，但不会停用此平台账号，也不会移除其在其他组织的成员资格。', ' from this organization? This revokes the member\'s sessions and credentials for this organization. The account and its other memberships stay active.')}</p><p>{t('最后一位组织 Owner 由服务器保护。', 'The server protects the last organization owner.')}</p>{error !== undefined && <CloudError error={error} />}<footer><button type="button" className="cloud-button" disabled={!!busy} onClick={() => setRemoving(undefined)}>{t('取消', 'Cancel')}</button><button type="button" className="cloud-button cloud-primary" disabled={!!busy} onClick={() => { void removeMember() }}>{busy ? t('正在移除…', 'Removing…') : t('确认移除', 'Confirm removal')}</button></footer></div></CloudDialog>}
  </div>
}

export function CloudSSO() {
  const { t } = useLocale()
  const configuration = useCloudQuery<SSOConfiguration>('/hub/sso')
  if (configuration.isPending) return <CloudLoading label={t('正在读取身份提供方配置…', 'Loading identity provider configuration…')} />
  if (configuration.error) return <CloudError error={configuration.error} retry={() => { void configuration.refetch() }} />
  const sso = configuration.data
  if (!sso) return null
  return <div className="cloud-page cloud-narrow"><div className="cloud-page-heading"><div><h1>{t('单点登录', 'Single sign-on')}</h1><p>{t('使用组织身份提供方登录。', 'Sign in with the organization\'s identity provider.')}</p></div></div>
    <section className="cloud-card"><h2>{t('OIDC 配置', 'OIDC configuration')}</h2><p>{t('状态：', 'Status: ')}{sso.enabled ? t('已配置', 'Configured') : t('未配置', 'Not configured')}</p>
      <dl className="cloud-sso-details"><dt>{t('签发方（Issuer）', 'Issuer')}</dt><dd>{sso.issuer || t('未配置', 'Not configured')}</dd><dt>{t('客户端 ID', 'Client ID')}</dt><dd>{sso.clientId || t('未配置', 'Not configured')}</dd><dt>{t('回调地址', 'Callback URL')}</dt><dd>{sso.redirectUrl || t('未配置', 'Not configured')}</dd></dl>
      {sso.enabled && <p><a className="cloud-button mt-4" href="/api/auth/oidc/login">{t('前往组织 SSO 登录', 'Sign in with organization SSO')}</a></p>}
    </section>
  </div>
}

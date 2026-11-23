import { useLocale } from '@maomada/k8s-ui/hooks/useLocale'
import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Badge } from '@maomada/k8s-ui'
import { ArrowRightLeft, Building2, ShieldCheck, Users } from 'lucide-react'
import { cloudRequest, useCloudQuery, type HubCluster } from './cloudApi'
import { CloudDialog, CloudError, CloudLoading } from './CloudShared'
import type { HubOrganization, OrganizationRole } from './CloudOrganization'

type UserStatus = 'pending' | 'active' | 'disabled'
interface Membership { organizationId: string; role: OrganizationRole }
interface PlatformUser { id: string; email: string; displayName: string; status: UserStatus; local: boolean; platformOwner: boolean; memberships: Membership[] }
interface PlatformCluster extends HubCluster { organizationId: string }
interface PlatformEdit { user: PlatformUser; status: UserStatus; memberships: Membership[]; confirmedImpact: boolean }
interface OrganizationResult { id: string; name: string; role: OrganizationRole }
interface PlatformOrganizationGroup {
  id: string
  name: string
  members: { user: PlatformUser; membership: Membership }[]
  clusters: PlatformCluster[]
}

const isPlatformAccount = (user: PlatformUser) => user.local || user.platformOwner

function PlatformGroup({ title, count, children }: { title: string; count: string; children: ReactNode }) {
  const headingId = useId()
  return <div role="group" aria-labelledby={headingId} className="mt-4 min-w-0 rounded-lg border border-theme-border bg-theme-base px-4 py-3">
    <header className="flex flex-wrap items-center justify-between gap-2"><h3 id={headingId} className="min-w-0 break-words font-semibold">{title}</h3><Badge tone="structural">{count}</Badge></header>
    {children}
  </div>
}

export function CloudPlatform({ platformOwner }: { platformOwner: boolean }) {
  const { t } = useLocale()
  const roleLabels: Record<OrganizationRole, string> = { owner: t('组织 Owner', 'Organization owner'), member: t('成员', 'Member'), viewer: t('只读成员', 'Viewer') }
  const client = useQueryClient()
  const users = useCloudQuery<{ users: PlatformUser[] }>('/hub/platform/users', '/api', platformOwner)
  const organizations = useCloudQuery<{ organizations: HubOrganization[] }>('/hub/organizations', '/api', platformOwner)
  const clusters = useCloudQuery<{ clusters: PlatformCluster[] }>('/hub/platform/clusters', '/api', platformOwner)
  const [editing, setEditing] = useState<PlatformEdit>()
  const [addingOrganization, setAddingOrganization] = useState(false)
  const [transfer, setTransfer] = useState<{ cluster: PlatformCluster; organizationId: string }>()
  const [ownerMemberId, setOwnerMemberId] = useState('')
  const [newOrganizationName, setNewOrganizationName] = useState('')
  const [newMembershipOrganization, setNewMembershipOrganization] = useState('')
  const [newMembershipRole, setNewMembershipRole] = useState<OrganizationRole>('member')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState<unknown>()
  const usersTitleId = useId()
  const clustersTitleId = useId()
  const usersSection = useRef<HTMLElement>(null)
  const editedUserFocus = useRef<{ userId: string; opener: HTMLButtonElement } | undefined>(undefined)
  useEffect(() => {
    if (editing || !editedUserFocus.current) return
    const { userId, opener } = editedUserFocus.current
    editedUserFocus.current = undefined
    // A membership edit can unmount the opening row. Preserve its organization
    // when it survives, otherwise return to the account's new location.
    const original = opener.isConnected && !opener.disabled ? opener : undefined
    const replacement = [...(usersSection.current?.querySelectorAll<HTMLButtonElement>('[data-platform-user-id]') ?? [])]
      .find(button => button.dataset.platformUserId === userId && !button.disabled)
    const target = original ?? replacement ?? document.getElementById(usersTitleId)
    target?.focus({ preventScroll: true })
  }, [editing, usersTitleId])
  const transferredFocus = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (!transfer && transferredFocus.current) {
      document.getElementById(`${clustersTitleId}-${transferredFocus.current}`)?.focus()
      transferredFocus.current = undefined
    }
  }, [transfer, clustersTitleId])
  if (!platformOwner) return <div className="cloud-page"><CloudError error={new Error(t('平台管理仅对具有明确 platformOwner 权限的账号开放。', 'Platform administration requires the platform owner role.'))} /></div>

  const organizationRows = organizations.data?.organizations ?? []
  const organizationNames = new Map(organizationRows.map(organization => [organization.id, organization.name]))
  const userRows = users.data?.users ?? []
  const ownerCandidates = userRows.filter(user => user.status === 'active')
  const platformAccounts = userRows.filter(isPlatformAccount)
  const ordinaryAccounts = userRows.filter(user => !isPlatformAccount(user))
  const pendingAccounts = ordinaryAccounts.filter(user => !user.memberships.length && user.status === 'pending')
  const unassignedAccounts = ordinaryAccounts.filter(user => !user.memberships.length && user.status !== 'pending')
  const groupsById = new Map<string, PlatformOrganizationGroup>()
  function organizationGroup(id: string) {
    let group = groupsById.get(id)
    if (!group) {
      group = { id, name: organizationNames.get(id) ?? (id || t('未知组织', 'Unknown organization')), members: [], clusters: [] }
      groupsById.set(id, group)
    }
    return group
  }
  for (const organization of organizationRows) organizationGroup(organization.id)
  for (const user of ordinaryAccounts) {
    for (const membership of user.memberships) organizationGroup(membership.organizationId).members.push({ user, membership })
  }
  for (const cluster of clusters.data?.clusters ?? []) organizationGroup(cluster.organizationId).clusters.push(cluster)
  const organizationGroups = [...groupsById.values()]
  const groupTitle = (group: PlatformOrganizationGroup) => organizationGroups.some(other => other.id !== group.id && other.name === group.name) ? `${group.name} · ${group.id}` : group.name
  const organizationLabel = (id: string) => {
    const group = groupsById.get(id)
    return group ? groupTitle(group) : organizationNames.get(id) ?? id
  }

  async function invalidatePortalData() {
    await Promise.all(['/hub', '/hub/members', '/hub/clusters', '/hub/platform/users', '/hub/platform/clusters', '/hub/organizations', '/mcp/credentials']
      .map(path => client.invalidateQueries({ queryKey: ['portal', '/api', path] })))
  }

  function beginEdit(user: PlatformUser, opener: HTMLButtonElement) {
    editedUserFocus.current = { userId: user.id, opener }
    setError(undefined)
    setNewMembershipOrganization('')
    setNewMembershipRole('member')
    setEditing({ user, status: user.status, memberships: user.memberships.map(item => ({ ...item })), confirmedImpact: false })
  }

  async function saveUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!editing || busy) return
    if (editing.user.status === 'pending' && editing.status === 'active' && !editing.memberships.length) {
      setError(new Error(t('审批账号前请选择组织和角色。', 'Select an organization and role before approving the account.')))
      return
    }
    if (!editing.confirmedImpact) {
      setError(new Error(t('保存前请确认对 Token 和活动流的撤销影响。', 'Acknowledge that tokens and active streams may be revoked before saving.')))
      return
    }
    setBusy(editing.user.id); setError(undefined)
    try {
      const updated = await cloudRequest<PlatformUser>(`/hub/platform/users/${encodeURIComponent(editing.user.id)}`, {
        method: 'PUT', body: { status: editing.status, memberships: editing.memberships.map(({ organizationId, role }) => ({ organizationId, role })) },
      })
      client.setQueryData<{ users: PlatformUser[] }>(['portal', '/api', '/hub/platform/users'], previous => previous ? { users: previous.users.map(user => user.id === updated.id ? updated : user) } : previous)
      await invalidatePortalData()
      setEditing(undefined)
    } catch (failure) { setError(failure) } finally { setBusy('') }
  }

  function addMembership() {
    if (!editing || !newMembershipOrganization || editing.memberships.some(item => item.organizationId === newMembershipOrganization)) return
    setEditing({ ...editing, confirmedImpact: false, memberships: [...editing.memberships, { organizationId: newMembershipOrganization, role: newMembershipRole }] })
    setNewMembershipOrganization('')
  }

  async function createOrganization(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || !newOrganizationName.trim() || !ownerCandidates.some(user => user.id === ownerMemberId)) return
    setBusy('organization'); setError(undefined)
    try {
      const created = await cloudRequest<OrganizationResult>('/hub/organizations', { method: 'POST', body: { name: newOrganizationName.trim(), ownerMemberId } })
      if (!created.id || !created.name) throw new Error(t('组织响应不完整。', 'The response did not include the complete organization details.'))
      client.setQueryData<{ organizations: HubOrganization[] }>(['portal', '/api', '/hub/organizations'], previous => previous ? { organizations: [...previous.organizations, created] } : previous)
      await invalidatePortalData()
      setNewOrganizationName(''); setOwnerMemberId(''); setAddingOrganization(false)
    } catch (failure) { setError(failure) } finally { setBusy('') }
  }

  async function transferCluster() {
    if (!transfer || busy || !transfer.organizationId || transfer.organizationId === transfer.cluster.organizationId) return
    setBusy(transfer.cluster.id); setError(undefined)
    try {
      const { cluster: result } = await cloudRequest<{ cluster: PlatformCluster }>(`/hub/clusters/${encodeURIComponent(transfer.cluster.id)}/transfer`, { method: 'POST', body: { organizationId: transfer.organizationId } })
      client.setQueryData<{ clusters: PlatformCluster[] }>(['portal', '/api', '/hub/platform/clusters'], previous => previous ? { clusters: previous.clusters.map(cluster => cluster.id === result.id ? result : cluster) } : previous)
      await invalidatePortalData()
      transferredFocus.current = result.id
      setTransfer(undefined)
    } catch (failure) { setError(failure) } finally { setBusy('') }
  }

  const loadError = users.error ?? organizations.error ?? clusters.error
  const loading = users.isPending || organizations.isPending || clusters.isPending

  function userRow(user: PlatformUser, membership?: Membership) {
    const label = user.displayName || user.email || user.id
    const actionLabel = user.email && user.email !== label ? `${label} · ${user.email}` : label
    const system = isPlatformAccount(user)
    const action = system ? t('系统账号不可编辑', 'System account') : user.status === 'pending' ? t('审核申请', 'Review application') : t('编辑成员资格', 'Edit memberships')
    return <li key={user.id} className="cloud-platform-user">
      <div className="min-w-0"><strong className="break-words">{label}</strong>{user.email && user.email !== label && <div className="cloud-member-id">{user.email}</div>}{user.local && <div className="mt-1"><Badge severity="neutral">{t('恢复账号', 'Recovery account')}</Badge></div>}</div>
      <Badge severity={user.status === 'active' ? 'success' : user.status === 'pending' ? 'warning' : 'neutral'}>{user.status === 'active' ? t('有效', 'Active') : user.status === 'pending' ? t('待审批', 'Pending approval') : t('已停用', 'Disabled')}</Badge>
      <div>{system ? <Badge tone="structural">{user.platformOwner ? t('平台 Owner', 'Platform owner') : t('恢复账号', 'Recovery account')}</Badge> : membership && <Badge tone="structural">{roleLabels[membership.role]}</Badge>}</div>
      <button className="cloud-button" data-platform-user-id={user.id} aria-label={`${action}：${actionLabel}`} disabled={!!busy || system} onClick={event => beginEdit(user, event.currentTarget)}>{action}</button>
    </li>
  }

  function accountGroup(title: string, accounts: PlatformUser[]) {
    return accounts.length > 0 && <PlatformGroup title={title} count={t(`${accounts.length} 个账号`, `${accounts.length} ${accounts.length === 1 ? 'account' : 'accounts'}`)}><ul role="list" className="m-0 list-none p-0">{accounts.map(user => userRow(user))}</ul></PlatformGroup>
  }

  return <div className="cloud-page">
    <div className="cloud-page-heading"><div><h1>{t('平台管理', 'Platform administration')}</h1><p>{t('管理平台账号、组织成员资格与集群归属。', 'Manage platform accounts, organization memberships, and cluster ownership.')}</p></div><Badge tone="structural">{t('平台 Owner', 'Platform owner')}</Badge></div>
    {loadError && <CloudError error={loadError} retry={() => { void Promise.all([users.refetch(), organizations.refetch(), clusters.refetch()]) }} />}
    {loading && <CloudLoading label={t('正在加载平台目录…', 'Loading platform directory…')} />}

    <section className="cloud-card"><header><h2><Building2 size={17} /> {t('组织', 'Organization')}</h2><button className="cloud-button" disabled={loading} onClick={() => { setError(undefined); setAddingOrganization(true) }}>{t('创建组织', 'Create organization')}</button></header>
      {organizationRows.length ? <div className="cloud-table-scroll"><table className="cloud-table"><thead><tr><th>{t('组织', 'Organization')}</th><th>ID</th></tr></thead><tbody>{organizationRows.map(organization => <tr key={organization.id}><td>{organization.name}</td><td className="portal-mono">{organization.id}</td></tr>)}</tbody></table></div> : !loading && !organizations.error && <p>{t('暂无组织。', 'No organizations.')}</p>}
    </section>

    <section ref={usersSection} className="cloud-card" role="group" aria-labelledby={usersTitleId}><header><h2 id={usersTitleId} tabIndex={-1}><Users size={17} aria-hidden="true" /> {t('平台用户', 'Platform users')}</h2></header>
      {users.data && <>
        {accountGroup(t('待审批', 'Pending approval'), pendingAccounts)}
        {organizationGroups.map(group => <PlatformGroup key={group.id} title={groupTitle(group)} count={t(`${group.members.length} 位成员`, `${group.members.length} ${group.members.length === 1 ? 'member' : 'members'}`)}>
          {group.members.length ? <ul role="list" className="m-0 list-none p-0">{group.members.map(({ user, membership }) => userRow(user, membership))}</ul> : <p className="mt-3 text-sm text-theme-text-secondary">{t('暂无成员。', 'No members.')}</p>}
        </PlatformGroup>)}
        {accountGroup(t('未分配组织', 'No organization assigned'), unassignedAccounts)}
        {accountGroup(t('平台管理员', 'Platform administrators'), platformAccounts)}
      </>}
      {!loading && !users.error && !users.data?.users.length && <p>{t('暂无平台用户。', 'No platform users.')}</p>}
    </section>

    <section className="cloud-card" role="group" aria-labelledby={clustersTitleId}><header><h2 id={clustersTitleId}><ArrowRightLeft size={17} aria-hidden="true" /> {t('集群归属', 'Cluster ownership')}</h2></header>
      {clusters.data && organizationGroups.map(group => <PlatformGroup key={group.id} title={groupTitle(group)} count={t(`${group.clusters.length} 个集群`, `${group.clusters.length} ${group.clusters.length === 1 ? 'cluster' : 'clusters'}`)}>
        {group.clusters.length ? <ul role="list" className="m-0 list-none p-0">{group.clusters.map(cluster => <li key={cluster.id} className="cloud-platform-cluster">
          <div className="min-w-0"><strong className="break-words">{cluster.name}</strong><div className="cloud-member-id portal-mono">{cluster.id}</div></div>
          <Badge severity={cluster.state === 'connected' ? 'success' : cluster.state === 'revoked' ? 'neutral' : 'warning'}>{cluster.state === 'connected' ? t('已连接', 'Connected') : cluster.state === 'revoked' ? t('已撤销', 'Revoked') : t('离线', 'Offline')}</Badge>
          <button id={`${clustersTitleId}-${cluster.id}`} className="cloud-button" aria-label={t(`转移集群：${cluster.name} · ${cluster.id}`, `Transfer cluster: ${cluster.name} · ${cluster.id}`)} disabled={!!busy || !organizationRows.some(item => item.id !== cluster.organizationId)} onClick={() => { setError(undefined); setTransfer({ cluster, organizationId: organizationRows.find(item => item.id !== cluster.organizationId)?.id ?? '' }) }}>{t('转移', 'Transfer')}</button>
        </li>)}</ul> : <p className="mt-3 text-sm text-theme-text-secondary">{t('暂无集群。', 'No clusters.')}</p>}
      </PlatformGroup>)}
      {!loading && !clusters.error && !clusters.data?.clusters.length && <p>{t('暂无集群。', 'No clusters.')}</p>}
    </section>

    {addingOrganization && <CloudDialog title={t('创建组织', 'Create organization')} busy={busy === 'organization'} onClose={() => { if (busy !== 'organization') setAddingOrganization(false) }}><form className="cloud-form" onSubmit={createOrganization}>
      <label>{t('组织名称', 'Organization name')}<input required maxLength={128} value={newOrganizationName} disabled={busy === 'organization'} onChange={event => setNewOrganizationName(event.target.value)} /></label>
      <label>{t('初始组织 Owner', 'Initial organization owner')}<select required className="portal-control" value={ownerMemberId} disabled={busy === 'organization'} onChange={event => setOwnerMemberId(event.target.value)}><option value="">{t('选择有效账号', 'Select an active account')}</option>{ownerCandidates.map(user => <option key={user.id} value={user.id}>{user.displayName || user.email}</option>)}</select></label>
      {!ownerCandidates.length && <p role="status">{t('没有可用的有效账号可设为组织 Owner。', 'No active accounts are available to own the organization.')}</p>}
      {error !== undefined && <CloudError error={error} />}
      <footer><button type="button" className="cloud-button" disabled={busy === 'organization'} onClick={() => setAddingOrganization(false)}>{t('取消', 'Cancel')}</button><button className="cloud-button cloud-primary" disabled={busy === 'organization' || !ownerMemberId || !newOrganizationName.trim()}>{busy === 'organization' ? t('正在创建…', 'Creating…') : t('创建组织', 'Create organization')}</button></footer>
    </form></CloudDialog>}

    {editing && <CloudDialog title={editing.user.status === 'pending' ? t('审批平台账号', 'Approve platform account') : t('编辑平台账号', 'Edit platform account')} busy={busy === editing.user.id} onClose={() => { if (busy !== editing.user.id) setEditing(undefined) }}><form className="cloud-form" onSubmit={saveUser}>
      <p>{editing.user.displayName || editing.user.email} · {editing.user.email}</p>
      <label>{t('账号状态', 'Account status')}<select className="portal-control" value={editing.status} disabled={busy === editing.user.id} onChange={event => setEditing({ ...editing, status: event.target.value as UserStatus, confirmedImpact: false })}><option value="pending">{t('待审批', 'Pending approval')}</option><option value="active">{t('有效', 'Active')}</option><option value="disabled">{t('已停用', 'Disabled')}</option></select></label>
      <fieldset className="cloud-form"><legend>{t('组织成员资格', 'Organization memberships')}</legend>{editing.memberships.map((membership, index) => <div className="cloud-platform-membership-edit" key={`${membership.organizationId}-${index}`}><strong>{organizationLabel(membership.organizationId)}</strong><select className="portal-control" aria-label={t(`组织角色：${organizationLabel(membership.organizationId)}`, `Organization role: ${organizationLabel(membership.organizationId)}`)} value={membership.role} disabled={busy === editing.user.id} onChange={event => setEditing({ ...editing, confirmedImpact: false, memberships: editing.memberships.map((item, row) => row === index ? { ...item, role: event.target.value as OrganizationRole } : item) })}><option value="owner">{t('组织 Owner', 'Organization owner')}</option><option value="member">{t('成员', 'Member')}</option><option value="viewer">{t('只读成员', 'Viewer')}</option></select><button type="button" className="cloud-text-button" disabled={busy === editing.user.id} onClick={() => setEditing({ ...editing, confirmedImpact: false, memberships: editing.memberships.filter((_, row) => row !== index) })}>{t('移除成员资格', 'Remove membership')}</button></div>)}
        <div className="cloud-platform-membership-edit"><select className="portal-control" aria-label={t('要添加的组织', 'Organization to add')} value={newMembershipOrganization} disabled={busy === editing.user.id} onChange={event => setNewMembershipOrganization(event.target.value)}><option value="">{t('添加组织', 'Add organization')}</option>{organizationRows.filter(item => !editing.memberships.some(membership => membership.organizationId === item.id)).map(item => <option key={item.id} value={item.id}>{organizationLabel(item.id)}</option>)}</select><select className="portal-control" aria-label={t('新成员角色', 'New member role')} value={newMembershipRole} disabled={busy === editing.user.id} onChange={event => setNewMembershipRole(event.target.value as OrganizationRole)}><option value="owner">{t('组织 Owner', 'Organization owner')}</option><option value="member">{t('成员', 'Member')}</option><option value="viewer">{t('只读成员', 'Viewer')}</option></select><button type="button" className="cloud-button" disabled={busy === editing.user.id || !newMembershipOrganization} onClick={addMembership}>{t('添加成员资格', 'Add membership')}</button></div>
      </fieldset>
      {editing.user.status === 'pending' && editing.status === 'active' && !editing.memberships.length && <p role="alert">{t('审批前请选择组织和角色。', 'Select an organization and role before approving.')}</p>}
      {error !== undefined && <CloudError error={error} />}
      <p>{t('将账号状态设为有效即代表批准。现有成员资格会保留，除非明确移除。', 'Set the status to Active to approve this account. Existing memberships are kept unless you remove them.')}</p>
      <label className="cloud-confirm-access"><input type="checkbox" checked={editing.confirmedImpact} disabled={busy === editing.user.id} onChange={event => setEditing({ ...editing, confirmedImpact: event.target.checked })} />{t('我了解状态或成员资格变更可能撤销 Token 和活动流；最后一位组织 Owner 由服务器保护。', 'I understand that changing status or memberships may revoke tokens and active streams. The server protects the last organization owner.')}</label>
      <footer><button type="button" className="cloud-button" disabled={busy === editing.user.id} onClick={() => setEditing(undefined)}>{t('取消', 'Cancel')}</button><button className="cloud-button cloud-primary" disabled={busy === editing.user.id || !editing.confirmedImpact || editing.user.status === 'pending' && editing.status === 'active' && !editing.memberships.length}>{busy === editing.user.id ? t('正在保存…', 'Saving…') : t('保存账号', 'Save account')}</button></footer>
    </form></CloudDialog>}

    {transfer && <CloudDialog title={t('转移集群', 'Transfer cluster')} busy={busy === transfer.cluster.id} onClose={() => { if (busy !== transfer.cluster.id) setTransfer(undefined) }}><div className="cloud-form"><p><strong>{transfer.cluster.name}</strong> {t('将从', 'will be transferred from')} {organizationLabel(transfer.cluster.organizationId)}{t(' 转移。', '.')}</p><label>{t('目标组织', 'Destination organization')}<select className="portal-control" aria-label={t('转移目标组织', 'Destination organization for transfer')} value={transfer.organizationId} disabled={busy === transfer.cluster.id} onChange={event => setTransfer({ ...transfer, organizationId: event.target.value })}><option value="">{t('选择目标组织', 'Select destination organization')}</option>{organizationRows.filter(item => item.id !== transfer.cluster.organizationId).map(item => <option key={item.id} value={item.id}>{organizationLabel(item.id)}</option>)}</select></label><p>{transfer.cluster.state === 'revoked' ? t('此集群已撤销，本次仅变更归属。', 'This cluster has already been revoked. The transfer changes its ownership.') : t('Agent 会自动重连，无需重新配对。', 'The Agent reconnects automatically.')}{t('原组织的访问权限和相关 MCP 凭据将失效。', ' Access and associated MCP credentials for the original organization will be revoked.')}</p>{error !== undefined && <CloudError error={error} />}<footer><button type="button" className="cloud-button" disabled={busy === transfer.cluster.id} onClick={() => setTransfer(undefined)}>{t('取消', 'Cancel')}</button><button type="button" className="cloud-button cloud-primary" disabled={busy === transfer.cluster.id || !transfer.organizationId || transfer.organizationId === transfer.cluster.organizationId} onClick={() => { void transferCluster() }}>{busy === transfer.cluster.id ? t('正在转移…', 'Transferring…') : t('确认转移', 'Confirm transfer')}</button></footer></div></CloudDialog>}
    <section className="cloud-card"><h2><ShieldCheck size={17} /> {t('平台权限边界', 'Platform permissions')}</h2><p>{t('组织 Owner 不等于平台 Owner；本页不会提供授予平台 Owner 权限的控制项。组织 Owner 对应 Kubernetes 管理员（不是 cluster-admin）；成员可编辑，查看者只读，最终资源权限由服务器执行。', 'Organization owners have the Kubernetes admin role. Members can edit, and viewers have read-only access. The server enforces resource permissions. Platform ownership is managed separately.')}</p></section>
  </div>
}

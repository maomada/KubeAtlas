import { useId, useState, type FormEvent, type ReactNode } from 'react'
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

const roleLabels: Record<OrganizationRole, string> = { owner: '组织 Owner', member: '成员', viewer: '只读成员' }
const isPlatformAccount = (user: PlatformUser) => user.local || user.platformOwner

function PlatformGroup({ title, count, children }: { title: string; count: string; children: ReactNode }) {
  const headingId = useId()
  return <div role="group" aria-labelledby={headingId} className="mt-4 min-w-0 rounded-lg border border-theme-border bg-theme-base px-4 py-3">
    <header className="flex flex-wrap items-center justify-between gap-2"><h3 id={headingId} className="min-w-0 break-words font-semibold">{title}</h3><Badge tone="structural">{count}</Badge></header>
    {children}
  </div>
}

export function CloudPlatform({ platformOwner }: { platformOwner: boolean }) {
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
  if (!platformOwner) return <div className="cloud-page"><CloudError error={new Error('平台管理仅对具有明确 platformOwner 权限的账号开放。')} /></div>

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
      group = { id, name: organizationNames.get(id) ?? (id || '未知组织'), members: [], clusters: [] }
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

  function beginEdit(user: PlatformUser) {
    setError(undefined)
    setNewMembershipOrganization('')
    setNewMembershipRole('member')
    setEditing({ user, status: user.status, memberships: user.memberships.map(item => ({ ...item })), confirmedImpact: false })
  }

  async function saveUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!editing || busy) return
    if (editing.user.status === 'pending' && editing.status === 'active' && !editing.memberships.length) {
      setError(new Error('审批账号前请选择组织和角色。'))
      return
    }
    if (!editing.confirmedImpact) {
      setError(new Error('保存前请确认对 Token 和活动流的撤销影响。'))
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
      if (!created.id || !created.name) throw new Error('组织响应不完整。')
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
      setTransfer(undefined)
    } catch (failure) { setError(failure) } finally { setBusy('') }
  }

  const loadError = users.error ?? organizations.error ?? clusters.error
  const loading = users.isPending || organizations.isPending || clusters.isPending

  function userRow(user: PlatformUser, membership?: Membership) {
    const label = user.displayName || user.email || user.id
    const actionLabel = user.email && user.email !== label ? `${label} · ${user.email}` : label
    const system = isPlatformAccount(user)
    const action = system ? '系统账号不可编辑' : user.status === 'pending' ? '审核申请' : '编辑成员资格'
    return <li key={user.id} className="cloud-platform-user">
      <div className="min-w-0"><strong className="break-words">{label}</strong>{user.email && user.email !== label && <div className="cloud-member-id">{user.email}</div>}{user.local && <div className="mt-1"><Badge severity="neutral">恢复账号</Badge></div>}</div>
      <Badge severity={user.status === 'active' ? 'success' : user.status === 'pending' ? 'warning' : 'neutral'}>{user.status === 'active' ? '有效' : user.status === 'pending' ? '待审批' : '已停用'}</Badge>
      <div>{system ? <Badge tone="structural">{user.platformOwner ? '平台 Owner' : '恢复账号'}</Badge> : membership && <Badge tone="structural">{roleLabels[membership.role]}</Badge>}</div>
      <button className="cloud-button" aria-label={`${action}：${actionLabel}`} disabled={!!busy || system} onClick={() => beginEdit(user)}>{action}</button>
    </li>
  }

  function accountGroup(title: string, accounts: PlatformUser[]) {
    return accounts.length > 0 && <PlatformGroup title={title} count={`${accounts.length} 个账号`}><ul role="list" className="m-0 list-none p-0">{accounts.map(user => userRow(user))}</ul></PlatformGroup>
  }

  return <div className="cloud-page">
    <div className="cloud-page-heading"><div><h1>平台管理</h1><p>管理平台账号、组织成员资格与集群归属。</p></div><Badge tone="structural">平台 Owner</Badge></div>
    {loadError && <CloudError error={loadError} retry={() => { void Promise.all([users.refetch(), organizations.refetch(), clusters.refetch()]) }} />}
    {loading && <CloudLoading label="正在加载平台目录…" />}

    <section className="cloud-card"><header><h2><Building2 size={17} /> 组织</h2><button className="cloud-button" disabled={loading} onClick={() => { setError(undefined); setAddingOrganization(true) }}>创建组织</button></header>
      {organizationRows.length ? <div className="cloud-table-scroll"><table className="cloud-table"><thead><tr><th>组织</th><th>ID</th></tr></thead><tbody>{organizationRows.map(organization => <tr key={organization.id}><td>{organization.name}</td><td className="portal-mono">{organization.id}</td></tr>)}</tbody></table></div> : !loading && !organizations.error && <p>暂无组织。</p>}
    </section>

    <section className="cloud-card" role="group" aria-labelledby={usersTitleId}><header><h2 id={usersTitleId}><Users size={17} aria-hidden="true" /> 平台用户</h2></header>
      {users.data && <>
        {accountGroup('待审批', pendingAccounts)}
        {organizationGroups.map(group => <PlatformGroup key={group.id} title={groupTitle(group)} count={`${group.members.length} 位成员`}>
          {group.members.length ? <ul role="list" className="m-0 list-none p-0">{group.members.map(({ user, membership }) => userRow(user, membership))}</ul> : <p className="mt-3 text-sm text-theme-text-secondary">暂无成员。</p>}
        </PlatformGroup>)}
        {accountGroup('未分配组织', unassignedAccounts)}
        {accountGroup('平台管理员', platformAccounts)}
      </>}
      {!loading && !users.error && !users.data?.users.length && <p>暂无平台用户。</p>}
    </section>

    <section className="cloud-card" role="group" aria-labelledby={clustersTitleId}><header><h2 id={clustersTitleId}><ArrowRightLeft size={17} aria-hidden="true" /> 集群归属</h2></header>
      {clusters.data && organizationGroups.map(group => <PlatformGroup key={group.id} title={groupTitle(group)} count={`${group.clusters.length} 个集群`}>
        {group.clusters.length ? <ul role="list" className="m-0 list-none p-0">{group.clusters.map(cluster => <li key={cluster.id} className="cloud-platform-cluster">
          <div className="min-w-0"><strong className="break-words">{cluster.name}</strong><div className="cloud-member-id portal-mono">{cluster.id}</div></div>
          <Badge severity={cluster.state === 'connected' ? 'success' : cluster.state === 'revoked' ? 'neutral' : 'warning'}>{cluster.state === 'connected' ? '已连接' : cluster.state === 'revoked' ? '已撤销' : '离线'}</Badge>
          <button className="cloud-button" aria-label={`转移集群：${cluster.name} · ${cluster.id}`} disabled={!!busy || !organizationRows.some(item => item.id !== cluster.organizationId)} onClick={() => { setError(undefined); setTransfer({ cluster, organizationId: organizationRows.find(item => item.id !== cluster.organizationId)?.id ?? '' }) }}>转移</button>
        </li>)}</ul> : <p className="mt-3 text-sm text-theme-text-secondary">暂无集群。</p>}
      </PlatformGroup>)}
      {!loading && !clusters.error && !clusters.data?.clusters.length && <p>暂无集群。</p>}
    </section>

    {addingOrganization && <CloudDialog title="创建组织" busy={busy === 'organization'} onClose={() => { if (busy !== 'organization') setAddingOrganization(false) }}><form className="cloud-form" onSubmit={createOrganization}>
      <label>组织名称<input required maxLength={128} value={newOrganizationName} disabled={busy === 'organization'} onChange={event => setNewOrganizationName(event.target.value)} /></label>
      <label>初始组织 Owner<select required className="portal-control" value={ownerMemberId} disabled={busy === 'organization'} onChange={event => setOwnerMemberId(event.target.value)}><option value="">选择有效账号</option>{ownerCandidates.map(user => <option key={user.id} value={user.id}>{user.displayName || user.email}</option>)}</select></label>
      {!ownerCandidates.length && <p role="status">没有可用的有效账号可设为组织 Owner。</p>}
      {error !== undefined && <CloudError error={error} />}
      <footer><button type="button" className="cloud-button" disabled={busy === 'organization'} onClick={() => setAddingOrganization(false)}>取消</button><button className="cloud-button cloud-primary" disabled={busy === 'organization' || !ownerMemberId || !newOrganizationName.trim()}>{busy === 'organization' ? '正在创建…' : '创建组织'}</button></footer>
    </form></CloudDialog>}

    {editing && <CloudDialog title={editing.user.status === 'pending' ? '审批平台账号' : '编辑平台账号'} busy={busy === editing.user.id} onClose={() => { if (busy !== editing.user.id) setEditing(undefined) }}><form className="cloud-form" onSubmit={saveUser}>
      <p>{editing.user.displayName || editing.user.email} · {editing.user.email}</p>
      <label>账号状态<select className="portal-control" value={editing.status} disabled={busy === editing.user.id} onChange={event => setEditing({ ...editing, status: event.target.value as UserStatus, confirmedImpact: false })}><option value="pending">待审批</option><option value="active">有效</option><option value="disabled">已停用</option></select></label>
      <fieldset className="cloud-form"><legend>组织成员资格</legend>{editing.memberships.map((membership, index) => <div className="cloud-platform-membership-edit" key={`${membership.organizationId}-${index}`}><strong>{organizationLabel(membership.organizationId)}</strong><select className="portal-control" aria-label={`组织角色：${organizationLabel(membership.organizationId)}`} value={membership.role} disabled={busy === editing.user.id} onChange={event => setEditing({ ...editing, confirmedImpact: false, memberships: editing.memberships.map((item, row) => row === index ? { ...item, role: event.target.value as OrganizationRole } : item) })}><option value="owner">组织 Owner</option><option value="member">成员</option><option value="viewer">只读成员</option></select><button type="button" className="cloud-text-button" disabled={busy === editing.user.id} onClick={() => setEditing({ ...editing, confirmedImpact: false, memberships: editing.memberships.filter((_, row) => row !== index) })}>移除成员资格</button></div>)}
        <div className="cloud-platform-membership-edit"><select className="portal-control" aria-label="要添加的组织" value={newMembershipOrganization} disabled={busy === editing.user.id} onChange={event => setNewMembershipOrganization(event.target.value)}><option value="">添加组织</option>{organizationRows.filter(item => !editing.memberships.some(membership => membership.organizationId === item.id)).map(item => <option key={item.id} value={item.id}>{organizationLabel(item.id)}</option>)}</select><select className="portal-control" aria-label="新成员角色" value={newMembershipRole} disabled={busy === editing.user.id} onChange={event => setNewMembershipRole(event.target.value as OrganizationRole)}><option value="owner">组织 Owner</option><option value="member">成员</option><option value="viewer">只读成员</option></select><button type="button" className="cloud-button" disabled={busy === editing.user.id || !newMembershipOrganization} onClick={addMembership}>添加成员资格</button></div>
      </fieldset>
      {editing.user.status === 'pending' && editing.status === 'active' && !editing.memberships.length && <p role="alert">审批前请选择组织和角色。</p>}
      {error !== undefined && <CloudError error={error} />}
      <p>将账号状态设为有效即代表批准。现有成员资格会保留，除非明确移除。</p>
      <label className="cloud-confirm-access"><input type="checkbox" checked={editing.confirmedImpact} disabled={busy === editing.user.id} onChange={event => setEditing({ ...editing, confirmedImpact: event.target.checked })} />我了解状态或成员资格变更可能撤销 Token 和活动流；最后一位组织 Owner 由服务器保护。</label>
      <footer><button type="button" className="cloud-button" disabled={busy === editing.user.id} onClick={() => setEditing(undefined)}>取消</button><button className="cloud-button cloud-primary" disabled={busy === editing.user.id || !editing.confirmedImpact || editing.user.status === 'pending' && editing.status === 'active' && !editing.memberships.length}>{busy === editing.user.id ? '正在保存…' : '保存账号'}</button></footer>
    </form></CloudDialog>}

    {transfer && <CloudDialog title="转移集群" busy={busy === transfer.cluster.id} onClose={() => { if (busy !== transfer.cluster.id) setTransfer(undefined) }}><div className="cloud-form"><p><strong>{transfer.cluster.name}</strong> 将从 {organizationLabel(transfer.cluster.organizationId)} 转移。</p><label>目标组织<select className="portal-control" aria-label="转移目标组织" value={transfer.organizationId} disabled={busy === transfer.cluster.id} onChange={event => setTransfer({ ...transfer, organizationId: event.target.value })}><option value="">选择目标组织</option>{organizationRows.filter(item => item.id !== transfer.cluster.organizationId).map(item => <option key={item.id} value={item.id}>{organizationLabel(item.id)}</option>)}</select></label><p>{transfer.cluster.state === 'revoked' ? '此集群已撤销，本次仅变更归属。' : 'Agent 会自动重连，无需重新配对。'}原组织的访问权限和相关 MCP 凭据将失效。</p>{error !== undefined && <CloudError error={error} />}<footer><button type="button" className="cloud-button" disabled={busy === transfer.cluster.id} onClick={() => setTransfer(undefined)}>取消</button><button type="button" className="cloud-button cloud-primary" disabled={busy === transfer.cluster.id || !transfer.organizationId || transfer.organizationId === transfer.cluster.organizationId} onClick={() => { void transferCluster() }}>{busy === transfer.cluster.id ? '正在转移…' : '确认转移'}</button></footer></div></CloudDialog>}
    <section className="cloud-card"><h2><ShieldCheck size={17} /> 平台权限边界</h2><p>组织 Owner 不等于平台 Owner；本页不会提供授予平台 Owner 权限的控制项。组织 Owner 对应 Kubernetes 管理员（不是 cluster-admin）；成员可编辑，查看者只读，最终资源权限由服务器执行。</p></section>
  </div>
}

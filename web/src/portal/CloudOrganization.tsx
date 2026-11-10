import { useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Badge } from '@maomada/k8s-ui'
import { Building2, KeyRound, Pencil, Search, ShieldCheck, Users } from 'lucide-react'
import { cloudRequest, useCloudQuery, type HubCluster } from './cloudApi'
import { CloudDialog, CloudError, CloudLink, CloudLoading, CopyButton } from './CloudShared'

export interface HubMember {
  id: string
  userId: string
  displayName: string
  role: 'owner' | 'admin' | 'member' | 'viewer'
  status: 'active' | 'pending' | 'disabled'
  clusterIds: string[]
  kubernetesGroups: string[]
  createdAt: string
  updatedAt: string
}

interface Organization { id: string; name: string; memberCount: number }
interface SSOConfiguration {
  enabled: boolean
  issuer?: string
  clientId?: string
  redirectUrl?: string
  provisioning: 'approval'
  directorySync: boolean
  backchannelLogout: boolean
}

export function canManageOrganization(member?: HubMember) {
  return member?.status === 'active' && (member.role === 'owner' || member.role === 'admin')
}

export async function logoutPortal() {
  const result = await cloudRequest<{ logoutUrl: string } | undefined>('/auth/logout', { method: 'POST', body: {} })
  window.location.replace(result?.logoutUrl ?? '/login')
}

export function PendingMembership({ member, retry }: { member: HubMember; retry: () => void }) {
  const [error, setError] = useState<unknown>()
  return <div className="cloud-page cloud-empty"><ShieldCheck size={36} /><h1>Waiting for approval</h1>
    <p>Your identity is verified. An organization administrator must approve your membership and select your cluster access before you can continue.</p>
    <strong className="cloud-account-identity">{member.displayName}</strong>
    <p>No cluster access or MCP credentials have been granted.</p>
    {error !== undefined && <CloudError error={error} />}
    <div className="cloud-row-actions"><button className="cloud-button" onClick={retry}>Check approval</button><button className="cloud-button" onClick={() => { void logoutPortal().catch(setError) }}>Sign out</button></div>
  </div>
}

export function CloudOrganization({ member }: { member?: HubMember }) {
  const organization = useCloudQuery<Organization>('/hub/organization')
  if (organization.isPending) return <CloudLoading label="Loading organization…" />
  if (organization.error) return <CloudError error={organization.error} retry={() => { void organization.refetch() }} />
  if (!organization.data) return null
  return <div className="cloud-page cloud-narrow">
    <div className="cloud-page-heading"><div><h1>Organization</h1><p>Manage your self-hosted workspace.</p></div><Badge tone="structural">Self-hosted</Badge></div>
    <section className="cloud-card"><header><h2><Building2 size={17} /> Organization details</h2></header>
      <OrganizationName key={organization.data.name} organization={organization.data} editable={canManageOrganization(member)} />
    </section>
    <section className="cloud-card"><header><h2><Users size={17} /> Members and access</h2></header><p>{organization.data.memberCount} active members · Your role: {member?.role ?? 'Unknown'}</p>
      <p>Organization roles control Hub management and cluster scope. Kubernetes RBAC still controls access to each cluster's resources.</p>
      {canManageOrganization(member) && <CloudLink href="/settings/organization/members" className="cloud-button">Manage members</CloudLink>}
    </section>
    <section className="cloud-card"><h2>Deployment scope</h2><p>This Hub serves one organization. Separate organizations require separate Hub deployments; multi-organization tenancy is not implemented.</p></section>
  </div>
}

function OrganizationName({ organization, editable }: { organization: Organization; editable: boolean }) {
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
    } catch (failure) { setError(failure) } finally { setBusy(false) }
  }
  return <form className="cloud-form" onSubmit={save}>
    <label>Organization name<input value={name} maxLength={128} required disabled={!editable || busy} onChange={event => setName(event.target.value)} /></label>
    <small>Organization ID: {organization.id}</small>
    {error !== undefined && <CloudError error={error} />}
    {editable && <footer><button className="cloud-button cloud-primary" disabled={busy || !name.trim() || name.trim() === organization.name}>{busy ? 'Saving…' : 'Save changes'}</button></footer>}
  </form>
}

export function CloudMembers({ member }: { member?: HubMember }) {
  const manage = canManageOrganization(member)
  const members = useCloudQuery<{ members: HubMember[] }>('/hub/members', '/api', manage)
  const directory = useCloudQuery<{ clusters: HubCluster[] }>('/hub/clusters', '/api', manage)
  const [filter, setFilter] = useState('')
  const [editing, setEditing] = useState<HubMember>()
  if (!manage) return <div className="cloud-page"><CloudError error={new Error('Only organization owners and administrators can manage members.')} /></div>
  const rows = members.data?.members.filter(item => `${item.displayName} ${item.userId} ${item.role} ${item.status}`.toLowerCase().includes(filter.toLowerCase())) ?? []
  return <div className="cloud-page">
    <div className="cloud-page-heading"><div><h1>Members</h1><p>Approve identities and manage their organization access.</p></div><Badge tone="structural">{members.data ? members.data.members.length : '…'} members</Badge></div>
    <section className="cloud-card cloud-member-onboarding"><KeyRound size={18} /><div><h2>Add a member</h2><p>Create their account in your self-hosted identity provider, then ask them to sign in using SSO. Their first login appears here as pending; approve it and select access below. Email invitations and directory synchronization are not available.</p></div><CloudLink className="cloud-button" href="/settings/organization/sso">SSO settings</CloudLink></section>
    {members.isPending ? <CloudLoading label="Loading members…" /> : members.error ? <CloudError error={members.error} retry={() => { void members.refetch() }} /> : <section className="cloud-table-panel">
      <div className="cloud-table-filter"><label className="cloud-search"><Search size={16} /><input type="search" placeholder="Search members" aria-label="Search members" value={filter} onChange={event => setFilter(event.target.value)} /></label><span>{rows.length} results</span></div>
      <div className="cloud-table-scroll"><table className="cloud-table cloud-members-table"><thead><tr><th>Member</th><th>Role</th><th>Status</th><th>Cluster scope</th><th>Access</th></tr></thead><tbody>
        {rows.map(item => <tr key={item.id}><td><strong>{item.displayName}</strong>{item.id === member?.id && <small> · You</small>}<div className="cloud-member-id portal-mono">{item.userId}</div></td><td>{item.role}</td><td><Badge severity={item.status === 'active' ? 'success' : item.status === 'pending' ? 'warning' : 'neutral'}>{item.status}</Badge></td><td>{item.role === 'owner' || item.role === 'admin' ? 'All clusters' : `${item.clusterIds.length} selected`}</td><td>{item.role === 'owner' ? <span className="cloud-muted">Recovery account</span> : <button className="cloud-button" onClick={() => setEditing(item)}><Pencil size={13} />{item.status === 'pending' ? 'Review' : 'Edit'}</button>}</td></tr>)}
        {!rows.length && <tr><td colSpan={5}>No members match this search.</td></tr>}
      </tbody></table></div>
    </section>}
    {editing && <MemberAccess key={editing.id} member={editing} clusters={directory.data?.clusters} directoryError={directory.error} close={() => setEditing(undefined)} />}
  </div>
}

function MemberAccess({ member, clusters, directoryError, close }: { member: HubMember; clusters?: HubCluster[]; directoryError: unknown; close: () => void }) {
  const client = useQueryClient()
  const [role, setRole] = useState(member.role)
  const [status, setStatus] = useState<HubMember['status']>(member.status === 'pending' ? 'active' : member.status)
  const [clusterIds, setClusterIds] = useState(member.clusterIds)
  const [groups, setGroups] = useState(member.kubernetesGroups.join('\n'))
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  async function save(event: FormEvent) {
    event.preventDefault()
    if (busy || !confirmed || !clusters || directoryError) return
    setBusy(true); setError(undefined)
    try {
      await cloudRequest(`/hub/members/${encodeURIComponent(member.id)}`, { method: 'PUT', body: {
        role, status, clusterIds: role === 'admin' ? [] : clusterIds,
        kubernetesGroups: [...new Set(groups.split(/[\n,]/).map(value => value.trim()).filter(Boolean))],
      } })
      await client.invalidateQueries({ queryKey: ['portal', '/api'] })
      close()
    } catch (failure) { setError(failure) } finally { setBusy(false) }
  }
  return <CloudDialog title="Member access" onClose={close} busy={busy}><form className="cloud-form" onSubmit={save}>
    <strong>{member.displayName}</strong>
    <label>Kubernetes identity<code className="cloud-member-id">{member.userId}</code></label><CopyButton value={member.userId} label="Copy identity" />
    <label>Role<select value={role} disabled={busy} onChange={event => setRole(event.target.value as HubMember['role'])}><option value="admin">Admin — manage organization and all clusters</option><option value="member">Member — use selected clusters</option><option value="viewer">Viewer — read-only selected clusters</option></select></label>
    <label>Status<select value={status} disabled={busy} onChange={event => setStatus(event.target.value as HubMember['status'])}><option value="active">Active</option><option value="pending">Pending approval</option><option value="disabled">Disabled</option></select></label>
    {directoryError ? <CloudError error={directoryError} /> : !clusters ? <CloudLoading label="Loading cluster scope…" /> : role === 'admin' ? <p>Admins have access to all current and future Hub clusters. Kubernetes permissions still require explicit RBAC bindings.</p> : <fieldset className="cloud-member-scope" disabled={busy}><legend>Allowed clusters</legend>{clusters.filter(cluster => !cluster.revokedAt).map(cluster => <label key={cluster.id}><input type="checkbox" checked={clusterIds.includes(cluster.id)} onChange={event => setClusterIds(ids => event.target.checked ? [...ids, cluster.id] : ids.filter(id => id !== cluster.id))} />{cluster.name}<small>{cluster.id}</small></label>)}<small>No selection means no cluster access.</small></fieldset>}
    <label>Kubernetes groups<textarea value={groups} disabled={busy} onChange={event => setGroups(event.target.value)} placeholder="radar:idp:team-name" /><small>Optional, one per line. Only trusted radar:* or cloud:* groups. Existing RoleBindings may grant powerful access; groups are not copied from IdP claims.</small></label>
    <label className="cloud-confirm-access"><input type="checkbox" checked={confirmed} disabled={busy} onChange={event => setConfirmed(event.target.checked)} />I understand that saving access changes ends this member's browser sessions and revokes their existing MCP tokens.</label>
    {error !== undefined && <CloudError error={error} />}
    <footer><button type="button" className="cloud-button" disabled={busy} onClick={close}>Cancel</button><button className="cloud-button cloud-primary" disabled={busy || !confirmed || !clusters || !!directoryError}>{busy ? 'Saving…' : 'Save access'}</button></footer>
  </form></CloudDialog>
}

export function CloudSSO() {
  const configuration = useCloudQuery<SSOConfiguration>('/hub/sso')
  if (configuration.isPending) return <CloudLoading label="Loading SSO configuration…" />
  if (configuration.error) return <CloudError error={configuration.error} retry={() => { void configuration.refetch() }} />
  const sso = configuration.data
  if (!sso) return null
  return <div className="cloud-page cloud-narrow">
    <div className="cloud-page-heading"><div><h1>Single sign-on</h1><p>Independent identity for your self-hosted Hub.</p></div><Badge severity={sso.enabled ? 'info' : 'neutral'}>{sso.enabled ? 'Configured' : 'Not configured'}</Badge></div>
    <section className="cloud-card"><header><h2><ShieldCheck size={17} /> OpenID Connect</h2></header>
      {sso.enabled ? <dl className="cloud-identity-details"><dt>Issuer</dt><dd>{sso.issuer}</dd><dt>Client ID</dt><dd>{sso.clientId}</dd><dt>Callback URL</dt><dd>{sso.redirectUrl}</dd><dt>Enrollment</dt><dd>Administrator approval required</dd></dl> : <p>Configure the Portal's OIDC issuer, client ID, callback URL and secret to enable Keycloak sign-in. Missing configuration does not disable the local recovery account.</p>}
      <p>Configuration is managed at server startup. Client secrets are never returned to this page. Configured does not confirm current identity-provider availability.</p>
    </section>
    {sso.enabled && <section className="cloud-card"><h2>Team sign-in link</h2><p>Share this link with people who already have an account in your identity provider. First sign-in requests membership; the link itself grants no access.</p><div className="cloud-endpoint"><code>{`${window.location.origin}/api/auth/oidc/login`}</code><CopyButton value={`${window.location.origin}/api/auth/oidc/login`} label="Copy link" /></div></section>}
    <section className="cloud-card"><h2>Access lifecycle</h2><p>First sign-in creates a pending member with no cluster access. Administrators approve roles, cluster scope and optional Kubernetes groups in Members.</p><p>Portal membership suspension ends existing Portal sessions and revokes that member's MCP tokens. Signing out only ends the current browser session, not independent MCP tokens.</p><CloudLink className="cloud-button" href="/settings/organization/members">View members</CloudLink></section>
    <section className="cloud-card"><h2>Directory synchronization unavailable</h2><p>Directory sync and IdP-initiated back-channel logout are not implemented. Disabling a user only in Keycloak does not immediately invalidate existing Hub sessions or MCP tokens. Disable their Portal membership as well.</p></section>
  </div>
}

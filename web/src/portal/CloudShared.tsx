import { useState, type AnchorHTMLAttributes, type ReactNode } from 'react'
import { AlertTriangle, Check, Copy, Loader2, X } from 'lucide-react'
import { Badge, DialogPortal, Tooltip } from '@maomada/k8s-ui'
import { CloudRequestError, type HubCluster } from './cloudApi'
import { navigateCloud } from './cloudNavigation'

export function CloudLink({ href = '/', onClick, title, ...props }: AnchorHTMLAttributes<HTMLAnchorElement>) {
  const link = <a {...props} href={href} onClick={event => {
    onClick?.(event)
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || props.target || props.download) return
    if (new URL(href, window.location.origin).origin !== window.location.origin) return
    event.preventDefault()
    navigateCloud(href)
  }} />
  return title ? <Tooltip content={title} position="right" wrapperClassName={props.className?.includes('cloud-nav-item') ? 'w-full' : undefined}>{link}</Tooltip> : link
}

export function CloudError({ error, retry }: { error: unknown; retry?: () => void }) {
  return <div role="alert" className="cloud-error"><AlertTriangle size={18} /><div>
    <strong>{error instanceof Error ? error.message : 'Data is unavailable'}</strong>
    {error instanceof CloudRequestError && error.requestId && <p className="portal-mono">Request {error.requestId}</p>}
    {retry && <button className="cloud-text-button" onClick={retry}>Try again</button>}
  </div></div>
}

export function CloudLoading({ label = 'Loading live data…' }: { label?: string }) {
  return <div role="status" className="cloud-loading"><Loader2 className="animate-spin" size={18} />{label}</div>
}

export function ClusterStatus({ cluster }: { cluster: HubCluster }) {
  return <Badge severity={cluster.state === 'connected' ? 'success' : cluster.state === 'revoked' ? 'neutral' : 'warning'}>
    {cluster.state === 'connected' ? 'Connected' : cluster.state === 'revoked' ? 'Revoked' : 'Disconnected'}
  </Badge>
}

export function CloudDialog({ title, children, onClose, busy = false }: {
  title: string; children: ReactNode; onClose: () => void; busy?: boolean
}) {
  return <DialogPortal open onClose={onClose} closable={!busy} ariaLabel={title} className="w-full max-w-lg">
    <div className="cloud-dialog"><header><h2>{title}</h2><button type="button" aria-label="Close dialog" disabled={busy} onClick={onClose}><X size={18} /></button></header>{children}</div>
  </DialogPortal>
}

export function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState('')
  return <><button className="cloud-button" type="button" onClick={async () => {
    try { await navigator.clipboard.writeText(value); setCopied(true); setError('') }
    catch { setError('Clipboard unavailable. Select and copy the text manually.') }
  }}>{copied ? <Check size={14} /> : <Copy size={14} />}{copied ? 'Copied' : label}</button>{error && <p role="status">{error}</p>}</>
}

export function relativeTime(value?: string) {
  if (!value || !Number.isFinite(Date.parse(value))) return 'Unknown'
  const minutes = Math.max(0, Math.floor((Date.now() - Date.parse(value)) / 60000))
  return minutes < 1 ? 'Just now' : minutes < 60 ? `${minutes}m ago` : minutes < 1440 ? `${Math.floor(minutes / 60)}h ago` : `${Math.floor(minutes / 1440)}d ago`
}

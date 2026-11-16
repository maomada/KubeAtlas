import { useState, type AnchorHTMLAttributes, type ReactNode } from 'react'
import { AlertTriangle, Check, Copy, Loader2, X } from 'lucide-react'
import { Badge, DialogPortal, Tooltip } from '@maomada/k8s-ui'
import { copyText } from '@maomada/k8s-ui/utils/clipboard'
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
    <strong>{error instanceof Error ? error.message : '数据不可用'}</strong>
    {error instanceof CloudRequestError && error.code && <p className="portal-mono">错误代码：{error.code}</p>}
    {error instanceof CloudRequestError && error.requestId && <p className="portal-mono">请求 ID：{error.requestId}</p>}
    {retry && <button className="cloud-text-button" onClick={retry}>重试</button>}
  </div></div>
}

export function CloudLoading({ label = '正在读取实时数据…' }: { label?: string }) {
  return <div role="status" className="cloud-loading"><Loader2 className="animate-spin" size={18} />{label}</div>
}

export function ClusterStatus({ cluster }: { cluster: HubCluster }) {
  return <Badge severity={cluster.state === 'connected' ? 'success' : cluster.state === 'revoked' ? 'neutral' : 'warning'}>
    {cluster.state === 'connected' ? '已连接' : cluster.state === 'revoked' ? '已撤销' : '未连接'}
  </Badge>
}

export function CloudDialog({ title, children, onClose, busy = false }: {
  title: string; children: ReactNode; onClose: () => void; busy?: boolean
}) {
  return <DialogPortal open onClose={onClose} closable={!busy} ariaLabel={title} className="w-full max-w-lg max-h-[90dvh] overflow-y-auto">
    <div className="cloud-dialog"><header><h2>{title}</h2><button type="button" aria-label="关闭对话框" disabled={busy} onClick={onClose}><X size={18} /></button></header>{children}</div>
  </DialogPortal>
}

export function CopyButton({ value, label = '复制' }: { value: string; label?: string }) {
  const [status, setStatus] = useState<'copied' | 'failed' | null>(null)
  return <><button className="cloud-button" type="button" onClick={() => {
    setStatus(null)
    void copyText(value).then(copied => setStatus(copied ? 'copied' : 'failed')).catch(() => setStatus('failed'))
  }}>{status === 'copied' ? <Check size={14} /> : <Copy size={14} />}{status === 'copied' ? '已复制' : label}</button>
    {status === 'failed' && <p role="status">复制失败：浏览器未能写入剪贴板，请重试或手动选择文本复制。</p>}</>
}

export function UnencryptedHttpWarning({ origin, enrollment = false }: { origin?: string; enrollment?: boolean }) {
  if (typeof window === 'undefined') return null
  let url: URL
  try { url = new URL(origin ?? window.location.href) } catch { return null }
  const hostname = url.hostname.toLowerCase()
  const loopback = hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '[::1]' || hostname === '::1' || /^127(?:\.\d{1,3}){3}$/.test(hostname)
  if (url.protocol !== 'http:' || loopback) return null
  return <p role="alert" className="portal-card mt-5 border-warning p-4 text-sm text-warning-text">HTTP 未加密：密码、会话 Cookie 和 Token 可能被观察或篡改，下载内容也可能被拦截或修改。HTTP + 字面 IP 仅在服务器明确配置并启用相应支持后，才可在可信网络中使用；建议使用 HTTPS。{enrollment ? ' 如需 HTTP 镜像仓库，操作员必须单独配置节点运行时；本 Portal 不会修改节点或容器运行时设置。' : ''}</p>
}

export function relativeTime(value?: string) {
  if (!value || !Number.isFinite(Date.parse(value))) return '未知'
  const minutes = Math.max(0, Math.floor((Date.now() - Date.parse(value)) / 60000))
  return minutes < 1 ? '刚刚' : minutes < 60 ? `${minutes} 分钟前` : minutes < 1440 ? `${Math.floor(minutes / 60)} 小时前` : `${Math.floor(minutes / 1440)} 天前`
}

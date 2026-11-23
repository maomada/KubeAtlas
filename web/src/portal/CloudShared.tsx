import { useState, type AnchorHTMLAttributes, type ReactNode } from 'react'
import { AlertTriangle, Check, Copy, Loader2, X } from 'lucide-react'
import { Badge, DialogPortal, Tooltip } from '@maomada/k8s-ui'
import { useLocale, type Locale } from '@maomada/k8s-ui/hooks/useLocale'
import { copyText } from '@maomada/k8s-ui/utils/clipboard'
import { cloudErrorMessage, CloudRequestError, type HubCluster } from './cloudApi'
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
  const { t } = useLocale()
  return <div role="alert" className="cloud-error"><AlertTriangle size={18} /><div>
    <strong>{cloudErrorMessage(error, t)}</strong>
    {error instanceof CloudRequestError && error.code && <p className="portal-mono">{t('错误代码：', 'Error code: ')}{error.code}</p>}
    {error instanceof CloudRequestError && error.requestId && <p className="portal-mono">{t('请求 ID：', 'Request ID: ')}{error.requestId}</p>}
    {retry && <button className="cloud-text-button" onClick={retry}>{t('重试', 'Retry')}</button>}
  </div></div>
}

export function CloudLoading({ label }: { label?: string }) {
  const { t } = useLocale()
  return <div role="status" className="cloud-loading"><Loader2 className="animate-spin" size={18} />{label ?? t('正在读取实时数据…', 'Loading live data…')}</div>
}

export function ClusterStatus({ cluster }: { cluster: HubCluster }) {
  const { t } = useLocale()
  return <Badge severity={cluster.state === 'connected' ? 'success' : cluster.state === 'revoked' ? 'neutral' : 'warning'}>
    {cluster.state === 'connected' ? t('已连接', 'Connected') : cluster.state === 'revoked' ? t('已撤销', 'Revoked') : t('未连接', 'Disconnected')}
  </Badge>
}

export function CloudDialog({ title, children, onClose, busy = false }: {
  title: string; children: ReactNode; onClose: () => void; busy?: boolean
}) {
  const { t } = useLocale()
  return <DialogPortal open onClose={onClose} closable={!busy} ariaLabel={title} className="w-full max-w-lg max-h-[90dvh] overflow-y-auto">
    <div className="cloud-dialog"><header><h2>{title}</h2><button type="button" aria-label={t('关闭对话框', 'Close dialog')} disabled={busy} onClick={onClose}><X size={18} /></button></header>{children}</div>
  </DialogPortal>
}

export function CopyButton({ value, label }: { value: string; label?: string }) {
  const { t } = useLocale()
  const [status, setStatus] = useState<'copied' | 'failed' | null>(null)
  return <><button className="cloud-button" type="button" onClick={() => {
    setStatus(null)
    void copyText(value).then(copied => setStatus(copied ? 'copied' : 'failed')).catch(() => setStatus('failed'))
  }}>{status === 'copied' ? <Check size={14} /> : <Copy size={14} />}{status === 'copied' ? t('已复制', 'Copied') : label ?? t('复制', 'Copy')}</button>
    {status === 'failed' && <p role="status">{t('复制失败：浏览器未能写入剪贴板，请重试或手动选择文本复制。', 'Clipboard access failed. Try again or select and copy the text manually.')}</p>}</>
}

export function UnencryptedHttpWarning({ origin, enrollment = false }: { origin?: string; enrollment?: boolean }) {
  const { t } = useLocale()
  if (typeof window === 'undefined') return null
  let url: URL
  try { url = new URL(origin ?? window.location.href) } catch { return null }
  const hostname = url.hostname.toLowerCase()
  const loopback = hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '[::1]' || hostname === '::1' || /^127(?:\.\d{1,3}){3}$/.test(hostname)
  if (url.protocol !== 'http:' || loopback) return null
  return <p role="alert" className="portal-card mt-5 border-warning p-4 text-sm text-warning-text">
    {t(
      'HTTP 未加密：密码、会话 Cookie 和 Token 可能被观察或篡改，下载内容也可能被拦截或修改。HTTP + 字面 IP 仅在服务器明确配置并启用相应支持后，才可在可信网络中使用；建议使用 HTTPS。',
      'HTTP is unencrypted. Passwords, session cookies, tokens, and downloads can be intercepted or modified. Use HTTP with a literal IP only on trusted networks and with explicit server configuration. Use HTTPS whenever possible.',
    )}
    {enrollment && t(
      ' 如需 HTTP 镜像仓库，操作员必须单独配置节点运行时；本 Portal 不会修改节点或容器运行时设置。',
      ' For HTTP image registries, configure node runtimes separately. The Hub leaves node and container runtime settings unchanged.',
    )}
  </p>
}

const englishRelativeTime = new Intl.RelativeTimeFormat('en')

export function relativeTime(value?: string, locale: Locale = 'zh-CN') {
  const timestamp = value ? Date.parse(value) : NaN
  if (!Number.isFinite(timestamp)) return locale === 'en' ? 'Unknown' : '未知'
  const minutes = Math.max(0, Math.floor((Date.now() - timestamp) / 60000))
  if (locale === 'en') {
    if (minutes < 1) return 'Just now'
    if (minutes < 60) return englishRelativeTime.format(-minutes, 'minute')
    if (minutes < 1440) return englishRelativeTime.format(-Math.floor(minutes / 60), 'hour')
    return englishRelativeTime.format(-Math.floor(minutes / 1440), 'day')
  }
  return minutes < 1 ? '刚刚' : minutes < 60 ? `${minutes} 分钟前` : minutes < 1440 ? `${Math.floor(minutes / 60)} 小时前` : `${Math.floor(minutes / 1440)} 天前`
}

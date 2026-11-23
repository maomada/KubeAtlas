import { useId } from 'react'
import { useLocale } from '@maomada/k8s-ui/hooks/useLocale'
import { ThemeToggle } from './ThemeToggle'

export function PortalLanguageSelect({ hideLabel = false }: { hideLabel?: boolean }) {
  const id = useId()
  const { locale, setLocale, t } = useLocale()
  return <>
    <label htmlFor={id} className={hideLabel ? 'sr-only' : 'font-semibold'}>{t('语言', 'Language')}</label>
    <select id={id} className="portal-control" value={locale} onChange={event => setLocale(event.target.value === 'en' ? 'en' : 'zh-CN')}>
      <option value="zh-CN" lang="zh-CN">简体中文</option>
      <option value="en" lang="en">English</option>
    </select>
  </>
}

export function PortalHeaderActions() {
  return <div className="portal-auth-actions flex shrink-0 items-center gap-2">
    <PortalLanguageSelect hideLabel />
    <ThemeToggle />
  </div>
}

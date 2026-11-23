import { Moon, Sun } from 'lucide-react'
import { useLocale } from '@maomada/k8s-ui/hooks/useLocale'
import { usePortalTheme } from './usePortalTheme'

export function ThemeToggle() {
  const { t } = useLocale()
  const [dark, setDark] = usePortalTheme()
  return <button type="button" className="portal-control" onClick={() => setDark(value => !value)} aria-label={dark ? t('切换浅色主题', 'Switch to light theme') : t('切换深色主题', 'Switch to dark theme')}>{dark ? <Sun size={17} /> : <Moon size={17} />}</button>
}

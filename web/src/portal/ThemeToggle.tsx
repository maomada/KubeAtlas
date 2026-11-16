import { Moon, Sun } from 'lucide-react'
import { usePortalTheme } from './usePortalTheme'

export function ThemeToggle() {
  const [dark, setDark] = usePortalTheme()
  return <button type="button" className="portal-control" onClick={() => setDark(value => !value)} aria-label={dark ? '切换浅色主题' : '切换深色主题'}>{dark ? <Sun size={17} /> : <Moon size={17} />}</button>
}

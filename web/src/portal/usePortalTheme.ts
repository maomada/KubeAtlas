import { readProductStorage } from '@maomada/k8s-ui/utils/legacy-brand'
import { useEffect, useState } from 'react'

function initialTheme() {
  try {
    const portal = readProductStorage(localStorage, 'kubeatlas-portal-theme')
    return (portal === 'dark' || portal === 'light' ? portal : readProductStorage(localStorage, 'kubeatlas-theme')) === 'dark'
  } catch { return false }
}

export function usePortalTheme() {
  const [dark, setDark] = useState(initialTheme)
  useEffect(() => {
    const sync = (event: StorageEvent) => {
      if ((event.key === 'kubeatlas-theme' || event.key === 'kubeatlas-portal-theme') && (event.newValue === 'dark' || event.newValue === 'light')) setDark(event.newValue === 'dark')
    }
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [])
  useEffect(() => {
    const theme = dark ? 'dark' : 'light'
    document.documentElement.classList.toggle('dark', dark)
    document.documentElement.style.colorScheme = theme
    try {
      localStorage.setItem('kubeatlas-portal-theme', theme)
      localStorage.setItem('kubeatlas-theme', theme)
    } catch { /* Browser preferences are optional. */ }
    window.dispatchEvent(new StorageEvent('storage', { key: 'kubeatlas-theme', newValue: theme }))
  }, [dark])
  return [dark, setDark] as const
}

import type { ReactNode } from 'react'

export function PaneLoader({
  label = '加载中…',
  className = '',
  children,
}: {
  label?: string
  className?: string
  children?: ReactNode
}) {
  return (
    <div className={`flex items-center justify-center ${className}`} aria-live="polite">
      <div className="min-w-0 max-w-full px-4 text-center">
        <span className="block text-sm font-medium text-theme-text-secondary">{label}</span>
        {children && <div className="mt-2 text-sm text-theme-text-tertiary">{children}</div>}
      </div>
    </div>
  )
}

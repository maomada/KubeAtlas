export function resolveNavigationUrl(href: string, base: string): string | null {
  try {
    const url = new URL(href, base)
    if (url.protocol !== 'https:' && url.protocol !== 'http:') return null
    if (url.username || url.password) return null
    return url.href
  } catch {
    return null
  }
}

import { LEGACY_READ_CODES as LEGACY_BRAND_READ_CODES } from './legacy-brand'

// What resolves each read code, mirroring KubeAtlas's pkg/readreason. A test
// checks this table against pkg/readreason/testdata/actions.json.
export type ReadAction =
  | 'retry'
  | 'wait'
  | 'check_target'
  | 'change_request'
  | 'user_access'
  | 'kubeatlas_config'
  | 'none'

export const READ_ACTIONS: Record<string, ReadAction> = {
  cluster_connecting: 'retry',
  cluster_disconnected: 'kubeatlas_config',
  connection_changed: 'check_target',
  cursor_expired: 'change_request',
  filter_eval_failed: 'change_request',
  findings_scan_incomplete: 'change_request',
  invalid_cursor: 'change_request',
  invalid_parameters: 'change_request',
  kind_not_watchable: 'change_request',
  kind_not_searchable: 'change_request',
  kind_sync_pending: 'retry',
  kind_sync_timed_out: 'wait',
  kind_unknown: 'change_request',
  list_too_large: 'change_request',
  namespace_not_found: 'change_request',
  namespace_required: 'change_request',
  object_not_found: 'none',
  outside_namespace_pin: 'kubeatlas_config',
  kubeatlas_credentials_rejected: 'kubeatlas_config',
  kubeatlas_forbidden: 'kubeatlas_config',
  kubeatlas_kind_not_collected: 'kubeatlas_config',
  kubeatlas_namespace_not_collected: 'kubeatlas_config',
  read_error: 'retry',
  user_forbidden: 'user_access',
  user_namespace_not_visible: 'change_request',
  user_no_namespace_access: 'user_access',
  user_permission_check_failed: 'retry',
}

// Spellings KubeAtlas releases sent before the shared vocabulary, for codes that
// kept their meaning: a newer UI reading an older KubeAtlas reads them as the code
// they became.
const LEGACY_READ_CODES: Record<string, string> = {
  ...LEGACY_BRAND_READ_CODES,
  kind_sync_failed: 'kind_sync_timed_out',
  rbac_denied: 'user_forbidden',
}

/** The current spelling of a read code, translating one an older KubeAtlas sent. */
export function canonicalReadCode(code: unknown): unknown {
  return typeof code === 'string' ? (LEGACY_READ_CODES[code] ?? code) : code
}

/** The action for a code; an unknown code (from a newer KubeAtlas) is treated as needing a change. */
export function readActionFor(code: unknown): ReadAction | undefined {
  const current = canonicalReadCode(code)
  return typeof current === 'string' ? (READ_ACTIONS[current] ?? 'change_request') : undefined
}

/** Why a read failed, in a few plain words; an unknown code reads as a failed read. */
export function gapReasonText(reason: string): string {
  switch (canonicalReadCode(reason)) {
    case 'kubeatlas_kind_not_collected':
    case 'kubeatlas_namespace_not_collected':
      return "KubeAtlas doesn't collect it"
    case 'kind_sync_pending':
      return 'still loading'
    case 'kind_sync_timed_out':
      return 'loading is overdue'
    case 'kubeatlas_forbidden':
    case 'kubeatlas_credentials_rejected':
      return 'KubeAtlas may not list it'
    case 'kind_not_watchable':
      return "its API can't be watched"
    case 'user_permission_check_failed':
      return "your access couldn't be checked"
    case 'user_forbidden':
      return 'you may not list it'
    case 'kind_unknown':
      return "KubeAtlas's API discovery doesn't list its kind or version"
    case 'object_not_found':
      return "KubeAtlas doesn't have it yet"
    case 'cluster_connecting':
      return 'KubeAtlas is still connecting'
    case 'cluster_disconnected':
      return 'KubeAtlas lost its connection'
    case 'connection_changed':
      return "KubeAtlas's connection changed meanwhile"
    default:
      return 'the read failed'
  }
}

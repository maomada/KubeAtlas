import { useEffect, useMemo, useRef, useState } from 'react'
import { AlertTriangle, ArrowLeft, Check, Loader2, ShieldAlert, ShieldCheck, X } from 'lucide-react'
import { normalizeYamlForReview, splitYamlDocuments } from '../../utils/yaml'
import { CrossplaneYamlApplyNotice } from './CrossplaneOperationNotice'
import { Badge } from './Badge'
import { StatusDot } from './status-tone'
import { YamlDiffEditor } from './YamlEditor'
import { AlertBanner } from './drawer-components'

export interface YamlPreviewResult {
  index: number
  status: 'accepted' | 'rejected' | 'unavailable'
  apiVersion?: string
  kind?: string
  namespace?: string
  name?: string
  action?: 'create' | 'update' | 'unknown'
  submittedYaml?: string
  baselineYaml?: string
  predictedYaml?: string
  warnings?: string[]
  error?: string
  reviewedResourceVersion?: string
  redacted?: boolean
}

export interface YamlReviewProps {
  submittedYaml: string
  mode?: 'apply' | 'create' | 'update'
  /** Review YAML omits status; raw evidence is bound to one editor target. */
  resourceData?: unknown
  originalYaml?: string
  documents: YamlPreviewResult[]
  nonAtomic?: boolean
  force?: boolean
  isApplying?: boolean
  applyError?: string | null
  /** Set when an apply failed and the refreshed review found the resource had
   *  changed since the user reviewed it: the diff now shows its latest version. */
  changedSinceReview?: boolean
  applyLabel?: string
  onClose?: () => void
  onBack: () => void
  onApply: () => void
  /** Set when the review replaces a dialog's own title, which names the dialog. */
  titleId?: string
}

function splitYaml(content: string) {
  return splitYamlDocuments(content).map((document) => document.content)
}

function previewSeverity(status: YamlPreviewResult['status']) {
  if (status === 'accepted') return 'success' as const
  if (status === 'rejected') return 'error' as const
  return 'warning' as const
}

function previewStatusLabel(status: YamlPreviewResult['status']) {
  switch (status) {
    case 'accepted': return '已接受'
    case 'rejected': return '已拒绝'
    case 'unavailable': return '无法预览'
    default: return status
  }
}

function previewActionLabel(action: NonNullable<YamlPreviewResult['action']>) {
  if (action === 'create') return '将创建'
  if (action === 'update') return '将更新'
  return `将${action}`
}

// The server's refusal when the resource moved past the reviewed version. The
// changed-after-review notice already says this, so only this error gives way
// to it; a timeout or an admission denial still shows beside the notice.
function isChangedAfterReviewError(error: string) {
  return /resource changed after review/i.test(error)
}

export function canApplyYamlPreview(
  documents: YamlPreviewResult[],
  unavailableAcknowledged: boolean,
  reviewReady = true,
) {
  return (
    reviewReady &&
    !documents.some((document) => document.status === 'rejected') &&
    (unavailableAcknowledged || !documents.some((document) => document.status === 'unavailable'))
  )
}

export function reviewedResourceVersionsForPreview(documents: YamlPreviewResult[]) {
  return Object.fromEntries(
    documents.flatMap((document) => {
      if (document.action === 'create') return [[document.index, '']]
      if (document.action === 'update' && document.reviewedResourceVersion) {
        return [[document.index, document.reviewedResourceVersion]]
      }
      return []
    }),
  )
}

export function YamlReview({
  submittedYaml,
  mode = 'apply',
  resourceData,
  originalYaml,
  documents,
  nonAtomic = false,
  force = false,
  isApplying = false,
  applyError,
  changedSinceReview = false,
  applyLabel = '应用已审查的更改',
  onClose,
  onBack,
  onApply,
  titleId,
}: YamlReviewProps) {
  const [selectedIndex, setSelectedIndex] = useState(0)
  const reviewRef = useRef<HTMLDivElement>(null)
  const [unified, setUnified] = useState(
    () => typeof window !== 'undefined' && window.innerWidth < 900,
  )
  const diffLayoutChosen = useRef(false)
  const [hideUnchanged, setHideUnchanged] = useState(true)
  const [unavailableAcknowledged, setUnavailableAcknowledged] = useState(false)
  const [diffReady, setDiffReady] = useState(false)
  const reviewSignature = useMemo(
    () =>
      JSON.stringify(
        documents.map(
          ({
            index,
            status,
            action,
            reviewedResourceVersion,
            predictedYaml,
            submittedYaml: documentYaml,
            error,
          }) => ({
            index,
            status,
            action,
            reviewedResourceVersion,
            predictedYaml,
            documentYaml,
            error,
          }),
        ),
      ),
    [documents],
  )
  const submitted = useMemo(() => splitYaml(submittedYaml), [submittedYaml])
  const original = useMemo(
    () => (originalYaml ? splitYaml(originalYaml) : submitted),
    [originalYaml, submitted],
  )
  const selected = documents[selectedIndex] ?? documents[0]
  const before = normalizeYamlForReview(
    selected?.baselineYaml ||
      (originalYaml
        ? (original[selected?.index] ?? '')
        : selected?.submittedYaml || submitted[selected?.index] || ''),
  )
  const after = normalizeYamlForReview(
    selected?.predictedYaml || selected?.submittedYaml || submitted[selected?.index] || '',
  )
  const unavailable = documents.filter((document) => document.status === 'unavailable')
  const rejected = documents.filter((document) => document.status === 'rejected')
  const accepted = documents.filter((document) => document.status === 'accepted')
  const canApply = canApplyYamlPreview(documents, unavailableAcknowledged, diffReady)
  const applyBlockedReason = isApplying
    ? '正在应用已审查的更改'
    : rejected.length > 0
      ? `应用前请修正 ${rejected.length} 个被拒绝的文档`
      : unavailable.length > 0 && !unavailableAcknowledged
        ? `应用前请确认 ${unavailable.length} 个未经验证的文档`
        : !diffReady
          ? '正在等待审查差异呈现'
          : undefined
  const noChanges = selected?.status === 'accepted' && before === after

  useEffect(() => {
    setUnavailableAcknowledged(false)
  }, [reviewSignature])

  useEffect(() => {
    const review = reviewRef.current
    if (!review) return
    const adaptDiffLayout = () => {
      if (!diffLayoutChosen.current) setUnified(review.clientWidth < 800)
    }
    adaptDiffLayout()
    const observer = new ResizeObserver(adaptDiffLayout)
    observer.observe(review)
    return () => observer.disconnect()
  }, [])

  return (
    <div ref={reviewRef} className="flex h-full min-h-0 flex-col bg-theme-base">
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-theme-border bg-theme-elevated/70 px-4 py-2.5">
        <button
          type="button"
          onClick={onBack}
          disabled={isApplying}
          className="flex items-center gap-1.5 rounded px-2 py-1 text-xs text-theme-text-secondary hover:bg-theme-hover hover:text-theme-text-primary disabled:opacity-50"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          返回编辑
        </button>
        <div className="h-4 w-px bg-theme-border" />
        <div>
          <div id={titleId} className="text-sm font-semibold text-theme-text-primary">
            审查集群准入的更改
          </div>
          <div className="text-xs text-theme-text-tertiary">
            {accepted.length} 个已接受 · {rejected.length} 个已拒绝 · {unavailable.length} 个无法预览
          </div>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <div className="flex rounded-md border border-theme-border bg-theme-base p-0.5 text-xs">
            <button
              type="button"
              onClick={() => {
                diffLayoutChosen.current = true
                setUnified(false)
              }}
              aria-pressed={!unified}
              className={`rounded px-2 py-1 ${!unified ? 'bg-theme-elevated text-theme-text-primary' : 'text-theme-text-tertiary hover:text-theme-text-secondary'}`}
            >
              并排
            </button>
            <button
              type="button"
              onClick={() => {
                diffLayoutChosen.current = true
                setUnified(true)
              }}
              aria-pressed={unified}
              className={`rounded px-2 py-1 ${unified ? 'bg-theme-elevated text-theme-text-primary' : 'text-theme-text-tertiary hover:text-theme-text-secondary'}`}
            >
              统一视图
            </button>
          </div>
          <label className="flex items-center gap-1.5 text-xs text-theme-text-secondary">
            <input
              type="checkbox"
              checked={hideUnchanged}
              onChange={(event) => setHideUnchanged(event.target.checked)}
              className="h-3.5 w-3.5 rounded border-theme-border bg-theme-base"
            />
            折叠未更改内容
          </label>
          {onClose && (
            <button
              type="button"
              onClick={onClose}
              disabled={isApplying}
              aria-label="关闭审查"
              className="rounded p-1 text-theme-text-secondary hover:bg-theme-hover hover:text-theme-text-primary disabled:opacity-50"
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>
      </div>

      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        {documents.length > 1 && (
          <nav
            className="max-h-36 shrink-0 overflow-auto border-b border-theme-border bg-theme-surface p-2 md:max-h-none md:w-56 md:border-b-0 md:border-r"
            aria-label="预览的 YAML 文档"
          >
            {documents.map((document, index) => (
              <button
                type="button"
                key={`${document.index}:${document.kind}:${document.name}`}
                onClick={() => setSelectedIndex(index)}
                className={`mb-1 flex w-full items-start gap-2 rounded-md border px-2.5 py-2 text-left ${
                  index === selectedIndex
                    ? 'border-theme-border bg-theme-elevated'
                    : 'border-transparent hover:bg-theme-hover'
                }`}
              >
                <StatusDot
                  tone={
                    document.status === 'accepted'
                      ? 'healthy'
                      : document.status === 'rejected'
                        ? 'unhealthy'
                        : 'degraded'
                  }
                  size="md"
                  className="mt-1"
                />
                <span className="min-w-0">
                  <span className="block truncate text-xs font-medium text-theme-text-primary">
                    {document.kind || `文档 ${document.index + 1}`}
                  </span>
                  <span className="block truncate text-[11px] text-theme-text-tertiary">
                    {document.namespace ? `${document.namespace}/` : ''}
                    {document.name || '未命名'}
                  </span>
                </span>
              </button>
            ))}
          </nav>
        )}

        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          {selected && (
            <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-theme-border bg-theme-surface px-3 py-2">
              <Badge severity={previewSeverity(selected.status)} size="sm">
                {selected.status === 'accepted' ? (
                  <Check className="h-3 w-3" />
                ) : (
                  <AlertTriangle className="h-3 w-3" />
                )}
                {previewStatusLabel(selected.status)}
              </Badge>
              {selected.action && selected.action !== 'unknown' && (
                <Badge severity="neutral" size="sm">
                  {previewActionLabel(selected.action)}
                </Badge>
              )}
              <span className="text-xs text-theme-text-tertiary">
                {selected.baselineYaml
                  ? '审查时的实时版本 → 服务器预览'
                  : originalYaml
                    ? '开始编辑时的实时版本 → 服务器预览'
                    : '实际提交的对象 → 服务器预览'}
              </span>
              {selected.redacted && (
                <Badge tone="note" size="sm">
                  Secret 值已遮蔽
                </Badge>
              )}
              {noChanges && (
                <Badge severity="neutral" size="sm">
                  没有实际更改
                </Badge>
              )}
            </div>
          )}

          {(selected?.error || (selected?.warnings?.length ?? 0) > 0) && (
            <div className="shrink-0 space-y-1 border-b border-theme-border bg-theme-surface px-3 py-2 text-xs">
              {selected.error && (
                <div
                  className={
                    selected.status === 'rejected'
                      ? 'text-red-600 dark:text-red-400'
                      : 'text-warning-text'
                  }
                >
                  {selected.error}
                </div>
              )}
              {selected.warnings?.map((warning) => (
                <div key={warning} className="flex items-start gap-1.5 text-warning-text">
                  <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>{warning}</span>
                </div>
              ))}
            </div>
          )}

          <div className="min-h-0 flex-1 bg-theme-surface">
            <YamlDiffEditor
              original={before}
              modified={after}
              height="100%"
              unified={unified}
              hideUnchanged={hideUnchanged}
              bleed
              onReadyChange={setDiffReady}
            />
          </div>
        </div>
      </div>

      <div className="shrink-0 border-t border-theme-border bg-theme-elevated/80 px-4 py-3">
        <CrossplaneYamlApplyNotice yaml={submittedYaml} baselineYaml={originalYaml} previewDocuments={documents} mode={mode} resourceData={resourceData} />
        {changedSinceReview && (
          <AlertBanner
            variant="warning"
            title="此资源在审查后已发生变化"
            message="上方差异现已显示最新版本。请检查后重新应用。"
          />
        )}
        {/* The notice above already says why the apply was refused; the
            raw API message would repeat it (the error toast carries it). */}
        {applyError && !(changedSinceReview && isChangedAfterReviewError(applyError)) && (
          <div className="mb-2 rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-600 dark:text-red-400">
            {applyError}
          </div>
        )}
        {nonAtomic && (
          <div className="mb-2 flex items-start gap-2 text-xs text-theme-text-secondary">
            <ShieldAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning-text" />
            <span>
              资源将按顺序应用，而非以原子方式应用。后续文档失败前，前面的文档可能已成功应用。
            </span>
          </div>
        )}
        {unavailable.length > 0 && rejected.length === 0 && (
          <label className="mb-2 flex items-start gap-2 text-xs text-theme-text-secondary">
            <input
              type="checkbox"
              checked={unavailableAcknowledged}
              onChange={(event) => setUnavailableAcknowledged(event.target.checked)}
              className="mt-0.5 h-3.5 w-3.5 rounded border-theme-border bg-theme-base"
            />
            <span>
              仍要应用 {unavailable.length} 个未经验证的文档。文档可能依赖前面的资源、API 不支持 dry-run，或无法读取实时状态。
              未经验证的文档无法防止审查后发生更改。
            </span>
          </label>
        )}
        {rejected.length > 0 && (
          <div className="mb-2 flex items-center gap-2 text-xs text-red-600 dark:text-red-400">
            <ShieldAlert className="h-3.5 w-3.5" />
            应用前请修正被拒绝的文档。
          </div>
        )}
        <div className="flex items-center justify-between gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-theme-text-tertiary">
            <span className="flex items-center gap-2">
              <ShieldCheck className="h-3.5 w-3.5 shrink-0" />
              预览使用 Kubernetes server-side dry-run。应用前，准入结果仍可能变化。
            </span>
            <Badge severity={force ? 'warning' : 'neutral'} size="sm">
              强制覆盖：{force ? '开启' : '关闭'}
            </Badge>
            <span>
              {force
                ? '其他管理器拥有的字段可能被覆盖。'
                : '与其他管理器字段的冲突会阻止保存。'}
            </span>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {applyBlockedReason && !isApplying && (
              <span
                aria-live="polite"
                className="max-w-48 text-right text-[11px] text-theme-text-tertiary"
              >
                {applyBlockedReason}
              </span>
            )}
            <button
              type="button"
              onClick={onApply}
              disabled={!canApply || isApplying}
              title={applyBlockedReason}
              className="btn-brand flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-4 py-1.5 text-xs font-medium disabled:cursor-not-allowed"
            >
              {isApplying && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {isApplying ? '正在应用…' : applyLabel}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

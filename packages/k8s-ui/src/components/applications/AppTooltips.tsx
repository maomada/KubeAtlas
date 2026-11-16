import { provenanceSource, HEALTH_META, appSourceLabel, isDeclaredAppSource, APP_IDENTITY_ANNOTATION, type AppRolloutSummary, type AppWorkload } from '../../utils/applications'
import { midTruncate } from '../../utils/format'

// Structured tooltip content for the application chips. Both render a short
// title/phrase plus the technical signal (label key, resource name, selector)
// in the shared inline-code chip — readable typography instead of a wall of text.

export function ProvenanceTooltip({
  tier,
  appKey,
  confidence,
}: {
  tier: number | undefined
  appKey: string
  confidence: string | undefined
}) {
  const src = provenanceSource(tier, appKey)
  const conf = confidence ?? 'low'
  return (
    <div className="space-y-1">
      <div className="text-xs text-theme-text-primary">
        按{src.lead}
        {src.code && (
          <>
            {' '}
            <code className="inline-code">{src.code}</code>
          </>
        )}
        {src.trail ? ` ${src.trail}` : ''}
      </div>
      {/* Only flag the case the user should act on — weak evidence. Medium/high
          confidence is resolver detail, not actionable. */}
      {conf === 'low' && (
        <div className={`text-[10px] uppercase tracking-wide ${HEALTH_META.degraded.text}`}>低置信度</div>
      )}
    </div>
  )
}

// "3 versions" on its own says little; the breakdown says which component runs
// what — the right shape for umbrella apps that genuinely have no single version.
export function VersionTooltip({ workloads }: { workloads: Pick<AppWorkload, 'name' | 'version'>[] }) {
  const rows = workloads.filter((w) => w.version)
  if (rows.length === 0) return null
  return (
    <div className="max-w-xs space-y-1">
      {/* These are the running image tags — they can legitimately differ from
          the headline appVersion (a label), so the title says what it shows. */}
      <div className="text-[10px] font-medium uppercase tracking-wide text-theme-text-tertiary">各工作负载的镜像标签</div>
      <ul className="space-y-0.5">
        {rows.map((w, i) => (
          <li key={i} className="flex items-baseline justify-between gap-3">
            <span className="truncate text-[11px] text-theme-text-secondary">{w.name}</span>
            <code className="inline-code text-[10px]">{w.version}</code>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function RolloutSummaryTooltip({ summary }: { summary: AppRolloutSummary }) {
  const details = summary.details.slice(0, 8)
  return (
    <div className="max-w-xs space-y-1">
      {details.map((detail, index) => <div key={index}>{detail}</div>)}
      {summary.details.length > details.length && (
        <div className="text-theme-text-tertiary">另有 {summary.details.length - details.length} 项</div>
      )}
    </div>
  )
}

export function CategoryTooltip({
  category,
  addonReason,
}: {
  category: string
  addonReason?: string
}) {
  const mixed = category === 'mixed'
  const title = mixed ? '混合应用 / 附加组件' : '平台附加组件'
  const lead = mixed
    ? '同时具有应用和附加组件的依据。此分类仅供参考，不代表应用身份。'
    : '依据平台附加组件特征分类；为便于完整查看而显示。'
  // addonReason is a "; "-separated list of signals, e.g.
  //   "app.kubernetes.io/name=argocd-redis (argocd); chart=argo-cd".
  // Render each as its own line; the selector goes in code, the trailing
  // "(namespace)" hint is split out as muted text.
  const items = (addonReason ?? '')
    .split(/;\s*/)
    .map((s) => s.trim())
    .filter(Boolean)
  return (
    <div className="max-w-xs space-y-1.5">
      <div className="text-xs font-semibold text-theme-text-primary">{title}</div>
      <div className="text-[11px] leading-snug text-theme-text-secondary">{lead}</div>
      {items.length > 0 && (
        <div className="space-y-1 border-t border-theme-border pt-1.5">
          <div className="text-[10px] font-medium uppercase tracking-wide text-theme-text-tertiary">依据</div>
          <ul className="max-h-52 space-y-1 overflow-y-auto">
            {items.map((it, i) => {
              const parsed = splitTrailingParen(it)
              const selector = parsed?.lead ?? it
              const where = parsed?.paren
              return (
                <li key={i} className="flex items-baseline gap-1.5">
                  <code className="inline-code text-[10px]">{selector}</code>
                  {where && <span className="text-[10px] text-theme-text-tertiary">{where}</span>}
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}

// AppIdentityTooltip — why these instances are grouped, and (in the fleet view)
// whether the grouping folds across clusters + how to make it. A clear
// source-driven headline, the per-evidence detail lines, and — when `fleet` and
// the identity isn't a declared cross-cluster origin — the canonical "how to
// fold" answer, so the question and its answer share one surface.
//
// `source`/`portable` come from the row's identity; absent (an older host that
// doesn't pass them) the headline degrades to the evidence lines. `fleet` gates
// the cross-cluster "how to fold" line — single-cluster (OSS) has no clusters to
// fold across, so it shows only the grouping evidence.
export function AppIdentityTooltip({
  members,
  source,
  portable,
  fleet,
}: {
  identityKey?: string
  members: { name: string; env: string; confidence: string; evidence: string }[]
  source?: string
  portable?: boolean
  fleet?: boolean
}) {
  const evidences = Array.from(new Set(members.map((m) => m.evidence).filter(Boolean)))
  const heuristicOnly = members.every((m) => m.confidence !== 'high')
  // The wire `portable` flag is authoritative; fall back to deriving it from the
  // source when an older host doesn't pass it.
  const folds = portable ?? isDeclaredAppSource(source)
  return (
    <div className="max-w-xs space-y-1.5">
      {source && <div className="text-xs font-medium text-theme-text-primary">分组依据：{appSourceLabel(source)}。</div>}
      {evidences.map((e, i) => (
        <div key={i} className="text-xs text-theme-text-secondary">
          <EvidenceLine evidence={e} />
        </div>
      ))}
      {fleet && folds && (
        <div className="text-[10px] uppercase tracking-wide text-theme-text-tertiary">跨集群合并显示</div>
      )}
      {fleet && source !== undefined && !folds && (
        <div className="space-y-1 border-t border-theme-border pt-1.5">
          <div className="text-[11px] leading-snug text-theme-text-secondary">
            按集群分别显示——名称相同并不能证明多个集群运行的是同一个应用。若要跨集群合并，请在各集群的工作负载上设置相同的{' '}
            <code className="inline-code">{APP_IDENTITY_ANNOTATION}</code>，或通过 Argo CD / Flux 部署，并在来源路径中包含环境信息。
          </div>
        </div>
      )}
      {!fleet && heuristicOnly && !source && (
        <div className={`text-[10px] uppercase tracking-wide ${HEALTH_META.degraded.text}`}>启发式推断 · 中等置信度</div>
      )}
    </div>
  )
}

// Renders the resolver's evidence string with its signals as inline-code.
// Known shapes from applications_identity.go; anything else passes through raw.
function EvidenceLine({ evidence }: { evidence: string }) {
  const nameStem = quotedEvidenceValue(evidence, 'name stem "', '" + shared image repo ')
  if (nameStem) {
    return (
      <>
        名称相同：<code className="inline-code">{nameStem.value}</code>；镜像：<code className="inline-code">{midTruncate(nameStem.rest, 32)}</code>
      </>
    )
  }
  const envLabel = quotedEvidenceValue(evidence, 'environment label "', '" + name/repo evidence')
  if (envLabel) {
    return (
      <>
        环境标签：<code className="inline-code">{envLabel.value}</code>，以及名称/仓库依据
      </>
    )
  }
  const argo = splitTrailingParen(evidence)
  if (argo && argo.lead.startsWith('Argo CD source path ') && argo.paren.startsWith('env overlay ')) {
    return (
      <>
        Argo CD 来源路径：<code className="inline-code">{argo.lead.slice('Argo CD source path '.length)}</code>
      </>
    )
  }
  return <>{evidence}</>
}

function splitTrailingParen(value: string): { lead: string; paren: string } | null {
  if (!value.endsWith(')')) return null
  const open = value.lastIndexOf(' (')
  if (open < 0) return null
  return { lead: value.slice(0, open), paren: value.slice(open + 2, -1) }
}

function quotedEvidenceValue(value: string, prefix: string, separator: string): { value: string; rest: string } | null {
  if (!value.startsWith(prefix)) return null
  const start = prefix.length
  const sep = value.indexOf(separator, start)
  if (sep < 0) return null
  return { value: value.slice(start, sep), rest: value.slice(sep + separator.length) }
}

// EnvHint — how environments are determined + how to set one explicitly.
// Rendered from the Environment facet's info icon and the unlabeled env
// cells, so the answer lives exactly where the question arises.
export function EnvHint({ unlabeled }: { unlabeled?: boolean }) {
  return (
    <div className="max-w-xs space-y-1">
      {unlabeled ? (
        <div className="text-xs text-theme-text-primary">未检测到此应用的环境信息。</div>
      ) : (
        <div className="text-xs text-theme-text-primary">根据标签、GitOps 覆盖路径或名称模式推断（显示为 <span className="italic">~推断</span>）。</div>
      )}
      <div className="text-[11px] leading-snug text-theme-text-secondary">
        可通过命名空间或工作负载标签显式设置：
      </div>
      <code className="inline-code">environment=staging</code>
      <div className="text-[10px] text-theme-text-tertiary">
        也识别以下键：<code className="inline-code">app.kubernetes.io/environment</code> · <code className="inline-code">env</code> · <code className="inline-code">tags.datadoghq.com/env</code>
      </div>
    </div>
  )
}

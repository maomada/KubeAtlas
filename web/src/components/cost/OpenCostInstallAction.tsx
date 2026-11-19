import { useId, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Package } from 'lucide-react'
import { installChartWithProgress, useCanHelmAct, useChartDetail, useHelmReleases } from '../../api/client'
import { getApiBase } from '../../api/config'

const repository = 'kubeatlas-bundled'
const name = 'opencost'
const namespaceFilter = [name]

export function OpenCostInstallAction({ cluster, onInstalled }: { cluster: string; onInstalled: () => void }) {
  const permission = useCanHelmAct(name)
  if (!permission.allowed) return null
  return <OpenCostInstaller cluster={cluster} onInstalled={onInstalled} />
}

function OpenCostInstaller({ cluster, onInstalled }: { cluster: string; onInstalled: () => void }) {
  const client = useQueryClient()
  const chart = useChartDetail(repository, name)
  const releases = useHelmReleases(namespaceFilter)
  const existing = releases.data?.find(release => release.namespace === name && release.name === name)
  const dialog = useRef<HTMLDialogElement>(null)
  const cancel = useRef<HTMLButtonElement>(null)
  const title = useId()
  const installing = useRef(false)
  const [review, setReview] = useState<{ version: string; apiBase: string; cluster: string }>()
  const [progress, setProgress] = useState('')
  const [error, setError] = useState<string>()
  const [finished, setFinished] = useState(false)

  function openReview() {
    if (!chart.data || !releases.data || existing || chart.isError || releases.isError || !cluster || finished) return
    setReview({ version: chart.data.version, apiBase: getApiBase(), cluster })
    setError(undefined)
    dialog.current?.showModal()
    cancel.current?.focus()
  }

  async function install() {
    if (installing.current || !review || getApiBase() !== review.apiBase || cluster !== review.cluster || chart.isError || releases.isError ||
        chart.data?.version !== review.version || existing || !releases.data || finished) return
    installing.current = true
    setError(undefined)
    setProgress('正在安装 OpenCost…')
    try {
      await installChartWithProgress({
        repository, chartName: name, version: review.version, releaseName: name,
        namespace: name, createNamespace: true,
      }, event => {
        if (event.phase === 'installing') setProgress('正在部署 OpenCost…')
      })
      setFinished(true)
      dialog.current?.close()
      onInstalled()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : '安装失败，请在 Helm 页面检查现有资源。')
    } finally {
      installing.current = false
      setProgress('')
      void client.invalidateQueries({ queryKey: ['helm-releases'] })
    }
  }

  if (finished || existing?.status === 'deployed') {
    return <p role="status" className="text-sm">OpenCost 已部署，等待 Prometheus 采集成本指标。</p>
  }
  if (existing) {
    return <p className="text-sm">已有 OpenCost 部署记录（{existing.status}），请在 Helm 页面检查。</p>
  }
  if (chart.isError || releases.isError) {
    return <div className="flex flex-wrap items-center justify-center gap-2 text-sm">
      <button type="button" className="btn-brand px-3 py-1.5" disabled>安装 OpenCost</button>
      <span>{chart.isError ? '离线安装包不可用，请联系平台管理员。' : '无法确认 OpenCost 的安装状态。'}</span>
      <button type="button" className="text-accent-text hover:underline" onClick={() => {
        void (chart.isError ? chart.refetch() : releases.refetch())
      }}>重新检查</button>
    </div>
  }

  return <>
    <button type="button" className="btn-brand inline-flex items-center gap-1.5 px-3 py-1.5 text-sm"
      disabled={!chart.data || !releases.data || !cluster} onClick={openReview}>
      <Package size={15} aria-hidden="true" />安装 OpenCost
    </button>
    <dialog ref={dialog} aria-labelledby={title} onCancel={event => {
      if (installing.current) event.preventDefault()
    }} className="m-auto max-h-[90dvh] w-[calc(100%_-_2rem)] max-w-md overflow-auto overscroll-contain rounded-xl border border-theme-border bg-theme-surface p-5 text-theme-text-primary shadow-theme-lg backdrop:overflow-hidden backdrop:overscroll-contain backdrop:bg-black/60">
      <h2 id={title} className="text-lg font-semibold">安装 OpenCost</h2>
      <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
        <dt>目标集群</dt><dd className="break-all">{review?.cluster ?? cluster}</dd>
        <dt>安装包版本</dt><dd>{review?.version ?? chart.data?.version}</dd>
        <dt>命名空间</dt><dd>opencost</dd>
      </dl>
      <p className="mt-4 text-sm text-theme-text-secondary">使用平台准备的离线包，接入现有 Prometheus。</p>
      <p role="status" className={progress ? 'mt-3 text-sm' : 'sr-only'}>{progress}</p>
      {error && <p role="alert" className="mt-3 break-words text-sm text-red-500">{error}</p>}
      <div className="mt-5 flex justify-end gap-2">
        <button ref={cancel} type="button" className="rounded-md border border-theme-border px-3 py-1.5 text-sm hover:bg-theme-hover"
          disabled={Boolean(progress)} onClick={() => dialog.current?.close()}>取消</button>
        <button type="button" className="btn-brand px-3 py-1.5 text-sm"
          disabled={Boolean(progress) || Boolean(error) || !review || review.version !== chart.data?.version}
          onClick={() => { void install() }}>确认安装</button>
      </div>
    </dialog>
  </>
}

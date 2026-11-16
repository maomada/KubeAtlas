// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { CopyButton } from './CloudShared'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let host: HTMLDivElement
let root: Root
let clipboardDescriptor: PropertyDescriptor | undefined
let execCommandDescriptor: PropertyDescriptor | undefined

function setClipboard(value: { writeText: (text: string) => Promise<void> } | undefined) {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value })
}

function setExecCommand(implementation: () => boolean) {
  const execCommand = vi.fn(implementation)
  Object.defineProperty(document, 'execCommand', { configurable: true, value: execCommand })
  return execCommand
}

function renderCopyButton(value: string) {
  act(() => root.render(<CopyButton value={value} label="复制注册命令" />))
  return host.querySelector<HTMLButtonElement>('button')!
}

async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.click()
    await new Promise(resolve => setTimeout(resolve, 0))
  })
}

beforeEach(() => {
  clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
  execCommandDescriptor = Object.getOwnPropertyDescriptor(document, 'execCommand')
  setClipboard(undefined)
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(() => {
  act(() => root.unmount())
  host.remove()
  if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor)
  else Reflect.deleteProperty(navigator, 'clipboard')
  if (execCommandDescriptor) Object.defineProperty(document, 'execCommand', execCommandDescriptor)
  else Reflect.deleteProperty(document, 'execCommand')
  vi.restoreAllMocks()
})

it('copies the exact command through the synchronous fallback when the Clipboard API is missing', async () => {
  const command = "KUBEATLAS_PROFILE='lan' \\\n  KUBEATLAS_ORIGIN='http://172.18.0.1:9461' bash install.sh\n"
  const previousFocus = document.createElement('input')
  previousFocus.value = 'preserve this selection'
  document.body.appendChild(previousFocus)
  previousFocus.focus()
  previousFocus.setSelectionRange(2, 9)
  let observed: { value: string; start: number | null; end: number | null } | undefined
  const execCommand = setExecCommand(() => {
    const textarea = document.body.querySelector('textarea')
    observed = {
      value: textarea?.value ?? '',
      start: textarea?.selectionStart ?? null,
      end: textarea?.selectionEnd ?? null,
    }
    return true
  })
  const button = renderCopyButton(command)
  button.focus()

  await click(button)

  expect(navigator.clipboard).toBeUndefined()
  expect(execCommand).toHaveBeenCalledWith('copy')
  expect(observed).toEqual({ value: command, start: 0, end: command.length })
  expect(document.body.querySelector('textarea')).toBeNull()
  expect(document.activeElement).toBe(button)
  expect(previousFocus.selectionStart).toBe(2)
  expect(previousFocus.selectionEnd).toBe(9)
  expect(button.textContent).toContain('已复制')
  expect(host.querySelector('[role="status"]')).toBeNull()
  previousFocus.remove()
})

it('uses the modern Clipboard API when it succeeds', async () => {
  const command = 'exact HTTPS command\n'
  const writeText = vi.fn().mockResolvedValue(undefined)
  setClipboard({ writeText })
  const execCommand = setExecCommand(() => true)
  const button = renderCopyButton(command)

  await click(button)

  expect(writeText).toHaveBeenCalledWith(command)
  expect(execCommand).not.toHaveBeenCalled()
  expect(button.textContent).toContain('已复制')
  expect(host.querySelector('[role="status"]')).toBeNull()
})

it('falls back to the synchronous copy command when the modern API rejects', async () => {
  const command = 'exact command with spaces and a newline\n'
  const writeText = vi.fn().mockRejectedValue(new DOMException('Blocked', 'NotAllowedError'))
  setClipboard({ writeText })
  let observed = ''
  const execCommand = setExecCommand(() => {
    observed = document.body.querySelector('textarea')?.value ?? ''
    return true
  })
  const button = renderCopyButton(command)

  await click(button)

  expect(writeText).toHaveBeenCalledWith(command)
  expect(execCommand).toHaveBeenCalledWith('copy')
  expect(observed).toBe(command)
  expect(document.body.querySelector('textarea')).toBeNull()
  expect(button.textContent).toContain('已复制')
  expect(host.querySelector('[role="status"]')).toBeNull()
})

it('reports a failed fallback honestly and restores focus and selection after cleanup', async () => {
  const command = 'command that could not be copied'
  const previousFocus = document.createElement('input')
  previousFocus.value = 'keep selection'
  document.body.appendChild(previousFocus)
  previousFocus.focus()
  previousFocus.setSelectionRange(1, 5)
  const execCommand = setExecCommand(() => false)
  const button = renderCopyButton(command)
  button.focus()

  await click(button)

  expect(execCommand).toHaveBeenCalledWith('copy')
  expect(document.body.querySelector('textarea')).toBeNull()
  expect(document.activeElement).toBe(button)
  expect(previousFocus.selectionStart).toBe(1)
  expect(previousFocus.selectionEnd).toBe(5)
  expect(button.textContent).toContain('复制注册命令')
  expect(button.textContent).not.toContain('已复制')
  expect(host.querySelector('[role="status"]')?.textContent).toContain('复制失败')
  previousFocus.remove()
})

import type { ReactNode } from 'react'
import { createContext, useContext } from 'react'

export interface YamlDraftPolicy {
  scope?: string
  readOnly?: boolean
}

const YamlDraftPolicyContext = createContext<YamlDraftPolicy>({})

export function YamlDraftPolicyProvider({
  scope,
  readOnly = false,
  children,
}: YamlDraftPolicy & { children: ReactNode }) {
  return (
    <YamlDraftPolicyContext.Provider value={{ scope, readOnly }}>
      {children}
    </YamlDraftPolicyContext.Provider>
  )
}

export function useYamlDraftPolicy(): YamlDraftPolicy {
  return useContext(YamlDraftPolicyContext)
}

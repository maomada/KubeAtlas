import { TimelineSwimlanes as TimelineSwimlanesUI, type TimelineSwimlanesProps } from '@maomada/k8s-ui'
import { useNavigate } from 'react-router-dom'
import { useHasLimitedAccess } from '../../contexts/CapabilitiesContext'

// Thin kubeatlas/web host wrapper over the shared k8s-ui TimelineSwimlanes: injects
// the RBAC capability flag (kubeatlas's CapabilitiesContext) and router-based
// navigation for GitOps lane labels. The presentational component lives in
// @maomada/k8s-ui so KubeAtlas Hub can reuse it fed by its own (tunnel) data.
export function TimelineSwimlanes(props: Omit<TimelineSwimlanesProps, 'hasLimitedAccess' | 'onNavigatePath'>) {
  const navigate = useNavigate()
  const hasLimitedAccess = useHasLimitedAccess()
  return <TimelineSwimlanesUI {...props} hasLimitedAccess={hasLimitedAccess} onNavigatePath={navigate} />
}

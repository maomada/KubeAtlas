import type { Translate } from '../../hooks/useLocale';

const preferChinese: Translate = zh => zh;

// Shared labels for rendering an issue's DiagnosticContext (the causal-link
// surface). Used by both the cluster Issues queue (IssuesView) and the
// per-resource Operational Issues block (ResourceIssuesSection) so the two
// can't drift on wording.

// Operator-facing — describes the issue's place in the causal picture in plain
// language, not KubeAtlas's internal role taxonomy.
export function diagnosticRoleLabel(role: string, t: Translate = preferChinese): string {
  switch (role) {
    case 'candidate':
      return t('可能原因', 'Possible cause');
    case 'affected':
      return t('受影响对象', 'Affected object');
    case 'rollup':
      return t('汇总', 'Summary');
    default:
      return t('上下文', 'Context');
  }
}

// Operator-facing fact labels — plain language over the implementation-shaped
// internal type names.
export function diagnosticFactLabel(type: string, t: Translate = preferChinese): string {
  switch (type) {
    case 'explicit_reference':
      return t('缺少引用', 'Missing reference');
    case 'node_startup_corroboration':
      return t('同节点故障', 'Same-node failure');
    case 'owner_rollup':
      return t('由 Pod 汇总', 'Grouped from Pods');
    case 'selected_backend_issue':
      return t('后端 Pod', 'Backend Pod');
    case 'service_config_mismatch':
      return t('Service 配置', 'Service configuration');
    case 'service_env_reference':
      return t('被引用的服务', 'Referenced Service');
    case 'probe_target_mismatch':
      return t('探针目标', 'Probe target');
    case 'blocked_init_container':
      return t('初始化容器', 'Init container');
    case 'restart_cause':
      return t('重启证据', 'Restart evidence');
    case 'pod_template_divergence':
      return t('Pod/模板差异', 'Pod/template differences');
    case 'node_blast_radius':
      return t('受影响的工作负载', 'Affected workloads');
    case 'pvc_blast_radius':
      return t('受阻的 Pod', 'Blocked Pods');
    case 'apiservice_hpa':
      return t('停滞的自动扩缩器', 'Stalled autoscaler');
    case 'secret_not_ready':
      return t('依赖此项的 Pod', 'Dependent Pods');
    case 'admission_webhook_backend':
      return t('准入依赖项', 'Admission dependency');
    case 'network_policy_isolates':
      return t('受影响的资源', 'Affected resources');
    default:
      return type.replace(/_/g, ' ');
  }
}

// Operator-facing lead-in for the symptom→root pointer chip. Honest per fact
// type: a declared PVC/Secret edge is a cause; a co-located node is only
// "related" (node pressure can be a shared victim, not the root), so it must not
// claim "caused by".
export function incidentParentLabel(factType?: string, confidence?: string, t: Translate = preferChinese): string {
  switch (factType) {
    case 'pvc_blast_radius':
    case 'secret_not_ready':
    case 'admission_webhook_backend':
      return t('原因', 'Cause');
    case 'apiservice_hpa':
      return t('可能原因', 'Possible cause');
    case 'node_blast_radius':
      return t('相关', 'Related');
    default:
      return confidence === 'high' ? t('原因', 'Cause') : t('可能原因', 'Possible cause');
  }
}

// Plain-language gloss for the confidence chip's tooltip — the operator should
// know a medium link is "these are co-located, the node may be the cause", not a
// proven fact.
export function confidenceTitle(confidence: string, t: Translate = preferChinese): string {
  switch (confidence) {
    case 'high':
      return t('置信度高：存在明确的结构关联（selector、owner 或 claim 引用）。', 'High confidence: an explicit selector, owner, or claim reference connects these resources.');
    case 'medium':
      return t('置信度中等：这些资源有关联，但因果关系是推断所得；采取行动前请先核实。', 'Medium confidence: the resources are related. Verify the inferred cause before acting.');
    case 'low':
      return t('置信度低：这是根据启发式规则匹配的结果。', 'Low confidence: this match is based on a heuristic.');
    default:
      return '';
  }
}

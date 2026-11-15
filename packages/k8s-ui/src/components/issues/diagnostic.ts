// Shared labels for rendering an issue's DiagnosticContext (the causal-link
// surface). Used by both the cluster Issues queue (IssuesView) and the
// per-resource Operational Issues block (ResourceIssuesSection) so the two
// can't drift on wording.

// Operator-facing — describes the issue's place in the causal picture in plain
// language, not Radar's internal role taxonomy.
export function diagnosticRoleLabel(role: string): string {
  switch (role) {
    case 'candidate':
      return '可能原因';
    case 'affected':
      return '受影响对象';
    case 'rollup':
      return '汇总';
    default:
      return '上下文';
  }
}

// Operator-facing fact labels — plain language over the implementation-shaped
// internal type names.
export function diagnosticFactLabel(type: string): string {
  switch (type) {
    case 'explicit_reference':
      return '缺少引用';
    case 'node_startup_corroboration':
      return '同节点故障';
    case 'owner_rollup':
      return '由 Pod 汇总';
    case 'selected_backend_issue':
      return '后端 Pod';
    case 'service_config_mismatch':
      return 'Service 配置';
    case 'service_env_reference':
      return '被引用的服务';
    case 'probe_target_mismatch':
      return '探针目标';
    case 'blocked_init_container':
      return '初始化容器';
    case 'restart_cause':
      return '重启证据';
    case 'pod_template_divergence':
      return 'Pod/模板差异';
    case 'node_blast_radius':
      return '受影响的工作负载';
    case 'pvc_blast_radius':
      return '受阻的 Pod';
    case 'apiservice_hpa':
      return '停滞的自动扩缩器';
    case 'secret_not_ready':
      return '依赖此项的 Pod';
    case 'admission_webhook_backend':
      return '准入依赖项';
    case 'network_policy_isolates':
      return '受影响的资源';
    default:
      return type.replace(/_/g, ' ');
  }
}

// Operator-facing lead-in for the symptom→root pointer chip. Honest per fact
// type: a declared PVC/Secret edge is a cause; a co-located node is only
// "related" (node pressure can be a shared victim, not the root), so it must not
// claim "caused by".
export function incidentParentLabel(factType?: string, confidence?: string): string {
  switch (factType) {
    case 'pvc_blast_radius':
    case 'secret_not_ready':
    case 'admission_webhook_backend':
      return '原因';
    case 'apiservice_hpa':
      return '可能原因';
    case 'node_blast_radius':
      return '相关';
    default:
      return confidence === 'high' ? '原因' : '可能原因';
  }
}

// Plain-language gloss for the confidence chip's tooltip — the operator should
// know a medium link is "these are co-located, the node may be the cause", not a
// proven fact.
export function confidenceTitle(confidence: string): string {
  switch (confidence) {
    case 'high':
      return '置信度高：存在明确的结构关联（selector、owner 或 claim 引用）。';
    case 'medium':
      return '置信度中等：这些资源有关联，但因果关系是推断所得；采取行动前请先核实。';
    case 'low':
      return '置信度低：这是根据启发式规则匹配的结果。';
    default:
      return '';
  }
}

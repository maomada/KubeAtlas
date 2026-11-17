{{/*
Expand the name of the chart.
*/}}
{{- define "kubeatlas.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kubeatlas.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kubeatlas.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kubeatlas.labels" -}}
helm.sh/chart: {{ include "kubeatlas.chart" . }}
{{ include "kubeatlas.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubeatlas.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubeatlas.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kubeatlas.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kubeatlas.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The URL path prefix KubeAtlas serves under: "" for root, otherwise "/<prefix>"
with no trailing slash. Shared by the Deployment (--base-path, probes) and
NOTES.txt (the URL printed after install).
*/}}
{{- define "kubeatlas.basePath" -}}
{{- $trimmed := trimAll "/" (default "" .Values.basePath) -}}
{{- if $trimmed -}}
{{- printf "/%s" $trimmed -}}
{{- end -}}
{{- end }}

{{- define "kubeatlas.operatorSettings" -}}
{{- $settings := dict "version" 1 -}}
{{- if .Values.audit -}}
{{- $_ := set $settings "audit" .Values.audit -}}
{{- end -}}
{{- if .Values.helm.ociSources -}}
{{- $_ := set $settings "helmOciSources" .Values.helm.ociSources -}}
{{- end -}}
{{- $settings | toPrettyJson -}}
{{- end -}}

{{/*
Whether the default read grant for KubeAtlas Cloud's background services
(kubeatlas:system) renders. It includes cluster-wide Secret read, so an absent
value means OFF: a `--reuse-values` upgrade from a release that predates the
key renders with the previous release's tree and never gains Secret read
without someone choosing it. Fresh installs, plain upgrades, GitOps and
`--reset-then-reuse-values` get true from values.yaml.
*/}}
{{- define "kubeatlas.cloudSystemRbac" -}}
{{- if and .Values.cloud.enabled (eq (toString .Values.cloud.systemRbac) "true") -}}
true
{{- else -}}
false
{{- end -}}
{{- end -}}

{{/*
Whether the default read grant for automatic Diagnose (kubeatlas:ai:reader) renders.
An absent value means OFF, as with kubeatlas.cloudSystemRbac: a
`--reuse-values` upgrade from a release that predates the key never gains a
new reader without someone choosing it.
*/}}
{{- define "kubeatlas.cloudAiRbac" -}}
{{- if and .Values.cloud.enabled (eq (toString .Values.cloud.aiRbac) "true") -}}
true
{{- else -}}
false
{{- end -}}
{{- end -}}

{{/*
Whether KubeAtlas calls Kubernetes with each person's own token instead of
impersonating them (auth.kubernetesCredentials=user-token).
*/}}
{{- define "kubeatlas.userTokenMode" -}}
{{- if eq (.Values.auth.kubernetesCredentials | default "impersonate") "user-token" -}}
true
{{- else -}}
false
{{- end -}}
{{- end -}}

{{/*
Enrollment mode: KubeAtlas joins cloud.hubURL with a shared enrollment key every
time it starts and holds the token it gets in memory. A configured cloud.token or
cloud.existingSecret wins, so a token install renders exactly as before.
*/}}
{{- define "kubeatlas.cloudEnrollmentMode" -}}
{{- $c := .Values.cloud -}}
{{- $e := $c.enrollment | default dict -}}
{{- if and $c.enabled $c.hubURL (or $e.key $e.existingSecret) (not (or $c.token $c.existingSecret)) -}}
true
{{- else -}}
false
{{- end -}}
{{- end -}}

{{- define "kubeatlas.cloudEnrollmentKeySecretName" -}}
{{- $e := .Values.cloud.enrollment | default dict -}}
{{- $e.existingSecret | default (printf "%s-cloud-enrollment-key" (include "kubeatlas.fullname" .)) -}}
{{- end -}}

{{/* Deployed Cloud identities remain valid while Hub, Agent and chart upgrade independently. */}}
{{- define "kubeatlas.legacyCloudGroup" -}}
{{- $role := . -}}
{{- if eq $role "ai" -}}{{- $role = "ai:reader" -}}{{- end -}}
{{- printf "radar:%s" $role -}}
{{- end -}}

{{/* fullname helper */}}
{{- define "iceberg-sentry.fullname" -}}
{{- printf "%s" (default .Chart.Name .Values.nameOverride) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "iceberg-sentry.labels" -}}
app.kubernetes.io/name: {{ include "iceberg-sentry.fullname" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "iceberg-sentry.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{ include "iceberg-sentry.fullname" . }}
{{- else -}}
default
{{- end -}}
{{- end -}}

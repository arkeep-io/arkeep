{{/*
Expand the name of the chart.
*/}}
{{- define "arkeep.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
Truncated at 63 characters because Kubernetes DNS naming spec requires it.
*/}}
{{- define "arkeep.fullname" -}}
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
Create chart label value used by selectors.
*/}}
{{- define "arkeep.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to all resources.
*/}}
{{- define "arkeep.labels" -}}
helm.sh/chart: {{ include "arkeep.chart" . }}
{{ include "arkeep.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels — used in Deployment spec.selector and Service spec.selector.
These must never change after first deploy (they are immutable on Deployments).
*/}}
{{- define "arkeep.selectorLabels" -}}
app.kubernetes.io/name: {{ include "arkeep.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "arkeep.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "arkeep.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image tag — defaults to chart appVersion.
*/}}
{{- define "arkeep.imageTag" -}}
{{- .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
Secret name — either the existing secret or the chart-managed one.
*/}}
{{- define "arkeep.secretName" -}}
{{- if .Values.secret.existingSecret }}
{{- .Values.secret.existingSecret }}
{{- else }}
{{- include "arkeep.fullname" . }}
{{- end }}
{{- end }}

{{/*
PostgreSQL DSN — built from subchart or external config.
*/}}
{{- define "arkeep.postgresDSN" -}}
{{- if .Values.externalPostgresql.url }}
{{- .Values.externalPostgresql.url }}
{{- else if .Values.postgresql.enabled }}
{{- $host := printf "%s-postgresql" .Release.Name }}
{{- printf "postgres://%s:%s@%s:5432/%s?sslmode=disable"
    .Values.postgresql.auth.username
    .Values.postgresql.auth.password
    $host
    .Values.postgresql.auth.database }}
{{- else }}
{{- printf "postgres://%s:%s@%s:%d/%s?sslmode=%s"
    .Values.externalPostgresql.username
    .Values.externalPostgresql.password
    .Values.externalPostgresql.host
    (.Values.externalPostgresql.port | int)
    .Values.externalPostgresql.database
    .Values.externalPostgresql.sslMode }}
{{- end }}
{{- end }}

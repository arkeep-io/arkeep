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
Bundled PostgreSQL — name of its StatefulSet, Service and Secret.
*/}}
{{- define "arkeep.postgresql.fullname" -}}
{{- printf "%s-postgresql" (include "arkeep.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Bundled PostgreSQL selector labels. The name differs from the server's so the
server Service never selects the database pod.
*/}}
{{- define "arkeep.postgresql.selectorLabels" -}}
app.kubernetes.io/name: {{ printf "%s-postgresql" (include "arkeep.name" .) | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Bundled PostgreSQL labels.
*/}}
{{- define "arkeep.postgresql.labels" -}}
helm.sh/chart: {{ include "arkeep.chart" . }}
{{ include "arkeep.postgresql.selectorLabels" . }}
app.kubernetes.io/component: database
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
PostgreSQL DSN — built from the bundled database or external config.
The password is required in all cases to prevent a silent misconfiguration
where an empty password produces an invalid DSN.
*/}}
{{- define "arkeep.postgresDSN" -}}
{{- if .Values.externalPostgresql.url }}
{{- .Values.externalPostgresql.url }}
{{- else if .Values.postgresql.enabled }}
{{- $host := include "arkeep.postgresql.fullname" . }}
{{- $pass := required "postgresql.auth.password is required when postgresql.enabled=true" .Values.postgresql.auth.password }}
{{- printf "postgres://%s:%s@%s:5432/%s?sslmode=disable"
    .Values.postgresql.auth.username
    $pass
    $host
    .Values.postgresql.auth.database }}
{{- else }}
{{- $pass := required "externalPostgresql.password is required when using external PostgreSQL without a full DSN (externalPostgresql.url)" .Values.externalPostgresql.password }}
{{- printf "postgres://%s:%s@%s:%d/%s?sslmode=%s"
    .Values.externalPostgresql.username
    $pass
    .Values.externalPostgresql.host
    (.Values.externalPostgresql.port | int)
    .Values.externalPostgresql.database
    .Values.externalPostgresql.sslMode }}
{{- end }}
{{- end }}

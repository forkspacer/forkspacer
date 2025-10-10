{{/*
Expand the name of the chart.
*/}}
{{- define "operator-ui.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "operator-ui.fullname" -}}
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
{{- define "operator-ui.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "operator-ui.labels" -}}
helm.sh/chart: {{ include "operator-ui.chart" . }}
{{ include "operator-ui.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "operator-ui.selectorLabels" -}}
app.kubernetes.io/name: {{ include "operator-ui.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: frontend
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "operator-ui.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "operator-ui.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Get the API server URL
*/}}
{{- define "operator-ui.apiUrl" -}}
{{- if .Values.env.REACT_APP_API_BASE_URL }}
{{- .Values.env.REACT_APP_API_BASE_URL }}
{{- else }}
{{- printf "http://%s-api-server.%s.svc.cluster.local:8080" .Release.Name (.Values.global.namespace | default .Release.Namespace) }}
{{- end }}
{{- end }}
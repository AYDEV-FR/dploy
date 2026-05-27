{{/*
Expand the name of the chart.
*/}}
{{- define "dploy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "dploy.fullname" -}}
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
{{- define "dploy.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "dploy.labels" -}}
helm.sh/chart: {{ include "dploy.chart" . }}
{{ include "dploy.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "dploy.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dploy.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "dploy.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "dploy.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Namespace where DployTemplate/DployInstance CRs live (defaults to the release namespace).
*/}}
{{- define "dploy.namespace" -}}
{{- default .Release.Namespace .Values.config.namespace }}
{{- end }}

{{/*
Operator names, labels and service account.
*/}}
{{- define "dploy.operator.fullname" -}}
{{- printf "%s-operator" (include "dploy.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "dploy.operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dploy.name" . }}-operator
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "dploy.operator.labels" -}}
helm.sh/chart: {{ include "dploy.chart" . }}
{{ include "dploy.operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: operator
{{- end }}

{{- define "dploy.operator.serviceAccountName" -}}
{{- if .Values.operator.serviceAccount.create }}
{{- default (include "dploy.operator.fullname" .) .Values.operator.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.operator.serviceAccount.name }}
{{- end }}
{{- end }}

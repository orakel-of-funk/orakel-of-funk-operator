{{/*
Expand the name of the chart.
*/}}
{{- define "orakel-of-funk.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "orakel-of-funk.fullname" -}}
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

{{- define "orakel-of-funk.namespace" -}}
{{- if .Values.namespaceOverride }}
{{- .Values.namespaceOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- include "orakel-of-funk.fullname" . }}
{{- end }}
{{- end }}

{{/*
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "orakel-of-funk.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "orakel-of-funk.labels" -}}
helm.sh/chart: {{ include "orakel-of-funk.chart" . }}
{{ include "orakel-of-funk.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "orakel-of-funk.selectorLabels" -}}
app.kubernetes.io/name: {{ include "orakel-of-funk.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "orakel-of-funk.serviceAccountName" -}}
{{- if .Values.orakel.serviceAccount.create }}
{{- default (include "orakel-of-funk.fullname" .) .Values.orakel.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.orakel.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Valkey labels
*/}}
{{- define "orakel-of-funk.valkey.labels" -}}
helm.sh/chart: {{ include "orakel-of-funk.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/name: valkey
{{- end }}

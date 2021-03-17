{{/*
Expand the name of the chart.
*/}}
{{- define "gubernator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "gubernator.fullname" -}}
{{- if .Values.gubernator.fullnameOverride }}
{{- .Values.gubernator.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gubernator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Gubernator Annotations
*/}}
{{- define "gubernator.annotations" -}}
meta.helm.sh/release-name: {{ .Release.Name }}
meta.helm.sh/release-namespace: {{ .Release.Namespace }}
{{- with .Values.gubernator.annotations }}
{{- toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gubernator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gubernator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gubernator.labels" -}}
{{- with .Values.gubernator.labels }}
{{- toYaml . }}
{{- end }}
helm.sh/chart: {{ include "gubernator.chart" . }}
{{ include "gubernator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
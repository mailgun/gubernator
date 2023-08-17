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
app: gubernator
helm.sh/chart: {{ include "gubernator.chart" . }}
{{ include "gubernator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
GRPC Port
*/}}
{{- define "gubernator.grpc.port" -}}
{{- if .Values.gubernator.server.grpc.port }}
{{- .Values.gubernator.server.grpc.port}}
{{- else }}
{{- print "81" }}
{{- end }}
{{- end }}

{{/*
HTTP Port
*/}}
{{- define "gubernator.http.port" -}}
{{- if .Values.gubernator.server.http.port }}
{{- .Values.gubernator.server.http.port}}
{{- else }}
{{- print "80" }}
{{- end }}
{{- end }}

{{/*Gubernator env*/}}
{{- define "gubernator.env"}}
- name: GUBER_K8S_NAMESPACE
  valueFrom:
    fieldRef:
      fieldPath: metadata.namespace
- name: GUBER_K8S_POD_IP
  valueFrom:
    fieldRef:
      fieldPath: status.podIP
- name: GUBER_GRPC_ADDRESS
  value: "0.0.0.0:{{ include "gubernator.grpc.port" . }}"
- name: GUBER_HTTP_ADDRESS
  value: "0.0.0.0:{{ include "gubernator.http.port" . }}"
- name: GUBER_PEER_DISCOVERY_TYPE
  value: "k8s"
- name: GUBER_K8S_POD_PORT
  value: "{{ include "gubernator.grpc.port" . }}"
- name: GUBER_K8S_ENDPOINTS_SELECTOR
  value: "app=gubernator"
{{- if .Values.gubernator.debug }}
- name: GUBER_DEBUG
  value: "true"
{{- end }}
- name: GUBER_K8S_WATCH_MECHANISM
{{- if .Values.gubernator.watchPods }}
  value: "pods"
{{- else }}
  value: "endpoints"
{{- end }}
{{- if .Values.gubernator.server.grpc.maxConnAgeSeconds }}
- name: GUBER_GRPC_MAX_CONN_AGE_SEC
  value: {{ .Values.gubernator.server.grpc.maxConnAgeSeconds }}
{{- end }}
{{- end }}

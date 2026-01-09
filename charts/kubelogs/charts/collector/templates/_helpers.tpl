{{/*
Expand the name of the chart.
*/}}
{{- define "collector.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "collector.fullname" -}}
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
{{- define "collector.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "collector.labels" -}}
helm.sh/chart: {{ include "collector.chart" . }}
{{ include "collector.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: collector
app.kubernetes.io/part-of: kubelogs
{{- end }}

{{/*
Selector labels
*/}}
{{- define "collector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "collector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name
*/}}
{{- define "collector.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "collector.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image name construction
*/}}
{{- define "collector.image" -}}
{{- $registry := .Values.global.imageRegistry | default "ghcr.io" }}
{{- $owner := .Values.global.imageOwner | default "kubelogs" }}
{{- $repository := .Values.image.repository | default (printf "%s/%s/kubelogs-collector" $registry $owner) }}
{{- $tag := .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" $repository $tag }}
{{- end }}

{{/*
Storage address - defaults to server service if not specified
*/}}
{{- define "collector.storageAddr" -}}
{{- if .Values.storage.remoteAddr }}
{{- .Values.storage.remoteAddr }}
{{- else }}
{{- printf "%s-server:50051" .Release.Name }}
{{- end }}
{{- end }}

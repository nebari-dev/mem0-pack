{{- define "nebari-mem0.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "nebari-mem0.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "nebari-mem0.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "nebari-mem0.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "nebari-mem0.adminKeySecret" -}}
{{- default .Values.adminKey.secretName .Values.adminKey.existingSecret -}}
{{- end -}}

{{- define "nebari-mem0.dbSecret" -}}
{{- if .Values.postgres.bundled.enabled -}}
{{- printf "%s-db-app" (include "nebari-mem0.fullname" .) -}}
{{- else -}}
{{- required "postgres.external.existingSecret is required when postgres.bundled.enabled is false" .Values.postgres.external.existingSecret -}}
{{- end -}}
{{- end -}}

{{/* Image reference: repository@digest when a digest is set, else repository:tag. Takes a dict with "image" (values block). */}}
{{- define "nebari-mem0.imageRef" -}}
{{- if .digest -}}
{{- printf "%s@%s" .repository .digest -}}
{{- else if .tag -}}
{{- printf "%s:%s" .repository .tag -}}
{{- else -}}
{{- fail (printf "set a tag or digest for image %s" .repository) -}}
{{- end -}}
{{- end -}}

{{- define "nebari-mem0.image" -}}
{{- include "nebari-mem0.imageRef" .Values.image -}}
{{- end -}}

{{- define "nebari-mem0.toolsImage" -}}
{{- include "nebari-mem0.imageRef" .Values.tools.image -}}
{{- end -}}

{{- define "nebari-mem0.containerSecurityContext" -}}
runAsNonRoot: true
runAsUser: {{ . }}
readOnlyRootFilesystem: true
allowPrivilegeEscalation: false
capabilities:
  drop: ["ALL"]
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "nebari-mem0.dbEnv" -}}
{{- $secret := include "nebari-mem0.dbSecret" . -}}
- name: POSTGRES_HOST
  valueFrom: {secretKeyRef: {name: {{ $secret }}, key: host}}
- name: POSTGRES_PORT
  valueFrom: {secretKeyRef: {name: {{ $secret }}, key: port}}
- name: POSTGRES_DB
  valueFrom: {secretKeyRef: {name: {{ $secret }}, key: dbname}}
- name: POSTGRES_USER
  valueFrom: {secretKeyRef: {name: {{ $secret }}, key: username}}
- name: POSTGRES_PASSWORD
  valueFrom: {secretKeyRef: {name: {{ $secret }}, key: password}}
- name: APP_DB_NAME
  valueFrom: {secretKeyRef: {name: {{ $secret }}, key: dbname}}
{{- end -}}

{{- define "nebari-mem0.modelEnv" -}}
- name: MEM0PACK_MODEL_BASE_URL
  value: http://127.0.0.1:8081/v1
- name: MEM0PACK_LLM_MODEL
  value: {{ .Values.models.llm | quote }}
- name: MEM0PACK_EMBEDDING_MODEL
  value: {{ .Values.models.embedding.model | quote }}
- name: MEM0PACK_EMBEDDING_DIMS
  value: {{ .Values.models.embedding.dimensions | toString | quote }}
{{- end -}}

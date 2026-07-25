{{- define "timadorus-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "timadorus-platform.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "timadorus-platform.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "timadorus-platform.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "timadorus-platform.selectorLabels" -}}
app.kubernetes.io/name: {{ include "timadorus-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
NATS connection URL: the bundled subchart's Service (named "<release-name>-nats", confirmed
via `helm template` against nats/nats 2.14.2 during design) when nats.enabled, else the
externally supplied URL.
*/}}
{{- define "timadorus-platform.natsURL" -}}
{{- if .Values.nats.enabled -}}
nats://{{ .Release.Name }}-nats:4222
{{- else -}}
{{- .Values.nats.externalURL -}}
{{- end -}}
{{- end -}}

{{/*
DATABASE_URL env entry, sourced from an existingSecret. Used by every Deployment and the
migration Job — postgres.existingSecret is required.
*/}}
{{- define "timadorus-platform.databaseURLEnv" -}}
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ required "postgres.existingSecret is required" .Values.postgres.existingSecret }}
      key: {{ .Values.postgres.secretKey | default "DATABASE_URL" }}
{{- end -}}

{{/*
JWT env entries for command-api/query-api, matching internal/config.JWT's field set. Mode
"hmac" sets JWT_HMAC_KEY_ID + JWT_HMAC_SECRET (from an existingSecret); mode "jwks" sets
JWT_JWKS_URL instead. JWT_ISSUER/JWT_AUDIENCE are always set (empty string if unconfigured,
matching internal/config's os.Getenv default of "").
*/}}
{{- define "timadorus-platform.jwtEnv" -}}
- name: JWT_ISSUER
  value: {{ .Values.jwt.issuer | quote }}
- name: JWT_AUDIENCE
  value: {{ .Values.jwt.audience | quote }}
{{- if eq .Values.jwt.mode "hmac" }}
- name: JWT_HMAC_KEY_ID
  value: {{ .Values.jwt.hmac.keyID | quote }}
- name: JWT_HMAC_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ required "jwt.hmac.existingSecret is required when jwt.mode is hmac" .Values.jwt.hmac.existingSecret }}
      key: {{ .Values.jwt.hmac.secretKey | default "JWT_HMAC_SECRET" }}
{{- else }}
- name: JWT_JWKS_URL
  value: {{ required "jwt.jwksURL is required when jwt.mode is jwks" .Values.jwt.jwksURL | quote }}
{{- end }}
{{- end -}}

{{/*
Gateway API parentRefs shared by both HTTPRoutes: the chart's own Gateway when
gateway.create is true, or the externally supplied gateway.existing.* coordinates when false.
*/}}
{{- define "timadorus-platform.gatewayParentRefs" -}}
{{- if .Values.gateway.create }}
parentRefs:
  - name: {{ include "timadorus-platform.fullname" . }}
{{- else }}
parentRefs:
  - name: {{ required "gateway.existing.name is required when gateway.create is false" .Values.gateway.existing.name }}
    {{- if .Values.gateway.existing.namespace }}
    namespace: {{ .Values.gateway.existing.namespace }}
    {{- end }}
    {{- if .Values.gateway.existing.sectionName }}
    sectionName: {{ .Values.gateway.existing.sectionName }}
    {{- end }}
{{- end }}
{{- end -}}

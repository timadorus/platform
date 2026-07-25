# Timadorus Platform Helm Chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Helm chart at `deploy/helm/timadorus-platform` that deploys command-api,
query-api, and projector to Kubernetes, running schema migrations automatically and exposing
command-api/query-api via the Gateway API.

**Architecture:** A single application chart with one Deployment+Service per binary (flat
`templates/` directory, no per-service subfolders), a `pre-install,pre-upgrade` hook Job that
runs the platform's existing `golang-migrate` migrations via a new shared script and a new
`Dockerfile.migrate` image, and Gateway API `HTTPRoute` resources for command-api/query-api
that attach either to a `Gateway` the chart creates or to an existing one, selected via values.
Postgres is always external (`existingSecret` reference only); NATS JetStream is an optional
subchart dependency (official `nats-io/k8s` chart) or an external URL.

**Tech Stack:** Helm 3 (`apiVersion: v2` chart), Kubernetes Gateway API v1
(`gateway.networking.k8s.io/v1`), `golang-migrate/migrate/v4` v4.19.1, Go 1.26 (matches the
three existing `Dockerfile.*` images), bash (migration script).

## Global Constraints

- Chart lives at `deploy/helm/timadorus-platform`; all templates are flat files directly under
  `templates/` (no per-service subdirectories) — this was an explicit correction to the design.
- The chart never templates a `Secret` containing a real credential. `DATABASE_URL` and the JWT
  HMAC secret (if used) are always referenced via `existingSecret` values.
- `golang-migrate` stays pinned at `v4.19.1` (matches the existing `Makefile`'s `$(MIGRATE)`
  pin) with the `postgres` build tag.
- NATS subchart dependency pinned at chart version `2.14.2` from
  `https://nats-io.github.io/k8s/helm/charts/` (verified available during design via
  `helm search repo`).
- Gateway API resources target `apiVersion: gateway.networking.k8s.io/v1` (GA since v1.0; CRDs
  for end-to-end testing pinned at `v1.6.1`, the latest release verified during design).
- The three existing binaries' env-var contract (`internal/config/config.go`) is the source of
  truth for every env var name set in Deployment templates — do not invent new env var names.
- Every task that changes `Makefile`, `scripts/`, or a `Dockerfile.*` must leave
  `make migrate-up`/`make migrate-down` working unchanged for local dev (regression
  requirement from the spec).

---

### Task 1: Chart scaffold — `Chart.yaml`, `values.yaml`, `.helmignore`

**Files:**
- Create: `deploy/helm/timadorus-platform/Chart.yaml`
- Create: `deploy/helm/timadorus-platform/values.yaml`
- Create: `deploy/helm/timadorus-platform/.helmignore`
- Modify: `.gitignore`

**Interfaces:**
- Produces: the full values schema every later template task reads from — field names below
  are load-bearing for Tasks 2-8; do not rename them without updating this file's note.

- [ ] **Step 1: Create the chart directories**

```bash
mkdir -p deploy/helm/timadorus-platform/templates
```

- [ ] **Step 2: Write `Chart.yaml`**

```yaml
apiVersion: v2
name: timadorus-platform
description: Helm chart for the Timadorus CQRS/ES platform (command-api, query-api, projector)
type: application
version: 0.1.0
appVersion: "0.1.0"

dependencies:
  - name: nats
    version: "2.14.2"
    repository: "https://nats-io.github.io/k8s/helm/charts/"
    condition: nats.enabled
```

- [ ] **Step 3: Write `values.yaml`**

```yaml
# Default values for timadorus-platform.

commandApi:
  image:
    repository: timadorus/command-api
    tag: ""
    pullPolicy: IfNotPresent
  replicas: 1
  containerPort: 8081
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi
  route:
    hostname: ""

queryApi:
  image:
    repository: timadorus/query-api
    tag: ""
    pullPolicy: IfNotPresent
  replicas: 1
  containerPort: 8082
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi
  route:
    hostname: ""

projector:
  image:
    repository: timadorus/projector
    tag: ""
    pullPolicy: IfNotPresent
  replicas: 1
  containerPort: 8083
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi

migration:
  image:
    repository: timadorus/migrate
    tag: ""
    pullPolicy: IfNotPresent
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 200m
      memory: 128Mi

postgres:
  # Name of a pre-existing Secret in the release namespace holding a full DATABASE_URL
  # (e.g. postgres://user:pass@host:5432/db?sslmode=...). Required — the chart never
  # templates this Secret itself.
  existingSecret: ""
  secretKey: "DATABASE_URL"

nats:
  # true: deploy NATS JetStream as a subchart dependency of this release.
  # false: use nats.externalURL instead.
  enabled: true
  externalURL: ""
  config:
    jetstream:
      enabled: true

jwt:
  # "jwks" or "hmac" — selects which of the two blocks below is used.
  mode: jwks
  jwksURL: ""
  issuer: ""
  audience: ""
  hmac:
    existingSecret: ""
    secretKey: "JWT_HMAC_SECRET"
    keyID: "dev"

gateway:
  # true: chart creates its own Gateway (gatewayClassName required below).
  # false: attach both HTTPRoutes to an existing Gateway (gateway.existing.* required below).
  create: true
  gatewayClassName: ""
  tls:
    enabled: false
    secretName: ""
  existing:
    name: ""
    namespace: ""
    sectionName: ""
```

- [ ] **Step 4: Write `.helmignore`**

```
.git/
*.swp
*.bak
*.orig
*~
.project
.idea/
*.tmproj
.vscode/
```

- [ ] **Step 5: Ignore fetched subchart archives in git**

Add a line to the repo-root `.gitignore` (currently just `bin/`):

```
bin/
deploy/helm/timadorus-platform/charts/
```

- [ ] **Step 6: Fetch the `nats` dependency and verify the chart lints**

Run:
```bash
helm dependency update deploy/helm/timadorus-platform
helm lint deploy/helm/timadorus-platform
```
Expected: `Saving 1 charts` / `Deleting outdated charts` from the first command, and
`deploy/helm/timadorus-platform/Chart.lock` + `deploy/helm/timadorus-platform/charts/nats-2.14.2.tgz`
now exist. `helm lint` prints `0 chart(s) linted, 0 chart(s) failed` (no templates exist yet,
so there's nothing to fail).

- [ ] **Step 7: Commit**

```bash
git add deploy/helm/timadorus-platform/Chart.yaml deploy/helm/timadorus-platform/Chart.lock \
  deploy/helm/timadorus-platform/values.yaml deploy/helm/timadorus-platform/.helmignore \
  .gitignore
git commit -m "helm: scaffold timadorus-platform chart (Chart.yaml, values.yaml)"
```

---

### Task 2: `_helpers.tpl` + projector Deployment/Service

**Files:**
- Create: `deploy/helm/timadorus-platform/templates/_helpers.tpl`
- Create: `deploy/helm/timadorus-platform/templates/projector-deployment.yaml`
- Create: `deploy/helm/timadorus-platform/templates/projector-service.yaml`

**Interfaces:**
- Consumes: `values.yaml` fields from Task 1 (`postgres.*`, `nats.*`, `projector.*`).
- Produces: named templates every later task calls by these exact names —
  `timadorus-platform.name`, `timadorus-platform.fullname`, `timadorus-platform.labels`,
  `timadorus-platform.selectorLabels`, `timadorus-platform.natsURL`,
  `timadorus-platform.databaseURLEnv`, `timadorus-platform.jwtEnv`,
  `timadorus-platform.gatewayParentRefs`.

- [ ] **Step 1: Write `_helpers.tpl`**

```
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
```

- [ ] **Step 2: Write `projector-deployment.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-projector
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: projector
spec:
  replicas: {{ .Values.projector.replicas }}
  selector:
    matchLabels:
      {{- include "timadorus-platform.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: projector
  template:
    metadata:
      labels:
        {{- include "timadorus-platform.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: projector
    spec:
      containers:
        - name: projector
          image: "{{ .Values.projector.image.repository }}:{{ .Values.projector.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.projector.image.pullPolicy }}
          ports:
            - name: http
              containerPort: {{ .Values.projector.containerPort }}
          env:
            - name: PROJECTOR_ADDR
              value: ":{{ .Values.projector.containerPort }}"
            {{- include "timadorus-platform.databaseURLEnv" . | nindent 12 }}
            - name: NATS_URL
              value: {{ include "timadorus-platform.natsURL" . | quote }}
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 15
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            {{- toYaml .Values.projector.resources | nindent 12 }}
```

- [ ] **Step 3: Write `projector-service.yaml`**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-projector
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: projector
spec:
  type: ClusterIP
  selector:
    {{- include "timadorus-platform.selectorLabels" . | nindent 4 }}
    app.kubernetes.io/component: projector
  ports:
    - name: http
      port: {{ .Values.projector.containerPort }}
      targetPort: http
```

- [ ] **Step 4: Render and inspect**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/projector-deployment.yaml \
  --show-only templates/projector-service.yaml
```
Expected: both manifests render with no template errors; the Deployment's `env` includes
`PROJECTOR_ADDR: ":8083"`, a `DATABASE_URL` `secretKeyRef` to `pg-secret`/`DATABASE_URL`, and
`NATS_URL: "nats://test-nats:4222"` (since `nats.enabled` defaults to `true`).

- [ ] **Step 5: Render with `nats.enabled=false` and confirm `NATS_URL` switches to the external value**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --set nats.enabled=false \
  --set nats.externalURL=nats://external-nats.example.com:4222 \
  --show-only templates/projector-deployment.yaml
```
Expected: `NATS_URL: "nats://external-nats.example.com:4222"` — the subchart-derived URL from
Step 4 is gone, and no NATS subchart resources render (the `condition: nats.enabled` in
`Chart.yaml` from Task 1 suppresses them).

- [ ] **Step 6: `helm lint`**

Run: `helm lint deploy/helm/timadorus-platform --set postgres.existingSecret=pg-secret --set gateway.gatewayClassName=dummy --set commandApi.route.hostname=x --set queryApi.route.hostname=y --set jwt.jwksURL=https://idp.example.com/jwks.json`
Expected: `0 chart(s) failed`.

- [ ] **Step 7: Commit**

```bash
git add deploy/helm/timadorus-platform/templates/_helpers.tpl \
  deploy/helm/timadorus-platform/templates/projector-deployment.yaml \
  deploy/helm/timadorus-platform/templates/projector-service.yaml
git commit -m "helm: add shared helpers and the projector Deployment/Service"
```

---

### Task 3: command-api Deployment/Service

**Files:**
- Create: `deploy/helm/timadorus-platform/templates/command-api-deployment.yaml`
- Create: `deploy/helm/timadorus-platform/templates/command-api-service.yaml`

**Interfaces:**
- Consumes: `timadorus-platform.labels`, `timadorus-platform.selectorLabels`,
  `timadorus-platform.fullname`, `timadorus-platform.databaseURLEnv`,
  `timadorus-platform.natsURL`, `timadorus-platform.jwtEnv` (all from Task 2's `_helpers.tpl`).

- [ ] **Step 1: Write `command-api-deployment.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-command-api
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: command-api
spec:
  replicas: {{ .Values.commandApi.replicas }}
  selector:
    matchLabels:
      {{- include "timadorus-platform.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: command-api
  template:
    metadata:
      labels:
        {{- include "timadorus-platform.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: command-api
    spec:
      containers:
        - name: command-api
          image: "{{ .Values.commandApi.image.repository }}:{{ .Values.commandApi.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.commandApi.image.pullPolicy }}
          ports:
            - name: http
              containerPort: {{ .Values.commandApi.containerPort }}
          env:
            - name: COMMAND_API_ADDR
              value: ":{{ .Values.commandApi.containerPort }}"
            {{- include "timadorus-platform.databaseURLEnv" . | nindent 12 }}
            - name: NATS_URL
              value: {{ include "timadorus-platform.natsURL" . | quote }}
            {{- include "timadorus-platform.jwtEnv" . | nindent 12 }}
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 15
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            {{- toYaml .Values.commandApi.resources | nindent 12 }}
```

- [ ] **Step 2: Write `command-api-service.yaml`**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-command-api
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: command-api
spec:
  type: ClusterIP
  selector:
    {{- include "timadorus-platform.selectorLabels" . | nindent 4 }}
    app.kubernetes.io/component: command-api
  ports:
    - name: http
      port: {{ .Values.commandApi.containerPort }}
      targetPort: http
```

- [ ] **Step 3: Render both JWT modes**

Run (jwks mode, the default):
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/command-api-deployment.yaml
```
Expected: env includes `JWT_JWKS_URL` set to the URL above, no `JWT_HMAC_SECRET` entry.

Run (hmac mode):
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.mode=hmac \
  --set jwt.hmac.existingSecret=jwt-secret \
  --show-only templates/command-api-deployment.yaml
```
Expected: env includes `JWT_HMAC_KEY_ID: "dev"` and a `JWT_HMAC_SECRET` `secretKeyRef` to
`jwt-secret`/`JWT_HMAC_SECRET`, no `JWT_JWKS_URL` entry.

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/timadorus-platform/templates/command-api-deployment.yaml \
  deploy/helm/timadorus-platform/templates/command-api-service.yaml
git commit -m "helm: add command-api Deployment/Service"
```

---

### Task 4: query-api Deployment/Service

**Files:**
- Create: `deploy/helm/timadorus-platform/templates/query-api-deployment.yaml`
- Create: `deploy/helm/timadorus-platform/templates/query-api-service.yaml`

**Interfaces:**
- Consumes: same helpers as Task 3, minus `timadorus-platform.natsURL` (query-api has no
  `NATS_URL` env var — confirmed in `internal/config.QueryAPI`, which has no `NATSURL` field).

- [ ] **Step 1: Write `query-api-deployment.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-query-api
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: query-api
spec:
  replicas: {{ .Values.queryApi.replicas }}
  selector:
    matchLabels:
      {{- include "timadorus-platform.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: query-api
  template:
    metadata:
      labels:
        {{- include "timadorus-platform.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: query-api
    spec:
      containers:
        - name: query-api
          image: "{{ .Values.queryApi.image.repository }}:{{ .Values.queryApi.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.queryApi.image.pullPolicy }}
          ports:
            - name: http
              containerPort: {{ .Values.queryApi.containerPort }}
          env:
            - name: QUERY_API_ADDR
              value: ":{{ .Values.queryApi.containerPort }}"
            {{- include "timadorus-platform.databaseURLEnv" . | nindent 12 }}
            {{- include "timadorus-platform.jwtEnv" . | nindent 12 }}
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 15
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            {{- toYaml .Values.queryApi.resources | nindent 12 }}
```

- [ ] **Step 2: Write `query-api-service.yaml`**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-query-api
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: query-api
spec:
  type: ClusterIP
  selector:
    {{- include "timadorus-platform.selectorLabels" . | nindent 4 }}
    app.kubernetes.io/component: query-api
  ports:
    - name: http
      port: {{ .Values.queryApi.containerPort }}
      targetPort: http
```

- [ ] **Step 3: Render and confirm no `NATS_URL`**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/query-api-deployment.yaml
```
Expected: env includes `QUERY_API_ADDR`, `DATABASE_URL`, `JWT_ISSUER`/`JWT_AUDIENCE`/
`JWT_JWKS_URL` — and no `NATS_URL` entry anywhere in the container spec.

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/timadorus-platform/templates/query-api-deployment.yaml \
  deploy/helm/timadorus-platform/templates/query-api-service.yaml
git commit -m "helm: add query-api Deployment/Service"
```

---

### Task 5: Gateway + HTTPRoutes (both `gateway.create` modes)

**Files:**
- Create: `deploy/helm/timadorus-platform/templates/gateway.yaml`
- Create: `deploy/helm/timadorus-platform/templates/command-api-httproute.yaml`
- Create: `deploy/helm/timadorus-platform/templates/query-api-httproute.yaml`

**Interfaces:**
- Consumes: `timadorus-platform.fullname`, `timadorus-platform.labels`,
  `timadorus-platform.gatewayParentRefs` (Task 2).

- [ ] **Step 1: Write `gateway.yaml`**

```yaml
{{- if .Values.gateway.create }}
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: {{ include "timadorus-platform.fullname" . }}
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
spec:
  gatewayClassName: {{ required "gateway.gatewayClassName must be set when gateway.create is true" .Values.gateway.gatewayClassName }}
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: Same
    {{- if .Values.gateway.tls.enabled }}
    - name: https
      protocol: HTTPS
      port: 443
      tls:
        mode: Terminate
        certificateRefs:
          - name: {{ required "gateway.tls.secretName must be set when gateway.tls.enabled is true" .Values.gateway.tls.secretName }}
      allowedRoutes:
        namespaces:
          from: Same
    {{- end }}
{{- end }}
```

- [ ] **Step 2: Write `command-api-httproute.yaml`**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-command-api
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
spec:
  {{- include "timadorus-platform.gatewayParentRefs" . | nindent 2 }}
  hostnames:
    - {{ required "commandApi.route.hostname is required" .Values.commandApi.route.hostname | quote }}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: {{ include "timadorus-platform.fullname" . }}-command-api
          port: {{ .Values.commandApi.containerPort }}
```

- [ ] **Step 3: Write `query-api-httproute.yaml`**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-query-api
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
spec:
  {{- include "timadorus-platform.gatewayParentRefs" . | nindent 2 }}
  hostnames:
    - {{ required "queryApi.route.hostname is required" .Values.queryApi.route.hostname | quote }}
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: {{ include "timadorus-platform.fullname" . }}-query-api
          port: {{ .Values.queryApi.containerPort }}
```

- [ ] **Step 4: Render `gateway.create: true` (default)**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/gateway.yaml \
  --show-only templates/command-api-httproute.yaml
```
Expected: a `Gateway` named `test-timadorus-platform` with `gatewayClassName: dummy` and one
`http` listener (no `https` listener, since `gateway.tls.enabled` defaults to false); the
`HTTPRoute`'s `parentRefs` is `[{name: test-timadorus-platform}]`.

- [ ] **Step 5: Render `gateway.create: false`**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.create=false \
  --set gateway.existing.name=shared-gw \
  --set gateway.existing.namespace=gateway-infra \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/gateway.yaml \
  --show-only templates/command-api-httproute.yaml
```
Expected: `templates/gateway.yaml` renders **empty output** (no `Gateway` resource — Helm
prints `# Source: timadorus-platform/templates/gateway.yaml` with nothing after it, or omits
the source header entirely depending on Helm version); the `HTTPRoute`'s `parentRefs` is
`[{name: shared-gw, namespace: gateway-infra}]`.

- [ ] **Step 6: Render `gateway.tls.enabled: true`**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set gateway.tls.enabled=true \
  --set gateway.tls.secretName=my-tls-secret \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/gateway.yaml
```
Expected: the `Gateway` now has two listeners — `http` (port 80) and `https` (port 443,
`tls.mode: Terminate`, `certificateRefs: [{name: my-tls-secret}]`).

- [ ] **Step 7: Commit**

```bash
git add deploy/helm/timadorus-platform/templates/gateway.yaml \
  deploy/helm/timadorus-platform/templates/command-api-httproute.yaml \
  deploy/helm/timadorus-platform/templates/query-api-httproute.yaml
git commit -m "helm: add Gateway + HTTPRoutes with a create-vs-attach toggle"
```

---

### Task 6: Shared migration script + `Makefile` refactor

**Files:**
- Create: `scripts/migrate-up.sh`
- Modify: `Makefile:1-50` (remove the inlined `SCHEMA_OWNERS`/`MIGRATE` loop, call the script)

**Interfaces:**
- Produces: `scripts/migrate-up.sh <up|down>`, reading `DATABASE_URL` (required),
  `MIGRATIONS_BASE` (default `.`), `MIGRATE_BIN` (default
  `go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1`) from the
  environment. Task 7's `Dockerfile.migrate` calls this same script with
  `MIGRATIONS_BASE=/migrations` and `MIGRATE_BIN=migrate`.

- [ ] **Step 1: Write `scripts/migrate-up.sh`**

```bash
#!/usr/bin/env bash
# Shared by `make migrate-up`/`make migrate-down` (Makefile) and the Helm chart's migration
# Job (deploy/helm/timadorus-platform/templates/migration-job.yaml, via Dockerfile.migrate) —
# single source of truth for the schema-owner list (plan §1: one migration directory per
# schema owner, each with its own independent schema_migrations tracking table).
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL must be set}"
: "${MIGRATIONS_BASE:=.}"
: "${MIGRATE_BIN:=go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1}"

direction="${1:?usage: migrate-up.sh <up|down>}"

schema_owners=(
  "eventstore:internal/eventstore/postgres/migrations"
  "projection_checkpoint:internal/projection/checkpoint/migrations"
  "projection_universe:internal/projection/universe/migrations"
  "projection_user:internal/projection/user/migrations"
  "projection_campaign:internal/projection/campaign/migrations"
  "projection_entity:internal/projection/entity/migrations"
  "projection_character:internal/projection/character/migrations"
  "projection_object:internal/projection/object/migrations"
)

for owner in "${schema_owners[@]}"; do
  name="${owner%%:*}"
  path="${owner#*:}"
  echo "==> migrate ${direction}: ${name}"
  if [ "$direction" = "down" ]; then
    $MIGRATE_BIN -database "${DATABASE_URL}&x-migrations-table=schema_migrations_${name}" -source "file://${MIGRATIONS_BASE}/${path}" down 1
  else
    $MIGRATE_BIN -database "${DATABASE_URL}&x-migrations-table=schema_migrations_${name}" -source "file://${MIGRATIONS_BASE}/${path}" "$direction"
  fi
done
```

- [ ] **Step 2: Make it executable**

```bash
chmod +x scripts/migrate-up.sh
```

- [ ] **Step 3: Refactor `Makefile`**

Replace the whole file with:

```makefile
.PHONY: build build-tools test lint generate migrate-up migrate-down dev-up dev-down

DATABASE_URL ?= postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable
BINDIR	     ?= $(CURDIR)/bin

build:
	go build ./...

build-tools: $(BINDIR)
	go build -o $(BINDIR)/timadorusctl ./cmd/timadorusctl

test:
	go test ./...

lint:
	go vet ./...
	# Best-effort: no golangci-lint release yet supports analyzing a `go 1.26` module (hard
	# failure in go/types on older analyzing toolchains, not a config issue) — don't fail the
	# build on it, `go vet` above is the enforced check until golangci-lint catches up.
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run || true

generate:
	go generate ./...

# scripts/migrate-up.sh holds the schema-owner list (plan §1: one migration directory per
# schema owner, each with its own schema_migrations tracking table) so the Helm chart's
# migration Job (deploy/helm/timadorus-platform) can share it instead of duplicating it.
migrate-up:
	DATABASE_URL="$(DATABASE_URL)" MIGRATIONS_BASE="$(CURDIR)" ./scripts/migrate-up.sh up

migrate-down:
	DATABASE_URL="$(DATABASE_URL)" MIGRATIONS_BASE="$(CURDIR)" ./scripts/migrate-up.sh down

dev-up:
	docker compose up -d

dev-down:
	docker compose down -v


$(BINDIR):
	mkdir -p $(BINDIR)
```

- [ ] **Step 4: Regression-test against the docker-compose Postgres**

Run:
```bash
make dev-up
sleep 3
make migrate-up
```
Expected: eight `==> migrate up: <name>` lines, each followed by `golang-migrate`'s own
success output (or `no change` if already applied), exit code 0.

- [ ] **Step 5: Verify `migrate-down` also still works**

Run: `make migrate-down`
Expected: eight `==> migrate down: <name>` lines, each rolling back exactly one migration, exit
code 0. Re-run `make migrate-up` afterward to leave the dev database in the "up" state before
moving on: `make migrate-up`.

- [ ] **Step 6: Commit**

```bash
git add scripts/migrate-up.sh Makefile
git commit -m "build: extract migration loop into scripts/migrate-up.sh"
```

---

### Task 7: `Dockerfile.migrate`

**Files:**
- Create: `Dockerfile.migrate`

**Interfaces:**
- Consumes: `scripts/migrate-up.sh` (Task 6).
- Produces: an image whose `ENTRYPOINT` is `scripts/migrate-up.sh`, expecting `up` or `down` as
  `docker run`'s trailing argument and `DATABASE_URL` as an env var — this is exactly what
  Task 8's `migration-job.yaml` invokes.

- [ ] **Step 1: Write `Dockerfile.migrate`**

```dockerfile
# Build: docker build -f Dockerfile.migrate -t timadorus/migrate .
# Run:   docker run --rm -e DATABASE_URL=... timadorus/migrate up

FROM golang:1.26-alpine AS build
RUN apk add --no-cache ca-certificates
RUN CGO_ENABLED=0 go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

FROM alpine:3.20
RUN apk add --no-cache bash
COPY --from=build /go/bin/migrate /usr/local/bin/migrate
COPY internal/eventstore/postgres/migrations /migrations/eventstore
COPY internal/projection/checkpoint/migrations /migrations/projection_checkpoint
COPY internal/projection/universe/migrations /migrations/projection_universe
COPY internal/projection/user/migrations /migrations/projection_user
COPY internal/projection/campaign/migrations /migrations/projection_campaign
COPY internal/projection/entity/migrations /migrations/projection_entity
COPY internal/projection/character/migrations /migrations/projection_character
COPY internal/projection/object/migrations /migrations/projection_object
COPY scripts/migrate-up.sh /usr/local/bin/migrate-up.sh
ENV MIGRATIONS_BASE=/migrations
ENV MIGRATE_BIN=migrate
ENTRYPOINT ["/usr/local/bin/migrate-up.sh"]
```

- [ ] **Step 2: Build the image**

Run: `docker build -f Dockerfile.migrate -t timadorus/migrate:test .`
Expected: builds successfully, final image based on `alpine:3.20`.

- [ ] **Step 3: Run it against the docker-compose Postgres, direction `down` then `up`**

The compose network's Postgres is reachable from another container on the same Docker network
(`docker compose` creates one named `<project>_default`; find it with
`docker network ls | grep timadorus` if the project directory name differs from `timadorus`).

Run:
```bash
docker run --rm --network container:$(docker compose ps -q postgres) \
  -e DATABASE_URL="postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable" \
  timadorus/migrate:test down
docker run --rm --network container:$(docker compose ps -q postgres) \
  -e DATABASE_URL="postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable" \
  timadorus/migrate:test up
```
Expected: same eight-line `==> migrate <direction>: <name>` output as `make migrate-up`/
`make migrate-down` produced in Task 6, ending with the schema back in the fully-migrated
state.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile.migrate
git commit -m "build: add Dockerfile.migrate for the Helm chart's migration Job"
```

---

### Task 8: `migration-job.yaml` Helm template

**Files:**
- Create: `deploy/helm/timadorus-platform/templates/migration-job.yaml`

**Interfaces:**
- Consumes: `timadorus-platform.fullname`, `timadorus-platform.labels`,
  `timadorus-platform.selectorLabels`, `timadorus-platform.databaseURLEnv` (Task 2);
  `values.migration.*` (Task 1); the image built in Task 7.

- [ ] **Step 1: Write `migration-job.yaml`**

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-migrate
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: migrate
  annotations:
    helm.sh/hook: pre-install,pre-upgrade
    helm.sh/hook-weight: "0"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        {{- include "timadorus-platform.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: migrate
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: "{{ .Values.migration.image.repository }}:{{ .Values.migration.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.migration.image.pullPolicy }}
          args: ["up"]
          env:
            {{- include "timadorus-platform.databaseURLEnv" . | nindent 12 }}
          resources:
            {{- toYaml .Values.migration.resources | nindent 12 }}
```

- [ ] **Step 2: Render and inspect the hook annotations**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/migration-job.yaml
```
Expected: a `batch/v1` `Job` with `helm.sh/hook: pre-install,pre-upgrade`, `args: ["up"]`, and
a `DATABASE_URL` env entry sourced from `pg-secret`.

- [ ] **Step 3: `helm lint` the whole chart end to end**

Run:
```bash
helm lint deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json
```
Expected: `0 chart(s) failed`.

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/timadorus-platform/templates/migration-job.yaml
git commit -m "helm: add pre-install/pre-upgrade migration Job"
```

---

### Task 9: Chart `README.md` + `NOTES.txt`

**Files:**
- Create: `deploy/helm/timadorus-platform/README.md`
- Create: `deploy/helm/timadorus-platform/templates/NOTES.txt`

**Interfaces:**
- None — this is documentation only, consumed by a human running `helm install`/`helm upgrade`.

- [ ] **Step 1: Write `templates/NOTES.txt`**

```
Timadorus platform release "{{ .Release.Name }}" deployed to namespace "{{ .Release.Namespace }}".

Migration Job logs:
  kubectl logs -n {{ .Release.Namespace }} job/{{ include "timadorus-platform.fullname" . }}-migrate

{{- if .Values.gateway.create }}
Gateway "{{ include "timadorus-platform.fullname" . }}" created (gatewayClassName: {{ .Values.gateway.gatewayClassName }}).
{{- else }}
Routes attached to existing Gateway "{{ .Values.gateway.existing.name }}"{{ if .Values.gateway.existing.namespace }} in namespace "{{ .Values.gateway.existing.namespace }}"{{ end }}.
{{- end }}

Routes:
  command-api -> http://{{ .Values.commandApi.route.hostname }}/
  query-api   -> http://{{ .Values.queryApi.route.hostname }}/

Check rollout status:
  kubectl rollout status -n {{ .Release.Namespace }} deployment/{{ include "timadorus-platform.fullname" . }}-command-api
  kubectl rollout status -n {{ .Release.Namespace }} deployment/{{ include "timadorus-platform.fullname" . }}-query-api
  kubectl rollout status -n {{ .Release.Namespace }} deployment/{{ include "timadorus-platform.fullname" . }}-projector
```

- [ ] **Step 2: Write `README.md`**

```markdown
# timadorus-platform Helm chart

Deploys the Timadorus CQRS/ES platform's three binaries (command-api, query-api, projector) to
Kubernetes, running schema migrations automatically and exposing command-api/query-api via the
Gateway API. See `docs/PLAN.md` for the platform architecture this chart deploys.

## Prerequisites

- A Kubernetes cluster with the [Gateway API CRDs](https://gateway-api.sigs.k8s.io/) installed,
  and (unless `gateway.create: false` and you're attaching to an already-working Gateway) a
  Gateway API controller installed and its `GatewayClass` name known.
- A reachable Postgres instance, plus a `Secret` in the release namespace holding a
  `DATABASE_URL` connection string (this chart never creates that Secret — see
  `postgres.existingSecret` below).
- Images for `command-api`, `query-api`, `projector`, and `migrate` built and pushed somewhere
  the cluster can pull from (`Dockerfile.command-api`, `Dockerfile.query-api`,
  `Dockerfile.projector`, `Dockerfile.migrate` at the repo root).

## Install

```bash
helm dependency update deploy/helm/timadorus-platform

helm install my-platform deploy/helm/timadorus-platform \
  --set postgres.existingSecret=my-postgres-secret \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set gateway.gatewayClassName=my-gateway-class \
  --set jwt.jwksURL=https://my-idp.example.com/.well-known/jwks.json
```

### Attaching to an existing Gateway instead of creating one

```bash
helm install my-platform deploy/helm/timadorus-platform \
  --set postgres.existingSecret=my-postgres-secret \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set gateway.create=false \
  --set gateway.existing.name=shared-gateway \
  --set gateway.existing.namespace=gateway-infra \
  --set jwt.jwksURL=https://my-idp.example.com/.well-known/jwks.json
```

### Using an external NATS cluster instead of the bundled one

```bash
  --set nats.enabled=false \
  --set nats.externalURL=nats://nats.my-cluster.svc.cluster.local:4222
```

## Values reference

| Key | Default | Description |
|---|---|---|
| `commandApi.image.repository` / `.tag` / `.pullPolicy` | `timadorus/command-api` / `""` (falls back to `Chart.AppVersion`) / `IfNotPresent` | command-api image |
| `commandApi.replicas` | `1` | command-api replica count |
| `commandApi.route.hostname` | `""` (required) | hostname the command-api `HTTPRoute` matches |
| `queryApi.*` | (mirrors `commandApi.*`) | query-api equivalents |
| `projector.image.*` / `.replicas` | (mirrors `commandApi.*`) | projector has no `route.hostname` — no public API |
| `migration.image.*` | `timadorus/migrate` / ... | image used by the pre-install/pre-upgrade migration Job |
| `postgres.existingSecret` | `""` (required) | name of a pre-existing Secret holding `DATABASE_URL` |
| `postgres.secretKey` | `DATABASE_URL` | key within that Secret |
| `nats.enabled` | `true` | deploy NATS JetStream as a subchart dependency |
| `nats.externalURL` | `""` | used instead when `nats.enabled: false` |
| `jwt.mode` | `jwks` | `jwks` or `hmac` |
| `jwt.jwksURL` / `.issuer` / `.audience` | `""` | used when `jwt.mode: jwks` |
| `jwt.hmac.existingSecret` / `.secretKey` / `.keyID` | `""` / `JWT_HMAC_SECRET` / `dev` | used when `jwt.mode: hmac` |
| `gateway.create` | `true` | `true`: chart creates its own `Gateway`; `false`: attach to an existing one |
| `gateway.gatewayClassName` | `""` (required when `create: true`) | `GatewayClass` for the chart-owned `Gateway` |
| `gateway.tls.enabled` / `.secretName` | `false` / `""` | optional HTTPS listener on the chart-owned `Gateway` |
| `gateway.existing.name` / `.namespace` / `.sectionName` | `""` | required when `create: false` — coordinates of the pre-existing `Gateway` to attach to |
```

- [ ] **Step 3: Render with `NOTES.txt` included and eyeball it**

Run:
```bash
helm template test deploy/helm/timadorus-platform \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json \
  --show-only templates/NOTES.txt 2>&1 || true
```
Note: `helm template` does not render `NOTES.txt` by name via `--show-only` in all Helm
versions — if this errors, instead run a real `helm install --dry-run` to see NOTES output:
```bash
helm install test deploy/helm/timadorus-platform --dry-run \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=dummy \
  --set commandApi.route.hostname=command-api.example.com \
  --set queryApi.route.hostname=query-api.example.com \
  --set jwt.jwksURL=https://idp.example.com/jwks.json
```
Expected: the printed NOTES section shows the migration-job log command, the Gateway line, and
both route URLs with the hostnames substituted in.

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/timadorus-platform/README.md deploy/helm/timadorus-platform/templates/NOTES.txt
git commit -m "helm: add chart README and NOTES.txt"
```

---

### Task 10: End-to-end verification against the local kind cluster

**Files:** none created — this task exercises everything built in Tasks 1-9 against a real
cluster (`kind-kind` context, already present in this environment) plus a disposable in-cluster
Postgres.

**Interfaces:** none — terminal task, no later task depends on it.

- [ ] **Step 1: Install the Gateway API CRDs**

Run: `kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.1/standard-install.yaml`
Expected: several `gatewayclasses.gateway.networking.k8s.io created` /
`gateways.gateway.networking.k8s.io created` / `httproutes.gateway.networking.k8s.io created`
lines (and related CRDs), exit code 0.

- [ ] **Step 2: Build all four images and load them into kind**

Run:
```bash
docker build -f Dockerfile.command-api -t timadorus/command-api:e2e .
docker build -f Dockerfile.query-api -t timadorus/query-api:e2e .
docker build -f Dockerfile.projector -t timadorus/projector:e2e .
docker build -f Dockerfile.migrate -t timadorus/migrate:e2e .
kind load docker-image timadorus/command-api:e2e timadorus/query-api:e2e \
  timadorus/projector:e2e timadorus/migrate:e2e
```
Expected: all four builds succeed; `kind load docker-image` reports each image loaded into the
`kind` cluster's node.

- [ ] **Step 3: Deploy a disposable Postgres + Secret into the cluster**

Run:
```bash
kubectl create namespace timadorus-e2e
kubectl -n timadorus-e2e create secret generic pg-secret \
  --from-literal=DATABASE_URL="postgres://timadorus:timadorus@e2e-postgres:5432/timadorus?sslmode=disable"
cat <<'EOF' | kubectl -n timadorus-e2e apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: e2e-postgres
spec:
  replicas: 1
  selector:
    matchLabels: { app: e2e-postgres }
  template:
    metadata:
      labels: { app: e2e-postgres }
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          env:
            - { name: POSTGRES_USER, value: timadorus }
            - { name: POSTGRES_PASSWORD, value: timadorus }
            - { name: POSTGRES_DB, value: timadorus }
          ports:
            - containerPort: 5432
---
apiVersion: v1
kind: Service
metadata:
  name: e2e-postgres
spec:
  selector: { app: e2e-postgres }
  ports:
    - { port: 5432, targetPort: 5432 }
EOF
kubectl -n timadorus-e2e rollout status deployment/e2e-postgres --timeout=60s
```
Expected: namespace and secret created, Postgres Deployment reaches `deployment
"e2e-postgres" successfully rolled out`.

- [ ] **Step 4: `helm install` with a minimal dummy `GatewayClass`**

Run:
```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: e2e-gatewayclass
spec:
  controllerName: example.com/e2e-no-op-controller
EOF

helm install e2e-release deploy/helm/timadorus-platform -n timadorus-e2e \
  --set commandApi.image.repository=timadorus/command-api --set commandApi.image.tag=e2e \
  --set queryApi.image.repository=timadorus/query-api --set queryApi.image.tag=e2e \
  --set projector.image.repository=timadorus/projector --set projector.image.tag=e2e \
  --set migration.image.repository=timadorus/migrate --set migration.image.tag=e2e \
  --set commandApi.image.pullPolicy=IfNotPresent --set queryApi.image.pullPolicy=IfNotPresent \
  --set projector.image.pullPolicy=IfNotPresent --set migration.image.pullPolicy=IfNotPresent \
  --set postgres.existingSecret=pg-secret \
  --set gateway.gatewayClassName=e2e-gatewayclass \
  --set commandApi.route.hostname=command-api.e2e.local \
  --set queryApi.route.hostname=query-api.e2e.local \
  --set jwt.jwksURL=https://idp.example.com/.well-known/jwks.json \
  --wait --timeout 3m
```
Expected: Helm reports the release deployed; `--wait` blocks until the migration Job (a
pre-install hook) completes and all three Deployments are ready, or the command fails loudly if
something is stuck — either result tells you something real.

If it fails, run `kubectl -n timadorus-e2e get pods` and
`kubectl -n timadorus-e2e logs job/e2e-release-timadorus-platform-migrate` to diagnose before
proceeding — do not skip this task or move on with a broken install.

- [ ] **Step 5: Verify the migration Job actually ran the migrations**

Run:
```bash
kubectl -n timadorus-e2e logs job/e2e-release-timadorus-platform-migrate
kubectl -n timadorus-e2e exec deploy/e2e-postgres -- \
  psql -U timadorus -d timadorus -c '\dt' | grep schema_migrations
```
Expected: the Job log shows all eight `==> migrate up: <name>` lines; the `psql` output lists
eight `schema_migrations_<name>` tables (one per schema owner).

- [ ] **Step 6: Verify Gateway API resources exist**

Run:
```bash
kubectl -n timadorus-e2e get gateway
kubectl -n timadorus-e2e get httproute
```
Expected: one `Gateway` named `e2e-release-timadorus-platform`, and two `HTTPRoute`s
(`...-command-api`, `...-query-api`) with the hostnames set above. Note: with the dummy
`e2e-gatewayclass` controller (`example.com/e2e-no-op-controller`, which nothing actually
reconciles), the `Accepted`/`Programmed` conditions will likely show `Unknown` or absent rather
than `True` — that's expected here since no real Gateway API implementation is installed in
this test; it does not indicate a problem with the chart's own resources, which the API server
already accepted as schema-valid by creating them.

- [ ] **Step 7: Verify the three Deployments are actually healthy via their own probes**

Run:
```bash
kubectl -n timadorus-e2e port-forward svc/e2e-release-timadorus-platform-command-api 18081:8081 &
sleep 2
curl -sf http://localhost:18081/healthz && echo " OK healthz" && curl -sf http://localhost:18081/readyz && echo " OK readyz"
kill %1

kubectl -n timadorus-e2e port-forward svc/e2e-release-timadorus-platform-query-api 18082:8082 &
sleep 2
curl -sf http://localhost:18082/healthz && echo " OK healthz" && curl -sf http://localhost:18082/readyz && echo " OK readyz"
kill %1

kubectl -n timadorus-e2e port-forward svc/e2e-release-timadorus-platform-projector 18083:8083 &
sleep 2
curl -sf http://localhost:18083/healthz && echo " OK healthz" && curl -sf http://localhost:18083/readyz && echo " OK readyz"
kill %1
```
Expected: every `curl` returns HTTP 200 (`-f` makes curl fail loudly on non-2xx), confirming
each container's own `/healthz`/`/readyz` — the same probes Kubernetes itself is using — pass
for real, not just that the pod is `Running`.

- [ ] **Step 8: Clean up the disposable test resources**

Run:
```bash
helm uninstall e2e-release -n timadorus-e2e
kubectl delete namespace timadorus-e2e
kubectl delete gatewayclass e2e-gatewayclass
```
Expected: release uninstalled, namespace and dummy `GatewayClass` deleted. Leave the Gateway
API CRDs installed (harmless, and removing them is unnecessary — they don't belong to this
release).

- [ ] **Step 9: No commit for this task**

This task only exercises the cluster; nothing in the repo changes. If Steps 4-7 surfaced a bug
in an earlier task's templates/scripts, go back, fix it in that task's files, re-run this
task's steps, and commit the fix with a message describing what was wrong (e.g.
`helm: fix missing NATS_URL quoting in command-api-deployment.yaml`).

---

## Self-review notes (fixed inline before handoff)

- Confirmed every env var name (`COMMAND_API_ADDR`, `QUERY_API_ADDR`, `PROJECTOR_ADDR`,
  `DATABASE_URL`, `NATS_URL`, `JWT_JWKS_URL`, `JWT_HMAC_SECRET`, `JWT_HMAC_KEY_ID`,
  `JWT_ISSUER`, `JWT_AUDIENCE`) against `internal/config/config.go` directly — no invented
  names.
- Confirmed query-api has no `NATS_URL` (its `internal/config.QueryAPI` struct has no
  `NATSURL` field) — Task 4 deliberately omits it, Task 3/Task 2 include it for command-api/
  projector.
- Confirmed all three binaries expose `/healthz`/`/readyz`/`/metrics` on the same port as their
  main traffic (`grep` of all three `cmd/*/main.go` files) — probes in Tasks 2-4 target the
  named `http` container port, not a separate port.
- Confirmed the `nats` chart dependency (`2.14.2`) exists in the `nats-io/k8s` repo and its
  Service naming (`<release-name>-nats`, port `4222`) via `helm search repo` / `helm template`
  against the real chart during design — not guessed.
- Confirmed the Gateway API CRD release used in Task 10 (`v1.6.1`) is the actual latest
  release via the GitHub API at design time, not an assumed version number.
- Confirmed `kind`, `kubectl`, and `docker` are present and a `kind` cluster already exists in
  this environment before writing Task 10 as a real (not hypothetical) verification step.

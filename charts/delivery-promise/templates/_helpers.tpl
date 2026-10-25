{{/*
Expand the name of the chart.
*/}}
{{- define "delivery-promise.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "delivery-promise.fullname" -}}
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
Chart name and version as used by the chart label.
*/}}
{{- define "delivery-promise.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "delivery-promise.labels" -}}
helm.sh/chart: {{ include "delivery-promise.chart" . }}
{{ include "delivery-promise.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. The chart has one always-on component (api) plus the
optional mcp and frontend (default OFF); every Deployment and Service pins
app.kubernetes.io/component so a Service can never select another
component's pods by accident (tests/test_service_selectors.py).
*/}}
{{- define "delivery-promise.selectorLabels" -}}
app.kubernetes.io/name: {{ include "delivery-promise.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "delivery-promise.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "delivery-promise.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "delivery-promise.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "delivery-promise.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference of the api Deployment.
*/}}
{{- define "delivery-promise.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
MANDATORY: Postgres is the only runtime. cmd/api exits at boot without
DATABASE_URL and has no in-memory mode. Fails the render naming the values
instead of crash-looping.
*/}}
{{- define "delivery-promise.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "delivery-promise requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: Kafka is the only runtime. cmd/api exits at boot without
KAFKA_BROKERS and rejects EVENT_PUBLISHER=log. There is no default broker,
no log sink and no way to switch Kafka off.
*/}}
{{- define "delivery-promise.requireKafka" -}}
{{- $_ := required "delivery-promise requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- if ne (toString .Values.config.eventPublisher) "kafka" -}}
{{- fail (printf "config.eventPublisher is %q - the only publisher is \"kafka\": there is no log sink and the binary rejects any other EVENT_PUBLISHER at boot." (toString .Values.config.eventPublisher)) -}}
{{- end -}}
{{- end -}}

{{/*
The one consumer of the eight producing contexts always runs, under a STABLE
consumer group id that must come from values (cmd/api exits at boot without
DELIVERY_PROMISE_CONSUMER_GROUP). A stable, service-prefixed id is what lets a
rescheduled pod resume from its committed offsets; it is never the pod name.
*/}}
{{- define "delivery-promise.requireConsumerGroup" -}}
{{- if not .Values.config.consumerGroup -}}
{{- fail "config.consumerGroup is required (DELIVERY_PROMISE_CONSUMER_GROUP): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id (for example delivery-promise-consumer), never the pod name." -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back an in-memory, log or permissive mode: no
EVENT_PUBLISHER other than kafka and no *_MODE variable at all (this binary
reads none; the real runtime has no mode switch). It also may not set a
variable the chart renders from a dedicated value, so nothing is set twice.
*/}}
{{- define "delivery-promise.requireCleanExtraEnv" -}}
{{- $dedicated := list "DATABASE_URL" "MIGRATIONS_DATABASE_URL" "KAFKA_BROKERS" "DELIVERY_PROMISE_CONSUMER_GROUP" "EVENT_PUBLISHER" "HTTP_ADDR" "LOG_LEVEL" "OUTBOX_RELAY_INTERVAL" "OUTBOX_MAX_ATTEMPTS" "SWEEP_INTERVAL" "SHUTDOWN_DRAIN_DELAY" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- $value := toString (default "" .value) -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s - *_MODE variables are refused: delivery-promise runs on real Postgres and real Kafka only, with no permissive, off or in-memory mode." $name) -}}
{{- end -}}
{{- if and (eq $name "EVENT_PUBLISHER") (ne $value "kafka") -}}
{{- fail (printf "extraEnv sets EVENT_PUBLISHER=%s - the only publisher is \"kafka\"." $value) -}}
{{- end -}}
{{- if has $name $dedicated -}}
{{- fail (printf "extraEnv sets %s - that variable has a dedicated chart value (database.*, kafka.brokers, config.*); set it there so it is rendered once." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
config.shutdownDrainDelay as whole seconds (rounded up). The binary parses it
with Go's time.ParseDuration ("5s", "1m30s", "500ms", "0"); empty means the
binary default of 5s. Anything else is refused so the grace-period check below
never works from a value the binary would ignore.
*/}}
{{- define "delivery-promise.drainSeconds" -}}
{{- $raw := toString (default "" .Values.config.shutdownDrainDelay) -}}
{{- if eq $raw "" -}}
5
{{- else if eq $raw "0" -}}
0
{{- else if regexMatch "^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$" $raw -}}
{{- $total := 0.0 -}}
{{- range $tok := regexFindAll "[0-9]+(\\.[0-9]+)?(ms|s|m|h)" $raw -1 -}}
{{- $n := float64 (regexFind "^[0-9]+(\\.[0-9]+)?" $tok) -}}
{{- $unit := regexFind "(ms|s|m|h)$" $tok -}}
{{- if eq $unit "ms" -}}{{- $total = addf $total (divf $n 1000.0) -}}
{{- else if eq $unit "s" -}}{{- $total = addf $total $n -}}
{{- else if eq $unit "m" -}}{{- $total = addf $total (mulf $n 60.0) -}}
{{- else -}}{{- $total = addf $total (mulf $n 3600.0) -}}
{{- end -}}
{{- end -}}
{{- ceil $total | int -}}
{{- else -}}
{{- fail (printf "config.shutdownDrainDelay is %q - want a Go duration such as 5s, 1m30s or 0 (the binary ignores anything else and uses 5s)." $raw) -}}
{{- end -}}
{{- end -}}

{{/*
The graceful shutdown of cmd/api is sequential: /readyz flips, then
SHUTDOWN_DRAIN_DELAY, then the HTTP drain (shutdownTimeout 10s), then the
consumer, the sweeper and, LAST, the outbox relay are awaited (workerDrainTimeout
10s each). The grace period must EXCEED that sum or the pod is SIGKILLed between
"consumer stopped" and "relay stopped", stranding an uncommitted offset or an
in-flight outbox batch. 3 workers x 10s + HTTP 10s = 40s + the drain delay.
tests/render_test.sh reads the constants from cmd/api/config.go and fails when
they drift from the numbers here.
*/}}
{{- define "delivery-promise.requireGracePeriod" -}}
{{- $needed := add (include "delivery-promise.drainSeconds" . | int) 40 -}}
{{- if le (int .Values.terminationGracePeriodSeconds) (int $needed) -}}
{{- fail (printf "terminationGracePeriodSeconds is %v - it must exceed %v (SHUTDOWN_DRAIN_DELAY %vs + HTTP drain 10s + consumer, sweeper and outbox relay 10s each): a shorter grace period SIGKILLs the consumer or the relay mid-drain." .Values.terminationGracePeriodSeconds $needed (include "delivery-promise.drainSeconds" .)) -}}
{{- end -}}
{{- end -}}

{{/*
config.sweepInterval and config.outboxRelayInterval: a Go duration or plain
seconds. A malformed value is warned about and replaced by the default by the
binary; refuse it at render so the intent is never silently lost.
*/}}
{{- define "delivery-promise.requireIntervals" -}}
{{- range $key := list "sweepInterval" "outboxRelayInterval" -}}
{{- $v := toString (default "" (index $.Values.config $key)) -}}
{{- if and $v (not (regexMatch "^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$|^[0-9]+(\\.[0-9]+)?$" $v)) -}}
{{- fail (printf "config.%s is %q - want a Go duration such as 500ms, 15s or 1m, or a plain number of seconds." $key $v) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment (always rendered) includes it, so a mis-set release fails
`helm template` / `helm install` whichever optional template is enabled.
*/}}
{{- define "delivery-promise.requireRealRuntime" -}}
{{- include "delivery-promise.requireDatabase" . -}}
{{- include "delivery-promise.requireKafka" . -}}
{{- include "delivery-promise.requireConsumerGroup" . -}}
{{- include "delivery-promise.requireCleanExtraEnv" . -}}
{{- include "delivery-promise.requireIntervals" . -}}
{{- include "delivery-promise.requireGracePeriod" . -}}
{{- end -}}

{{/*
Fully qualified name of the A2A agent Deployment/Service (docs/adr/0010).

cmd/a2a is a third deployable of the same image: it reads the service
database through the same use cases as the API and writes nothing (no relay,
no consumer, no sweeper, no Kafka). ClusterIP only; exposure is the optional
Kong HTTPRoute (a2a-route.yaml).
*/}}
{{- define "delivery-promise.a2aFullname" -}}
{{- include "delivery-promise.fullname" . }}-a2a
{{- end }}

{{/*
a2a.extraEnv may not bring back an in-memory, log or permissive mode (no
*_MODE variable at all), may not give the read-only agent the write side's
Kafka/relay/consumer/sweeper environment, and may not set a variable the
chart renders from a dedicated value, so nothing is set twice.
*/}}
{{- define "delivery-promise.requireCleanA2AExtraEnv" -}}
{{- $dedicated := list "A2A_ADDR" "A2A_AGENT_URL" "DATABASE_URL" "MIGRATIONS_DATABASE_URL" "LOG_LEVEL" "ENVIRONMENT" "SERVICE_VERSION" "OTEL_SERVICE_NAME" "OTEL_EXPORTER_OTLP_ENDPOINT" -}}
{{- $writeSide := list "KAFKA_BROKERS" "EVENT_PUBLISHER" "DELIVERY_PROMISE_CONSUMER_GROUP" "OUTBOX_RELAY_INTERVAL" "OUTBOX_MAX_ATTEMPTS" "SWEEP_INTERVAL" "SHUTDOWN_DRAIN_DELAY" -}}
{{- range .Values.a2a.extraEnv -}}
{{- $name := toString .name -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "a2a.extraEnv sets %s - *_MODE variables are refused: delivery-promise runs on real Postgres only, with no permissive, off or in-memory mode." $name) -}}
{{- end -}}
{{- if has $name $writeSide -}}
{{- fail (printf "a2a.extraEnv sets %s - the A2A agent is read-only and never dials Kafka; that variable belongs to the api component." $name) -}}
{{- end -}}
{{- if has $name $dedicated -}}
{{- fail (printf "a2a.extraEnv sets %s - that variable has a dedicated chart value (a2a.*, database.*, config.logLevel, otel.*, environment); set it there so it is rendered once." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

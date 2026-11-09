{{/*
Fully qualified names of the analytics read side (ADR 0018): the projector
Deployment (no Service: it serves only its admin probes) and the reports
Deployment/Service. Components analytics-projector and analytics-reports keep
every Service selecting exactly one Deployment.
*/}}
{{- define "delivery-promise.projectorFullname" -}}
{{- include "delivery-promise.fullname" . }}-projector
{{- end }}

{{- define "delivery-promise.reportsFullname" -}}
{{- include "delivery-promise.fullname" . }}-reports
{{- end }}

{{/*
Name of the Secret holding the analytical DSNs: the operator's own
(analytics.database.existingSecret, keys ANALYTICS_DATABASE_URL and
ANALYTICS_READER_DATABASE_URL) or the one this chart creates.
*/}}
{{- define "delivery-promise.analyticsSecretName" -}}
{{- if .Values.analytics.database.existingSecret }}
{{- .Values.analytics.database.existingSecret }}
{{- else }}
{{- include "delivery-promise.fullname" . }}-analytics
{{- end }}
{{- end }}

{{/*
analytics.enabled needs an analytical DSN source (both binaries refuse to boot
without one), the Kafka brokers (the projector consumes
retailnetwork.delivery-promise.events and .analytics) and a stable consumer
group. Fail at render instead of crash-looping.
*/}}
{{- define "delivery-promise.requireAnalytics" -}}
{{- if not (or .Values.analytics.database.projectorUrl .Values.analytics.database.existingSecret) -}}
{{- fail "analytics.enabled is true but neither analytics.database.projectorUrl nor analytics.database.existingSecret is set (ANALYTICS_DATABASE_URL): the projector and the reports refuse to boot without an analytical database." -}}
{{- end -}}
{{- if not .Values.kafka.brokers -}}
{{- fail "analytics.enabled is true but kafka.brokers is empty (KAFKA_BROKERS): the projector consumes retailnetwork.delivery-promise.events and .analytics." -}}
{{- end -}}
{{- if not .Values.analytics.projector.consumerGroup -}}
{{- fail "analytics.projector.consumerGroup is required (ANALYTICS_CONSUMER_GROUP): use the STABLE id delivery-promise-analytics, never the pod name." -}}
{{- end -}}
{{- end -}}

{{/*
Fully qualified name of the MCP server Deployment/Service (ADR 0007).

cmd/mcp is a second deployable of the same image: it reads the service
database through the same use cases as the API and writes nothing (no relay,
no consumer, no sweeper, no Kafka). ClusterIP only; exposure is the optional
Kong HTTPRoute (mcp-route.yaml).
*/}}
{{- define "delivery-promise.mcpFullname" -}}
{{- include "delivery-promise.fullname" . }}-mcp
{{- end }}

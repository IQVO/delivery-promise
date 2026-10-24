{{/*
Fully qualified name of the frontend Module Federation remote deployment/service.

The remote (web/, `delivery_promise_mfe`) is served by its own nginx pod and
reached through warehouse-infra's Nginx web gateway at /mfes/delivery-promise/.
It is deliberately a separate workload from the API: nothing in this chart
routes to it, and the api Service must never select it (component=frontend
vs component=api). Kept in its own helper file so the frontend component
stays self-contained (templates/frontend-*.yaml + this file + the frontend:
values block).
*/}}
{{- define "delivery-promise.frontendFullname" -}}
{{- include "delivery-promise.fullname" . }}-frontend
{{- end }}

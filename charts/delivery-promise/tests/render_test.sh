#!/usr/bin/env bash
# Chart render assertions for delivery-promise: Postgres and Kafka are the only
# runtime, so the mandatory configuration has no silent default, no
# permissive/off/log/*_MODE is ever rendered, DATABASE_URL comes from a Secret,
# the pod is hardened, the grace period exceeds the binary's shutdown bounds and
# the env cmd/api reads is exactly the env the chart renders.
# Run: bash charts/delivery-promise/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=delivery-promise
group=delivery-promise-consumer
# Dummy values, no password: the chart refuses to render without a database
# source, Kafka brokers and the consumer group. None is ever dialled.
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092
  --set "config.consumerGroup=$group")
# Every optional value set (ct lint's values file), so every env var the binary
# can read is rendered.
everything=(-f "$chart_dir/ci/optional-templates-values.yaml")

fail() { echo "FAIL: $*" >&2; exit 1; }
command -v helm >/dev/null || fail "helm is required"

render() { helm template "$release" "$chart_dir" "$@"; }

configmap="$(render "${base[@]}" --show-only templates/configmap.yaml)"
deployment="$(render "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}

# --- identity -------------------------------------------------------------------
[[ "$(basename "$chart_dir")" == delivery-promise ]] || fail "the chart directory must be delivery-promise"
grep -qE '^name: delivery-promise$' "$chart_dir/Chart.yaml" || fail "Chart.yaml name must be delivery-promise"
grep -q 'charts/delivery-promise' "$repo_dir/.github/workflows/ci.yml" || fail "ci.yml does not reference charts/delivery-promise"
grep -q 'delivery-promise-\${CHART_VERSION}.tgz' "$repo_dir/.github/workflows/ci.yml" || fail "the release job must package delivery-promise-<version>.tgz"

# --- envFrom: the api mounts the chart ConfigMap -----------------------------------
grep -A3 'envFrom:' <<<"$deployment" | grep -q 'configMapRef:' || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$deployment" | grep -q "name: $configmap_name" || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"

# A tuned config.* value reaches the ConfigMap the api mounts and the Deployment env.
tuned=(--set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 --set config.sweepInterval=30s --set config.shutdownDrainDelay=2s --set config.logLevel=debug)
tuned_cm="$(render "${base[@]}" "${tuned[@]}" --show-only templates/configmap.yaml)"
tuned_dep="$(render "${base[@]}" "${tuned[@]}" --show-only templates/deployment.yaml)"
for kv in OUTBOX_RELAY_INTERVAL=7s OUTBOX_MAX_ATTEMPTS=5 SWEEP_INTERVAL=30s SHUTDOWN_DRAIN_DELAY=2s LOG_LEVEL=debug; do
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$tuned_cm" || fail "config value for ${kv%%=*} is not rendered into the ConfigMap"
  [[ "$(env_value "$tuned_dep" "${kv%%=*}")" == "${kv#*=}" ]] || fail "config value for ${kv%%=*} is not rendered into the api Deployment"
done
# The optional ones are absent by default so the binary's own defaults apply.
for var in OUTBOX_RELAY_INTERVAL SWEEP_INTERVAL SHUTDOWN_DRAIN_DELAY; do
  if grep -q "name: $var\$" <<<"$deployment"; then fail "$var must not be rendered while its value is empty"; fi
done
# The relay attempt cap defaults to 20 and 0 (unlimited) is rendered as is.
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
[[ "$(env_value "$deployment" OUTBOX_MAX_ATTEMPTS)" == 20 ]] || fail "api Deployment does not set OUTBOX_MAX_ATTEMPTS=20"
unlimited="$(render "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"

# --- chart vs binary: every env var cmd/api reads is rendered, and only those -------
# The names are the whole-string upper-case literals of cmd/api's non-test files
# (env("X"), os.Getenv("X") and the env... constants), so a new variable in
# cmd/api fails this test until the chart renders it.
binary_env() {
  local f
  for f in "$repo_dir"/cmd/api/*.go; do
    case "$f" in *_test.go) continue ;; esac
    grep -ohE '"[A-Z][A-Z0-9_]*"' "$f" || true
  done | tr -d '"' | sort -u
}
# Word splitting is safe: the names are [A-Z0-9_] only (bash 3.2 has no mapfile).
# shellcheck disable=SC2207
api_vars=($(binary_env))
(( ${#api_vars[@]} >= 11 )) || fail "found only ${#api_vars[@]} env literals in cmd/api; the scan is looking in the wrong place"
rendered_everything="$(render "${everything[@]}" --show-only templates/deployment.yaml)"
for var in "${api_vars[@]}"; do
  grep -q "name: $var\$" <<<"$rendered_everything" || fail "the api Deployment does not render $var, which cmd/api reads"
done
# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys).
full_configmap="$(render "${everything[@]}" --show-only templates/configmap.yaml)"
known=" ${api_vars[*]} "
while read -r key; do
  [[ "$known" == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")
# Every variable cmd/api requires at boot ("X is required") is rendered by default.
required="$(grep -ho 'errors.New("[A-Z_]* is required' "$repo_dir"/cmd/api/config.go | sed -E 's/errors.New\("//; s/ is required//' | sort -u)"
[[ -n "$required" ]] || fail "found no mandatory variables in cmd/api/config.go; the scan is looking in the wrong place"
for var in $required; do
  grep -q "name: $var\$" <<<"$deployment" || fail "the api Deployment does not render $var, which cmd/api requires"
done

# --- the mandatory configuration is rendered from values ----------------------------
[[ "$(env_value "$deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap does not carry KAFKA_BROKERS"
[[ "$(env_value "$deployment" EVENT_PUBLISHER)" == kafka ]] || fail "EVENT_PUBLISHER must be rendered as kafka"
[[ "$(env_value "$deployment" DELIVERY_PROMISE_CONSUMER_GROUP)" == "$group" ]] || fail "DELIVERY_PROMISE_CONSUMER_GROUP is not rendered from config.consumerGroup"
grep -q "^  DELIVERY_PROMISE_CONSUMER_GROUP: \"$group\"" <<<"$configmap" || fail "the ConfigMap does not carry the consumer group"
# The group is stable and service-prefixed in the chart's own values, never a pod name.
[[ "$(env_value "$(render -f "$chart_dir/ci/default-values.yaml" --show-only templates/deployment.yaml)" DELIVERY_PROMISE_CONSUMER_GROUP)" == delivery-promise-* ]] \
  || fail "the CI consumer group must be service-prefixed (delivery-promise-...)"
if grep -E 'DELIVERY_PROMISE_CONSUMER_GROUP' -A2 <<<"$deployment" | grep -qE 'fieldRef|metadata\.name|HOSTNAME'; then fail "the consumer group must never derive from the pod name"; fi

# --- DATABASE_URL: a Secret, never the ConfigMap ------------------------------------
grep -A4 'name: DATABASE_URL$' <<<"$deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
if grep -q 'postgres://' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be an optional secretKeyRef (the binary falls back to DATABASE_URL)"
existing="$(render --set database.existingSecret=my-db --set kafka.brokers=k:9092 --set "config.consumerGroup=$group")"
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "database.existingSecret is not referenced by DATABASE_URL"
if grep -q 'kind: Secret' <<<"$existing"; then fail "the chart must not create a Secret when database.existingSecret is set"; fi

# --- no in-memory / permissive / *_MODE anywhere ------------------------------------
render_all="$(render "${base[@]}" "${everything[@]}")"
if grep -qE 'name: [A-Z0-9_]*_MODE$' <<<"$render_all"; then fail "the render carries a *_MODE variable; delivery-promise has none"; fi
if grep -qiE 'value: "?(permissive|log|off)"?$' <<<"$render_all"; then fail "the render carries permissive/log/off, which the binary rejects at boot"; fi
if grep -qiE 'in-memory|silently runs|permissive' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt mentions an in-memory / permissive mode"
fi

# --- pod hardening and probes ---------------------------------------------------------
for needle in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' 'type: RuntimeDefault'; do
  grep -q "$needle" <<<"$deployment" || fail "the api Deployment lacks '$needle'"
done
grep -A2 'drop:' <<<"$deployment" | grep -q 'ALL' || fail "the api container must drop ALL capabilities"
if grep -qE 'readOnlyRootFilesystem: false|privileged: true|allowPrivilegeEscalation: true' <<<"$deployment"; then fail "the api Deployment weakens the hardening"; fi
grep -A4 'livenessProbe:' <<<"$deployment" | grep -q 'path: /healthz' || fail "liveness must probe /healthz"
grep -A4 'readinessProbe:' <<<"$deployment" | grep -q 'path: /readyz' || fail "readiness must probe /readyz"

# --- shutdown room ---------------------------------------------------------------------
# The grace period must EXCEED drain delay + HTTP drain + consumer + sweeper + relay.
# The bounds are read from cmd/api/config.go so the chart cannot drift from the binary.
secs() { grep -E "^[[:space:]]*$1[[:space:]]*=" "$repo_dir/cmd/api/config.go" | sed -E 's/.*= *([0-9]+) \* time\.Second.*/\1/'; }
drain="$(secs defaultShutdownDrain)"; http="$(secs shutdownTimeout)"; worker="$(secs workerDrainTimeout)"
[[ "$drain" =~ ^[0-9]+$ && "$http" =~ ^[0-9]+$ && "$worker" =~ ^[0-9]+$ ]] || fail "could not read the shutdown bounds from cmd/api/config.go (drain='$drain' http='$http' worker='$worker')"
sum=$(( drain + http + 3 * worker ))   # consumer, sweeper and relay are awaited one after the other
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
expect_refusal "a grace period equal to the shutdown sum ($sum s)" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds="$sum"
expect_refusal "a grace period under the sum" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=20
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
render "${base[@]}" --set terminationGracePeriodSeconds=$(( sum + 1 )) >/dev/null || fail "a grace period of $(( sum + 1 ))s must render"
grace="$(render "${base[@]}" --show-only templates/deployment.yaml | awk '/terminationGracePeriodSeconds:/ {print $2}')"
(( grace > sum )) || fail "the default terminationGracePeriodSeconds ($grace) must exceed the shutdown sum ($sum)"
# A longer drain delay raises the floor.
expect_refusal "a 1m drain delay with a 100s grace period" "terminationGracePeriodSeconds" "${base[@]}" --set config.shutdownDrainDelay=1m --set terminationGracePeriodSeconds=$(( 60 + http + 3 * worker ))
render "${base[@]}" --set config.shutdownDrainDelay=1m --set terminationGracePeriodSeconds=$(( 60 + http + 3 * worker + 1 )) >/dev/null || fail "a 1m drain delay with enough grace must render"
expect_refusal "a malformed drain delay" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=soon

# --- refusals: the mandatory values name themselves --------------------------------------
expect_refusal "no database source" "database.url or database.existingSecret" --set kafka.brokers=k:9092 --set "config.consumerGroup=$group"
expect_refusal "no kafka.brokers" "kafka.brokers" --set database.url=postgres://u@h/db --set "config.consumerGroup=$group"
expect_refusal "no config.consumerGroup" "config.consumerGroup" --set database.url=postgres://u@h/db --set kafka.brokers=k:9092
expect_refusal "an empty config.consumerGroup" "DELIVERY_PROMISE_CONSUMER_GROUP" --set database.url=postgres://u@h/db --set kafka.brokers=k:9092 --set config.consumerGroup=
expect_refusal "the log publisher" "config.eventPublisher" "${base[@]}" --set config.eventPublisher=log
expect_refusal "an unknown publisher" "config.eventPublisher" "${base[@]}" --set config.eventPublisher=nats
expect_refusal "the log publisher through extraEnv" "EVENT_PUBLISHER" "${base[@]}" --set 'extraEnv[0].name=EVENT_PUBLISHER' --set 'extraEnv[0].value=log'
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=STORE_MODE' --set 'extraEnv[0].value=memory'
expect_refusal "a permissive *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=CONSUMER_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "a variable that has a dedicated value, through extraEnv" "DATABASE_URL" "${base[@]}" --set 'extraEnv[0].name=DATABASE_URL' --set 'extraEnv[0].value=postgres://x'
expect_refusal "a malformed sweep interval" "config.sweepInterval" "${base[@]}" --set config.sweepInterval=often
# An unrelated extraEnv entry still renders, once.
extra="$(render "${base[@]}" --set 'extraEnv[0].name=GOMAXPROCS' --set 'extraEnv[0].value=2' --show-only templates/deployment.yaml)"
[[ "$(grep -c 'name: GOMAXPROCS$' <<<"$extra")" == 1 ]] || fail "extraEnv GOMAXPROCS is not rendered exactly once"

# --- one component: no MCP, frontend, projector or reports; optional pieces are off -----
default_render="$(render "${base[@]}")"
[[ "$(grep -c '^kind: Deployment$' <<<"$default_render")" == 1 ]] || fail "the chart must render exactly one Deployment (api)"
[[ "$(grep -c '^kind: Service$' <<<"$default_render")" == 1 ]] || fail "the chart must render exactly one Service (api)"
for kind in HorizontalPodAutoscaler Ingress HTTPRoute; do
  if grep -q "^kind: $kind\$" <<<"$default_render"; then fail "$kind must not render with default values"; fi
done
if grep -qE 'app.kubernetes.io/component: (mcp|frontend|analytics-projector|analytics-reports|projector|reports)' <<<"$render_all"; then
  fail "delivery-promise has only the api component"
fi
optional="$(render "${base[@]}" "${everything[@]}")"
for kind in HorizontalPodAutoscaler Ingress HTTPRoute; do
  grep -q "^kind: $kind\$" <<<"$optional" || fail "$kind does not render when enabled"
done
if grep -q 'replicas:' <<<"$(render "${base[@]}" "${everything[@]}" --show-only templates/deployment.yaml)"; then fail "the Deployment must omit replicas while the HPA owns them"; fi

echo "PASS: delivery-promise chart render assertions"

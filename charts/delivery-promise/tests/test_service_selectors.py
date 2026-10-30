#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates. A Service that selects on those two alone selects
ALL of them, so a request for the api could be answered by another component.
Every Deployment here carries `app.kubernetes.io/component` in
selector.matchLabels and its pod labels, and every Service selects on it, so each
Service selects EXACTLY ONE Deployment. This test fails if that ever stops being
true. delivery-promise has one always-on component (`api`) plus the
optional `mcp` (ADR 0007), `a2a` (ADR 0010) and `frontend` (ADR 0008, the
delivery_promise_mfe remote's nginx pod), all default OFF; the test renders
everything at once, so each Service must still select EXACTLY ONE Deployment.
It also pins the env isolation: the mcp and a2a pods never see
Kafka/relay/consumer/sweeper env, and the frontend pod never sees a database
or Kafka variable.

Run: python3 charts/delivery-promise/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "delivery-promise"
GROUP = "delivery-promise-consumer"

# Dummy values, no password: the chart refuses to render without a database
# source, the Kafka brokers and the consumer group. None is ever dialled.
BASE = [
    "--set", "database.url=postgres://u@example.invalid:5432/db",
    "--set", "kafka.brokers=kafka.example.invalid:9092",
    "--set", f"config.consumerGroup={GROUP}",
]
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "gatewayApi.parentRefs[0].name=example-gateway",
    "--set", "gatewayApi.parentRefs[0].namespace=gateway",
    "--set", "config.outboxRelayInterval=500ms",
    "--set", "config.sweepInterval=15s",
    "--set", "config.shutdownDrainDelay=5s",
    "--set", "mcp.enabled=true",
    "--set", "mcp.route.enabled=true",
    "--set", "mcp.route.parentRefs[0].name=example-gateway",
    "--set", "mcp.route.parentRefs[0].namespace=gateway",
    "--set", "a2a.enabled=true",
    "--set", "a2a.route.enabled=true",
    "--set", "a2a.route.parentRefs[0].name=example-gateway",
    "--set", "a2a.route.parentRefs[0].namespace=gateway",
    "--set", "frontend.enabled=true",
]

# Every variable cmd/api reads; each has a dedicated chart value.
API_ENV = (
    "HTTP_ADDR", "LOG_LEVEL", "EVENT_PUBLISHER", "KAFKA_BROKERS", "DATABASE_URL", "MIGRATIONS_DATABASE_URL",
    "DELIVERY_PROMISE_CONSUMER_GROUP", "OUTBOX_MAX_ATTEMPTS", "OUTBOX_RELAY_INTERVAL", "SWEEP_INTERVAL",
    "SHUTDOWN_DRAIN_DELAY",
)
ALWAYS_ON = {
    "HTTP_ADDR", "LOG_LEVEL", "EVENT_PUBLISHER", "KAFKA_BROKERS", "DATABASE_URL", "MIGRATIONS_DATABASE_URL",
    "DELIVERY_PROMISE_CONSUMER_GROUP", "OUTBOX_MAX_ATTEMPTS",
}
COMPONENT = "app.kubernetes.io/component"
# The MCP server is read-only: no Kafka, no outbox relay, no consumer, no sweeper.
MCP_STRAY = {
    "KAFKA_BROKERS", "DELIVERY_PROMISE_CONSUMER_GROUP", "OUTBOX_RELAY_INTERVAL", "OUTBOX_MAX_ATTEMPTS",
    "SWEEP_INTERVAL", "SHUTDOWN_DRAIN_DELAY", "EVENT_PUBLISHER",
}
# Every env var cmd/mcp reads that has a dedicated chart value.
MCP_ENV = ("MCP_ADDR", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT", "DATABASE_URL", "MIGRATIONS_DATABASE_URL")
# The A2A agent is read-only too (ADR 0010): same stray set as the MCP server.
A2A_STRAY = MCP_STRAY
# Every env var cmd/a2a reads that has a dedicated chart value.
A2A_ENV = ("A2A_ADDR", "A2A_AGENT_URL", "LOG_LEVEL", "SERVICE_VERSION", "ENVIRONMENT", "DATABASE_URL", "MIGRATIONS_DATABASE_URL")


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_of(dep: dict) -> list[dict]:
    return dep["spec"]["template"]["spec"]["containers"][0].get("env", [])


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in env_of(dep)]


def env_value(dep: dict, name: str) -> str | None:
    return next((e.get("value") for e in env_of(dep) if e["name"] == name), None)


def command_of(dep: dict) -> list[str] | None:
    return dep["spec"]["template"]["spec"]["containers"][0].get("command")


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}
    fullname = f"{RELEASE}"

    mcp, a2a, frontend = f"{fullname}-mcp", f"{fullname}-a2a", f"{fullname}-frontend"
    if fullname not in services:
        failures.append("the api Service was not rendered")
    elif selector_of(services[fullname]).get(COMPONENT) != "api":
        failures.append("the api Service selector must pin component=api")
    # mcp (ADR 0007), a2a (ADR 0010) and frontend (ADR 0008) render their own
    # Deployment and Service when enabled (they are in ENABLE_EVERYTHING).
    if set(deployments) != {fullname, mcp, a2a, frontend}:
        failures.append(f"the chart must render exactly the api, mcp, a2a and frontend Deployments, got {sorted(deployments)}")
    if set(services) != {fullname, mcp, a2a, frontend}:
        failures.append(f"the chart must render exactly the api, mcp, a2a and frontend Services, got {sorted(services)}")
    for name, component in ((mcp, "mcp"), (a2a, "a2a"), (frontend, "frontend")):
        if selector_of(services.get(name, {})).get(COMPONENT) != component:
            failures.append(f"the {component} Service selector must pin component={component}")

    # Every Deployment's own selector must pin a component too, and be satisfied
    # by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if COMPONENT not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks {COMPONENT}")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # Ingress and HTTPRoute both target the api Service by name.
    for d in docs:
        if d.get("kind") == "Ingress":
            for rule in d["spec"]["rules"]:
                for path in rule["http"]["paths"]:
                    if path["backend"]["service"]["name"] not in services:
                        failures.append("the Ingress backend is not a Service of this chart")
        if d.get("kind") == "HTTPRoute":
            for rule in d["spec"]["rules"]:
                for ref in rule["backendRefs"]:
                    if ref["name"] not in services:
                        failures.append("the HTTPRoute backend is not a Service of this chart")

    # The api HPA owns replicas when enabled.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != fullname:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(fullname, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    api = deployments.get(fullname)
    if api is None:
        failures.append("the api Deployment was not rendered")
        return
    api_env = env_names(api)
    for want in API_ENV:
        if want not in api_env:
            failures.append(f"the api Deployment does not render {want} from its dedicated value")
    duplicates = sorted({n for n in api_env if api_env.count(n) > 1})
    if duplicates:
        failures.append(f"the api Deployment renders env vars twice: {duplicates}")
    for name, want in (("SWEEP_INTERVAL", "15s"), ("OUTBOX_RELAY_INTERVAL", "500ms"), ("SHUTDOWN_DRAIN_DELAY", "5s")):
        if env_value(api, name) != want:
            failures.append(f"the api Deployment renders {name}={env_value(api, name)!r}, want {want!r}")

    # --- mcp (ADR 0007): its own env, never the api's Kafka/relay/consumer env ---
    mcp_dep = deployments.get(f"{fullname}-mcp")
    if mcp_dep is None:
        failures.append("the mcp Deployment was not rendered")
    else:
        if command_of(mcp_dep) != ["/app/mcp"]:
            failures.append(f"the mcp Deployment must run /app/mcp, got {command_of(mcp_dep)}")
        mcp_env = env_names(mcp_dep)
        for want in MCP_ENV:
            if want not in mcp_env:
                failures.append(f"the mcp Deployment does not render {want}, which cmd/mcp reads")
        stray = sorted(MCP_STRAY & set(mcp_env))
        if stray:
            failures.append(f"the mcp Deployment renders write-side env {stray}: the MCP server is read-only")

    # --- a2a (ADR 0010): its own env, never the api's Kafka/relay/consumer env ---
    a2a_dep = deployments.get(f"{fullname}-a2a")
    if a2a_dep is None:
        failures.append("the a2a Deployment was not rendered")
    else:
        if command_of(a2a_dep) != ["/app/a2a"]:
            failures.append(f"the a2a Deployment must run /app/a2a, got {command_of(a2a_dep)}")
        a2a_env = env_names(a2a_dep)
        for want in A2A_ENV:
            if want not in a2a_env:
                failures.append(f"the a2a Deployment does not render {want}, which cmd/a2a reads")
        stray = sorted(A2A_STRAY & set(a2a_env))
        if stray:
            failures.append(f"the a2a Deployment renders write-side env {stray}: the A2A agent is read-only")
        if env_value(a2a_dep, "A2A_ADDR") != ":8093":
            failures.append(f"the a2a Deployment must listen on :8093, got {env_value(a2a_dep, 'A2A_ADDR')!r}")
        agent_url = env_value(a2a_dep, "A2A_AGENT_URL") or ""
        if not agent_url.startswith("http://") or not agent_url.endswith("-a2a.default.svc.cluster.local:8093/"):
            failures.append(f"the a2a Deployment must advertise its in-cluster Service DNS, got {agent_url!r}")

    # --- frontend (ADR 0008): static nginx, no database, no Kafka ---
    fe_dep = deployments.get(f"{fullname}-frontend")
    if fe_dep is None:
        failures.append("the frontend Deployment was not rendered")
    else:
        fe_env = set(env_names(fe_dep))
        if fe_env & {"DATABASE_URL", "KAFKA_BROKERS", "DELIVERY_PROMISE_CONSUMER_GROUP"}:
            failures.append(f"the frontend Deployment renders database/Kafka env: {sorted(fe_env)}")
    for d in docs:
        if d.get("kind") == "Service" and d["metadata"]["name"] == f"{fullname}-frontend":
            if d["spec"].get("type") != "ClusterIP":
                failures.append("the frontend Service must stay ClusterIP")
        if d.get("kind") == "HTTPRoute" and d["metadata"]["name"] == f"{fullname}-mcp":
            rules = d["spec"]["rules"]
            if not any(f.get("type") == "URLRewrite" for r in rules for f in r.get("filters", [])):
                failures.append("the mcp HTTPRoute must rewrite the host prefix to /mcp")
        if d.get("kind") == "HTTPRoute" and d["metadata"]["name"] == f"{fullname}-a2a":
            targets = [
                f["urlRewrite"]["path"].get("replacePrefixMatch")
                for r in d["spec"]["rules"] for f in r.get("filters", []) if f.get("type") == "URLRewrite"
            ]
            if targets != ["/"]:
                failures.append(f"the a2a HTTPRoute must rewrite the host prefix to /, got {targets}")


def check_refusals(failures: list[str]) -> None:
    def runtime(skip: str | None = None) -> list[str]:
        args: list[str] = []
        if skip != "database":
            args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
        if skip != "kafka":
            args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
        if skip != "group":
            args += ["--set", f"config.consumerGroup={GROUP}"]
        return args

    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("without config.consumerGroup", runtime("group"), "config.consumerGroup is required"),
        ("with an empty config.consumerGroup", runtime("group") + ["--set", "config.consumerGroup="], "config.consumerGroup is required"),
        ("the log publisher", BASE + ["--set", "config.eventPublisher=log"], "the only publisher is"),
        ("an unknown publisher", BASE + ["--set", "config.eventPublisher=nats"], "the only publisher is"),
        ("EVENT_PUBLISHER=log through extraEnv",
         BASE + ["--set", "extraEnv[0].name=EVENT_PUBLISHER", "--set", "extraEnv[0].value=log"], "extraEnv sets EVENT_PUBLISHER=log"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=STORE_MODE", "--set", "extraEnv[0].value=memory"], "extraEnv sets STORE_MODE"),
        ("a grace period under the shutdown sum", BASE + ["--set", "terminationGracePeriodSeconds=45"], "terminationGracePeriodSeconds"),
        ("a malformed drain delay", BASE + ["--set", "config.shutdownDrainDelay=soon"], "config.shutdownDrainDelay"),
        ("a malformed sweep interval", BASE + ["--set", "config.sweepInterval=often"], "config.sweepInterval"),
    ]
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)

    # Default values must not deploy an HPA, an Ingress or a route, and must
    # render exactly one Deployment and one Service.
    defaults = render(BASE)
    stray = [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional components rendered with default values: {stray}")
    kinds = [d["kind"] for d in defaults]
    if kinds.count("Deployment") != 1 or kinds.count("Service") != 1:
        failures.append(f"default values must render one Deployment and one Service, got {kinds}")
    dep = next(d for d in defaults if d.get("kind") == "Deployment")
    names = env_names(dep)
    off = {"OUTBOX_RELAY_INTERVAL", "SWEEP_INTERVAL", "SHUTDOWN_DRAIN_DELAY"}
    if off & set(names):
        failures.append(f"these must be off with default values: {sorted(off & set(names))}")
    if env_value(dep, "EVENT_PUBLISHER") != "kafka":
        failures.append(f"EVENT_PUBLISHER must always be rendered as kafka, got {env_value(dep, 'EVENT_PUBLISHER')!r}")
    for want in sorted(ALWAYS_ON):
        if want not in names:
            failures.append(f"{want} is mandatory and must always be rendered")
    if any(n.endswith("_MODE") for n in names):
        failures.append("no *_MODE variable may be rendered")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: every Service selects exactly one Deployment (api); HPA, Ingress and HTTPRoute are off by default; "
          "every cmd/api env var has a dedicated value; the a2a agent is isolated like the mcp server; the chart refuses to render without a database source, brokers "
          "or the consumer group, with a log/unknown publisher or a *_MODE variable, with a grace period under the "
          "shutdown sum, or with a malformed duration")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

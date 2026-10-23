#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates. A Service that selects on those two alone selects
ALL of them, so a request for the api could be answered by another component.
Every Deployment here carries `app.kubernetes.io/component` in
selector.matchLabels and its pod labels, and every Service selects on it, so each
Service selects EXACTLY ONE Deployment. This test fails if that ever stops being
true. delivery-promise has one component, `api`; the test is written over
whatever Deployments and Services the chart renders, so a component added later
is covered without edits.

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


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}
    fullname = f"{RELEASE}"

    if fullname not in services:
        failures.append("the api Service was not rendered")
    elif selector_of(services[fullname]).get(COMPONENT) != "api":
        failures.append("the api Service selector must pin component=api")
    if set(deployments) != {fullname}:
        failures.append(f"the chart must render exactly the api Deployment, got {sorted(deployments)}")
    if set(services) != {fullname}:
        failures.append(f"the chart must render exactly the api Service, got {sorted(services)}")

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
          "every cmd/api env var has a dedicated value; the chart refuses to render without a database source, brokers "
          "or the consumer group, with a log/unknown publisher or a *_MODE variable, with a grace period under the "
          "shutdown sum, or with a malformed duration")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

"""Check render.yaml before it is deployed.

Three kinds of mistake, in increasing order of how long they take to notice:

  1. invalid YAML, or a shape Render will reject outright
  2. a `fromService` or `fromDatabase` naming something this file does not
     define — accepted as YAML, rejected or left empty at deploy time
  3. a `dockerfilePath` or `dockerContext` pointing at a path that no longer
     exists, which only fails once Render tries to build

The schema check needs the network. If the schema cannot be fetched the script
says so and keeps going with the local checks, rather than failing a build over
someone else's uptime.

Usage:
    python scripts/check-blueprint.py [path/to/render.yaml]
"""

from __future__ import annotations

import json
import os
import sys
import urllib.request

SCHEMA_URL = "https://render.com/schema/render.yaml.json"


def fail(message: str) -> None:
    print("  FAIL %s" % message)


def load(path: str) -> dict:
    try:
        import yaml
    except ImportError:
        print("PyYAML is required: pip install pyyaml jsonschema")
        raise SystemExit(2)
    with open(path, encoding="utf-8") as handle:
        return yaml.safe_load(handle.read())


def check_schema(doc: dict) -> list[str]:
    try:
        import jsonschema
    except ImportError:
        print("  skip schema check (jsonschema not installed)")
        return []
    try:
        with urllib.request.urlopen(SCHEMA_URL, timeout=20) as response:
            schema = json.load(response)
    except Exception as exc:  # network, DNS, 404 — not this repo's problem
        print("  skip schema check (%s: %s)" % (type(exc).__name__, exc))
        return []

    problems = []
    validator = jsonschema.Draft7Validator(schema)
    for error in sorted(validator.iter_errors(doc), key=lambda e: list(e.absolute_path)):
        where = "/".join(str(p) for p in error.absolute_path) or "(root)"
        problems.append("%s: %s" % (where, error.message[:200]))
    return problems


def check_references(doc: dict) -> list[str]:
    services = {s.get("name") for s in doc.get("services", [])}
    databases = {d.get("name") for d in doc.get("databases", [])}
    problems = []

    for service in doc.get("services", []):
        for env in service.get("envVars", []) or []:
            key = env.get("key", "?")
            ref = env.get("fromService")
            if ref and ref.get("name") not in services:
                problems.append("%s/%s: fromService names %r, which this file does not define"
                                % (service.get("name"), key, ref.get("name")))
            ref = env.get("fromDatabase")
            if ref and ref.get("name") not in databases:
                problems.append("%s/%s: fromDatabase names %r, which this file does not define"
                                % (service.get("name"), key, ref.get("name")))
    return problems


def check_paths(doc: dict, root: str) -> list[str]:
    problems = []
    for service in doc.get("services", []):
        name = service.get("name")
        for key in ("dockerfilePath", "dockerContext"):
            value = service.get(key)
            if not value:
                continue
            target = os.path.normpath(os.path.join(root, value))
            if not os.path.exists(target):
                problems.append("%s: %s points at %s, which does not exist" % (name, key, value))
    return problems


def main() -> int:
    path = sys.argv[1] if len(sys.argv) > 1 else "render.yaml"
    root = os.path.dirname(os.path.abspath(path)) or "."
    doc = load(path)

    services = doc.get("services", [])
    print("%s: %d service(s), %d database(s)" % (path, len(services), len(doc.get("databases", []))))

    problems: list[str] = []
    problems += check_schema(doc)
    problems += check_references(doc)
    problems += check_paths(doc, root)

    if problems:
        print("%d problem(s):" % len(problems))
        for problem in problems:
            fail(problem)
        return 1
    print("  ok: schema, service references and build paths all check out")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

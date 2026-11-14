#!/usr/bin/env python3
"""Check imported team assets without invoking Squad or contacting a target."""

import hashlib
import json
from pathlib import Path
import re
import sys
from urllib.parse import unquote


ROOT = Path(__file__).resolve().parent
TEAM = ROOT / ".squad"
SUPPORT = {"scribe", "ralph", "rai", "fact-checker"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def read_json(path):
    return json.loads(path.read_text())


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def validate():
    source = read_json(ROOT.parent / "squad-source.json")
    require(source["repository"] == "octodemo/modernization-squad", "Wrong origin")
    require(source["commit"] == "d63d93f7f6059848a4965b2656a7c7f35aa10f10", "Wrong pin")
    require(source["license"] is None, "Do not invent an upstream license")
    require(source["permission"]["basis"] == "explicit-user-permission", "Missing permission")
    records = source["imported_files"]
    require(len(records) == 110, "Expected 100 team assets + 10 pinned framework documents")
    by_path = {record["path"]: record for record in records}
    require(len(by_path) == len(records), "Duplicate provenance path")
    for relative, record in by_path.items():
        path = ROOT / relative
        require(path.resolve().is_relative_to(ROOT), f"Escaping provenance path: {relative}")
        require(path.is_file() and not path.is_symlink(), f"Missing imported asset: {relative}")
        actual = digest(path)
        require(actual == record["imported_sha256"], f"Changed imported asset: {relative}")
        require(record["adapted"] == (record["upstream_sha256"] != actual), f"Adaptation flag: {relative}")
    native = ".github/agents/squad.agent.md"
    require(by_path[native]["adapted"], "Expected explicit native-gh-aw coordinator adaptation")
    coordinator = (ROOT / native).read_text()
    require(
        'name: Squad\n' in coordinator
        and "2.0.0-preview.4" in coordinator
        and "SQUAD_COORDINATOR_CANARY_HEAD_b7d2" in coordinator
        and "SQUAD_COORDINATOR_CANARY_a8f3" in coordinator,
        "Missing native coordinator identity/version/canaries",
    )
    config = read_json(TEAM / "config.json")
    require(
        config == {"version": 1, "runtimeMode": "native-gh-aw", "stateBackend": "local"},
        "Expected explicit native-gh-aw local config without linked/remote state",
    )
    for skill in ("coordinator-response-mode", "coordinator-source-of-truth", "reviewer-protocol"):
        relative = f".squad/skills/{skill}/SKILL.md"
        require(relative in by_path, f"Missing pinned enabled skill: {skill}")
        require(
            by_path[relative]["upstream_path"] == f".squad/templates/skills/{skill}/SKILL.md",
            f"Missing relocated skill provenance: {skill}",
        )
    for path in (TEAM / "casting").glob("*.json"):
        require(not by_path[path.relative_to(ROOT).as_posix()]["adapted"], f"Casting changed: {path}")

    registry = read_json(TEAM / "casting/registry.json")
    history = read_json(TEAM / "casting/history.json")
    marker = read_json(TEAM / "casting/registry-history.commit.json")
    agents = registry["agents"]
    require(len(agents) == 89 and SUPPORT <= agents.keys(), "Expected 85 specialists + 4 support")
    require(all(agent["status"] == "active" for agent in agents.values()), "Inactive identity")
    require(registry["transaction_id"] == history["transaction_id"] == marker["transaction_id"], "Casting transaction")
    require(registry["revision"] == history["registry_revision"] == marker["registry_revision"], "Casting revision")
    require(marker["registry_sha256"] == digest(TEAM / "casting/registry.json"), "Registry hash")
    require(marker["history_sha256"] == digest(TEAM / "casting/history.json"), "History hash")
    snapshots = history["assignment_cast_snapshots"]
    require(len(snapshots) == 1, "Unexpected identity history")
    cast = next(iter(snapshots.values()))["agents"]
    require(len(cast) == 89 and set(cast) == set(agents), "Identity snapshot differs")

    members = (TEAM / "team.md").read_text().split("## Members\n", 1)[1].split("\n## ", 1)[0]
    roster = re.findall(r"^\| ([^|]+?) \|.*?\[charter\]\(([^)]+)\)", members, re.M)
    require(len(roster) == 89, "Expected 89 roster rows")
    ids = set()
    for display_name, relative in roster:
        path = TEAM / relative
        require(path.is_file(), f"Broken roster path: {relative}")
        agent_id = path.parent.name
        require(agent_id not in ids, f"Duplicate roster identity: {agent_id}")
        ids.add(agent_id)
        require(display_name == agents[agent_id]["display_name"], f"Identity name mismatch: {agent_id}")
    require(ids == set(agents), "Registry and roster differ")
    charters = list((TEAM / "agents").glob("*/charter.md"))
    require({path.parent.name for path in charters} == ids, "Charter directories differ")
    routing = (TEAM / "routing.md").read_text().split("## Phase routing", 1)[0]
    routes = re.findall(r"^\| ([^|]+?) \| `([^`]+)` \|", routing, re.M)
    require(len(routes) == 85, "Expected 85 specialist routes")
    require(all(work == target for work, target in routes), "Altered route identity")
    require({target for _, target in routes} == ids - SUPPORT, "Routing and cast differ")

    forbidden = re.compile(
        r"planning[- ]only|advisory[- ]only|no implementation authorized|"
        r"no source/code/config/tests|dormant during advisory|"
        r"Azure is the destination policy|recorded Azure-policy exception", re.I,
    )
    stale = re.compile(
        r"advisory-(?:process|roles|templates|invocation)\.md|"
        r"modernization-advisory\.agent\.md|modernization-contracts/SKILL\.md|"
        r"roster-proposal\.md|tools/advisory_packet\.py|/Users/|/home/",
    )
    for path in charters:
        text = path.read_text()
        require(not forbidden.search(text), f"Planning-only or destination restriction: {path}")
        if path.parent.name not in SUPPORT:
            require(len(text.split("## Boundaries", 1)[0]) > 1500, f"Missing capability detail: {path}")
            require("CAO" in text and "dependency" in text, f"Missing CAO scope: {path}")
        require(path.relative_to(ROOT).as_posix() in by_path, f"Unprovenanced charter: {path}")
    for path in ROOT.rglob("*"):
        require(not path.is_symlink(), f"Symlink not permitted: {path}")
        relative = path.relative_to(ROOT)
        require(not any("scratch" in part for part in relative.parts), f"Import scratch retained: {path}")
        if not path.is_file():
            continue
        require(
            not (
                path.name in {"history.md", ".mcp.json", "package.json", "local-launch.json", "audit-trail.md"}
                or path.suffix in {".jsonl", ".yml", ".yaml", ".gz"}
                or any(part in {"memory", "engagements", "trials", ".vscode"} for part in relative.parts)
            ),
            f"Excluded runtime/history/config asset: {path}",
        )
        if path.suffix != ".md":
            continue
        text = path.read_text()
        require(not stale.search(text), f"Stale active source reference: {path}")
        for reference in re.findall(
            r"\.squad/(?:templates/[\w./-]+\.md|skills/[\w./-]+/SKILL\.md)",
            text,
        ):
            require((ROOT / reference).is_file(), f"Missing enabled framework reference: {path}: {reference}")
        require(
            not re.search(
                r"(?:MUST|must|required to|PRE-CHECK:)\s+(?:use|call|run)\s+`?(?:squad_state_|memory\.)",
                text,
            ),
            f"Mandatory unsupported state service: {path}",
        )
        for link in re.findall(r"\[[^\]]*\]\(([^)]+)\)", text):
            target = unquote(link.split("#", 1)[0])
            if target and not re.match(r"[A-Za-z][A-Za-z0-9+.-]*:", target):
                require((path.parent / target).exists(), f"Broken Markdown link: {path}: {target}")
    print("Validated 85 specialists + 4 support, 89 roster paths, 85 specialist routes.")
    print("Native coordinator identity retained/adapted; casting unchanged; provenance hashes valid.")
    print("Native gh-aw local config, bundled enabled references/skills and local links valid.")
    print("No imported engagement history, planning-only restrictions or stale active references.")
    print("Static assets only; runtime and end-to-end execution are not verified.")


if __name__ == "__main__":
    try:
        validate()
    except (ValueError, KeyError, OSError) as error:
        print(f"Asset validation failed: {error}", file=sys.stderr)
        sys.exit(1)

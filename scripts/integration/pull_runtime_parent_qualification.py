"""Register existing owned native parents through actual App preparation paths."""

import copy
import importlib
from urllib.parse import urlsplit


STAGES = {
    "install-plan": ("nvke_install_plans", "plan", "plan_type"),
    "install-plan-charts": ("nvke_install_plan_charts", "charts", "chart_type"),
    "install-plan-tree": ("nvke_install_plan_trees", "tree", "tree_type"),
    "install-plan-context": (
        "nvke_install_plan_contexts",
        "context",
        "context_type",
    ),
    "install-plan-image": ("nvke_install_plan_images", "image", "image_type"),
    "install-plan-workload-inventory": (
        "nvke_install_plan_workloads",
        "workload",
        "workload_type",
    ),
    "install-plan-image-binding": (
        "nvke_install_plan_image_bindings",
        "binding",
        "image_binding_type",
    ),
    "install-plan-node-platforms": (
        "nvke_install_plan_node_platforms",
        "nodePlatforms",
        "artifact_type",
    ),
    "install-plan-pull-authority": (
        "nvke_install_plan_pull_authority",
        "pullAuthority",
        "artifact_type",
    ),
    "install-plan-pull-secret-analysis": (
        "nvke_install_plan_pull_secret_analysis",
        "analysis",
        "artifact_type",
    ),
}


def _public(value):
    from dataclasses import asdict, is_dataclass
    from datetime import datetime
    from uuid import UUID

    if is_dataclass(value):
        return _public(asdict(value))
    if isinstance(value, dict):
        return {
            key.split("_")[0]
            + "".join(part.title() for part in key.split("_")[1:]): _public(item)
            for key, item in value.items()
        }
    if isinstance(value, (list, tuple)):
        return [_public(item) for item in value]
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, UUID):
        return str(value)
    return value


def register_pull_runtime_parents(*, history, review):
    """Call within the real caller/token context and a fresh owned test database.

    Native records must already exist on the selected agent. This registers their
    exact retained requests through the normal App APIs, including real SDK/mTLS
    calls. It does not qualify original installation dispatch or live Kubernetes.
    """
    records = copy.deepcopy(history)
    original_review = copy.deepcopy(review)
    public, seen = {}, {}
    source = dict(review)
    for record in records:
        if record["method"] != "POST":
            continue
        route = urlsplit(record["path"]).path.rsplit("/", 1)[-1]
        if route not in STAGES:
            continue
        module_name, name, converter_name = STAGES[route]
        body = record["body"]
        artifact_id = body["planId"] if name == "plan" else body["artifactId"]
        key = (route, artifact_id)
        if key in seen:
            assert seen[key] == record
            continue
        assert record["path"].startswith(
            "/v1/jobs/" + str(review["operation_id"]) + "/"
        )
        values = dict(review)
        if name != "plan":
            values.update({key: source[key] for key in PLAN_FIELDS})
        if name not in {"plan", "charts"}:
            values.update({key: source[key] for key in CHART_FIELDS})
        if name not in {"plan", "charts", "tree"}:
            values.update({key: source[key] for key in TREE_FIELDS})
        if name == "context":
            values.update(item=body["item"], ordinal=record["result"]["ordinal"])
        if name in {"binding", "nodePlatforms", "pullAuthority", "analysis"} or (
            name == "image" and "workloadId" in body
        ):
            values.update({key: source[key] for key in WORKLOAD_FIELDS})
        if name in {"nodePlatforms", "pullAuthority", "analysis"}:
            values.update({key: source[key] for key in BINDING_FIELDS})
        if name == "analysis":
            values.update({key: source[key] for key in PULL_FIELDS})
        module = importlib.import_module("astrolift_clusters." + module_name)
        result = module.prepare(body=copy.deepcopy(body), **values)
        assert result["id"] == artifact_id and result["status"] == "prepared"
        assert result["metadata"] == record["result"]
        schema = importlib.import_module("astrolift_clusters.schema." + module_name)
        public[name] = _public(getattr(schema, converter_name)(result))
        seen[key] = record
        if name == "plan":
            source.update(
                plan_id=result["id"],
                expected_plan_version=result["version"],
                plan_digest=result["metadata"]["plan"]["planDigest"],
            )
        if name in PINS:
            prefix, digest_field = PINS[name]
            source.update(
                {
                    prefix + "_id": result["id"],
                    "expected_" + prefix + "_version": result["version"],
                    prefix + "_digest": result["metadata"][digest_field],
                }
            )
    assert set(public) >= {
        "plan",
        "charts",
        "tree",
        "workload",
        "binding",
        "nodePlatforms",
        "pullAuthority",
        "analysis",
    }
    assert review == original_review and history == records
    return public, source


PLAN_FIELDS = ("plan_id", "expected_plan_version", "plan_digest")
CHART_FIELDS = (
    "chart_artifact_id",
    "expected_chart_artifact_version",
    "chart_artifact_digest",
)
TREE_FIELDS = ("tree_id", "expected_tree_version", "tree_digest")
WORKLOAD_FIELDS = ("workload_id", "expected_workload_version", "workload_digest")
BINDING_FIELDS = ("binding_id", "expected_binding_version", "binding_digest")
PULL_FIELDS = (
    "pull_authority_id",
    "expected_pull_authority_version",
    "pull_authority_digest",
)
PINS = {
    "charts": ("chart_artifact", "artifactDigest"),
    "tree": ("tree", "treeDigest"),
    "workload": ("workload", "workloadDigest"),
    "binding": ("binding", "bindingDigest"),
    "pullAuthority": ("pull_authority", "authorityDigest"),
}

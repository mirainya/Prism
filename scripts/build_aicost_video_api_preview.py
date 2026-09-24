"""Render a read-only admin API preview from the verified AICost video plan.

No requests or database operations are performed. The onboarding template is
deliberately incomplete where the redacted catalog snapshot lacks required data.
"""

import copy
import json
import argparse
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
PROPOSAL = ROOT / ".temp/aicost_video_proposed_plan_20260924.json"
SNAPSHOT = ROOT / ".temp/prism_live_video_catalog_snapshot_20260923.json"
BASE = "/api/admin/unified-gateway"
FAST_MODEL = "seedance2.0-900-fast"
FAST_REFERENCE = "seedance2.0-900-3"


def load_json(path):
    with path.open(encoding="utf-8") as source:
        return json.load(source)


def make_fast_template(proposal, snapshot, expected_version):
    missing = [row for row in proposal["not_created"]
               if row["target"]["model"] == FAST_MODEL]
    reference = [row for row in proposal["changes"]
                 if row["model"] == FAST_REFERENCE]
    entries = [row for row in snapshot["entries"]
               if any(product["vendor_model"] == FAST_REFERENCE
                      for product in row["products"])]
    if len(missing) != 1 or len(reference) != 1 or len(entries) != 1:
        raise ValueError("missing or ambiguous seedance2.0-900-fast reference")
    target = missing[0]["target"]
    sibling = reference[0]["proposed_product"]
    sibling_entry = entries[0]
    sibling_product = next(product for product in sibling_entry["products"]
                           if product["vendor_model"] == FAST_REFERENCE)
    sibling_sku = sibling_entry["skus"][0]
    if (target["group"] != reference[0]["group"] or
            missing[0]["credential_pool_candidates"] != [sibling["pool_code"]] or
            sibling["entitled_credential_count"] < 1 or
            len(sibling_entry["skus"]) != 1):
        raise ValueError("seedance2.0-900-fast reference pool is not verified")
    constraints = copy.deepcopy(sibling["capability_constraints"])
    models = constraints["adapter"]["validation"]["models"]
    if list(models) != [FAST_REFERENCE]:
        raise ValueError("reference has unexpected model-specific validation")
    models[FAST_MODEL] = models.pop(FAST_REFERENCE)
    if (constraints["max_images"], constraints["max_videos"],
            constraints["max_audios"], constraints["duration_options"]) != (
            target["images"], target["videos"], target["audios"], target["seconds"]):
        raise ValueError("reference capability does not match fast model")
    rate = reference[0]["cost_rates"][0]["old"]
    if (rate["unit_code"], rate["pricing_mode"], rate["quantity_source"]) != (
            target["unit"], "flat", "one"):
        raise ValueError("reference rate is incompatible with fast model")
    rate_body = {field: rate[field] for field in (
        "unit_code", "component_code", "quantity_source", "charge_event",
        "unit_scale", "quantity_step", "max_quantity", "pricing_mode",
        "pricing_expr")}
    rate_body.update(unit_price=target["price"], reason_code="aicost_snapshot_20260924")
    code = "aicost-seedance-2-0-900-fast"
    body = {
        "expected_active_release_id": proposal["release"]["id"],
        "expected_config_version": expected_version,
        "sku": {
            "model_code": FAST_MODEL, "api_name": FAST_MODEL,
            "display_name": FAST_MODEL,
            "description": missing[0]["proposed_public_description"],
            "visibility": "visible", "capability_tags": sibling_sku["capability_tags"],
            "operation_code": sibling_sku["operation_code"],
            "contract_version": sibling_sku["contract_version"],
            "http_method": sibling_sku["http_method"],
            "route_template": sibling_sku["route_template"],
            "normalization_version": None,
            "sku_code": "seedance2.0-900-fast-standard",
            "variant_code": sibling_sku["variant_code"],
            "delivery_mode": sibling_sku["delivery_mode"],
            "max_results": sibling_sku["max_results"],
            "idempotency_mode": sibling_sku["idempotency_mode"],
            "service_tiers": sibling_sku["service_tiers"],
        },
        "product": {
            "channel_id": sibling["channel_id"],
            "credential_pool_id": sibling["credential_pool_id"],
            "product_code": code + "-upstream", "vendor_model": FAST_MODEL,
            "capability_constraints": constraints,
            "constraints_schema_version": sibling["constraints_schema_version"],
            "adapter_code": sibling["adapter_code"],
            "adapter_version": sibling["adapter_version"],
            "transport_code": code + "-transport",
            "base_url": sibling_product["base_url"],
            "request_method": sibling["request_method"],
            "request_path": sibling["request_path"],
            "auth_scheme": None, "transport_timeout_ms": None,
            "task_timeout_ms": None, "task_scope": sibling["task_scope"],
            "cancel_mode": sibling["cancel_mode"],
            "source_url_policy": sibling["source_url_policy"],
            "upstream_scope_kind": None, "upstream_scope_key": None,
            "allowed_hosts": None, "actions": None,
            "cost_plan_code": code + "-cost",
        },
        "route": {"priority": sibling_product["routes"][0]["priority"],
                  "weight": sibling_product["routes"][0]["weight"]},
        "sell_rate": copy.deepcopy(rate_body), "cost_rate": copy.deepcopy(rate_body),
    }
    return {
        "ready": False,
        "path": BASE + "/catalog-changes/model-onboard",
        "body_template": body,
        "missing_required_fields": [
            "sku.normalization_version", "product.auth_scheme",
            "product.transport_timeout_ms", "product.task_timeout_ms",
            "product.upstream_scope_kind", "product.upstream_scope_key",
            "product.allowed_hosts", "product.actions",
        ],
        "required_live_checks": [
            "active release config_version", "pool entitlement and token group",
            "settlement currency and price unit", "product and public name uniqueness",
        ],
    }


def build_preview(proposal, snapshot):
    release = proposal["release"]
    if release != snapshot["release"] or release["id"] == 0 or release["config_version"] == 0:
        raise ValueError("proposal and Prism snapshot release differ")
    changes = proposal["changes"]
    if len(changes) != 31 or len(proposal["not_created"]) != 4:
        raise ValueError("unexpected AICost video model partition")
    if any(row["blockers"] for row in changes):
        raise ValueError("existing product has an unresolved blocker")
    seen_models = set()
    seen_public = set()
    for row in changes:
        identities = row["public_identity"]
        if (row["model"] in seen_models or len(identities) != 1 or
                len(identities[0]["sku_api_names"]) != 1 or
                identities[0]["model_code"] != identities[0]["sku_api_names"][0] or
                identities[0]["model_code"] in seen_public or
                row["old_product"]["vendor_model"] != row["model"] or
                row["proposed_product"]["vendor_model"] != row["model"] or
                len(row["cost_rates"]) != 1 or len(row["sell_rates"]) != 1):
            raise ValueError(f"ambiguous product or public identity: {row['model']}")
        seen_models.add(row["model"])
        seen_public.add(identities[0]["model_code"])
    if any(row["model"] in seen_public for row in changes):
        raise ValueError("target public name already exists in the rename set")
    other_public = {row["model_code"] for row in snapshot["entries"]
                    if row["model_code"] not in seen_public}
    if any(row["model"] in other_public for row in changes):
        raise ValueError("target public name is already used outside the rename set")

    version = release["config_version"]
    steps = []

    def add_step(kind, path, body):
        nonlocal version
        body = {"expected_active_release_id": release["id"],
                "expected_config_version": version, **body}
        version += 1
        steps.append({"kind": kind, "method": "POST", "path": BASE + path,
                      "body": body, "config_version_after": version})

    for row in changes:
        old, proposed = row["old_product"], row["proposed_product"]
        if ({key: value for key, value in old.items()
             if key not in ("capability_constraints", "offering_state")} !=
                {key: value for key, value in proposed.items()
                 if key not in ("capability_constraints", "offering_state")}):
            raise ValueError(f"unsupported product field change: {row['model']}")
        if old["capability_constraints"] != proposed["capability_constraints"]:
            add_step("product", "/catalog-changes/product", {
                "product_code": old["product_code"], "vendor_model": row["model"],
                "capability_constraints": proposed["capability_constraints"],
            })
    for row in changes:
        for kind, rates, path in (
                ("cost_rate", row["cost_rates"], "/catalog-changes/cost-rate"),
                ("sell_rate", row["sell_rates"], "/catalog-changes/sell-rate")):
            for rate in rates:
                if not rate["changed"]:
                    continue
                old, new = rate["old"], rate["proposed"]
                if ({key: value for key, value in old.items() if key != "unit_price"} !=
                        {key: value for key, value in new.items() if key != "unit_price"}):
                    raise ValueError(f"unsupported rate field change: {row['model']}")
                if (old["pricing_mode"] != "flat" or new["pricing_mode"] != "flat" or
                        old["unit_code"] != new["unit_code"] or
                        old["component_code"] != new["component_code"]):
                    raise ValueError(f"rate shape changed for {row['model']}")
                body = {"unit_price": new["unit_price"], "pricing_mode": "flat",
                        "reason_code": "aicost_snapshot_20260924",
                        "component_code": old["component_code"]}
                if kind == "cost_rate":
                    body.update(product_code=row["old_product"]["product_code"],
                                plan_code=row["old_product"]["cost_plan_code"],
                                pool_code=row["old_product"]["pool_code"])
                else:
                    body["sku_code"] = old["parent_key"]
                add_step(kind, path, body)

    renames = []
    for row in changes:
        old_name = row["public_identity"][0]["model_code"]
        new_name = row["model"]
        renames.append({"from_api_name": old_name, "to_api_name": new_name,
                        "display_name": new_name,
                        "description": row["proposed_public_description"]})
    if len(renames) > 32:
        raise ValueError("public identity batch exceeds API limit")
    add_step("public_identities", "/catalog-changes/public-model-identities", {
        "reason_code": "aicost_actual_model_names", "renames": renames})

    activations = []
    for row in changes:
        old = row["old_product"]
        if old["offering_state"] != "active":
            activations.append({
                "model": row["model"], "method": "PATCH",
                "path": BASE + f"/offerings/{old['offering_id']}/runtime-state",
                "body": {"state": "active", "reason_code": "aicost_video_catalog_20260924",
                         "expected_version": old["offering_state_version"]},
            })
    if (len(steps) != 38 or len(activations) != 4 or version != release["config_version"] + 38):
        raise ValueError("unexpected existing-model change counts")
    return {
        "mode": "offline_preview_only", "network_or_database_writes": False,
        "snapshot_captured_at": proposal["snapshot_captured_at"],
        "release": release, "currency_assumption": "1 CREDIT per quoted yuan; verify live",
        "expected_config_version_after_existing_changes": version,
        "existing_change_steps": steps,
        "activate_after_deployment_and_verification": activations,
        "incomplete_onboard_template": make_fast_template(proposal, snapshot, version),
        "not_ready_for_onboarding": [row["target"]["model"] for row in proposal["not_created"]
                                   if row["target"]["model"] != FAST_MODEL],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, help="write preview JSON locally")
    args = parser.parse_args()
    preview = build_preview(load_json(PROPOSAL), load_json(SNAPSHOT))
    encoded = json.dumps(preview, ensure_ascii=False, indent=2) + "\n"
    if args.output is None:
        print(encoded, end="")
    else:
        args.output.write_text(encoded, encoding="utf-8")


if __name__ == "__main__":
    main()

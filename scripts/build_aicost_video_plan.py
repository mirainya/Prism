"""Build a read-only AICost video catalog change plan from local snapshots.

The result is a proposal, not an executable migration. In particular, a model
listed by AICost has not necessarily been tested with its assigned Token pool.
"""

import argparse
import copy
import json
from decimal import Decimal, InvalidOperation
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
MEDIA = (("images", "image_url", "images"),
         ("videos", "video_url", "videos"),
         ("audios", "audio_url", "audios"))


def load_json(path):
    with Path(path).open(encoding="utf-8") as source:
        return json.load(source)


def unique_map(rows, key, label):
    result = {}
    for row in rows:
        value = row.get(key)
        if not isinstance(value, str) or not value or value in result:
            raise ValueError(f"invalid or duplicate {label} {key}: {value!r}")
        result[value] = row
    return result


def price(value, label):
    try:
        number = Decimal(str(value))
    except (InvalidOperation, TypeError) as error:
        raise ValueError(f"invalid {label} price: {value!r}") from error
    if not number.is_finite() or number < 0:
        raise ValueError(f"invalid {label} price: {value!r}")
    return number


def normalized_price(value):
    return format(value.normalize(), "f")


def pricing_groups(value):
    if isinstance(value, str):
        return value.split()
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return value
    raise ValueError("AICost pricing groups must be a string or string list")


def proposed_description(target, expected_price):
    minimum, maximum, options = desired_duration(target)
    duration = ("、".join(str(value) for value in options) if options is not None
                else f"{minimum}-{maximum}")
    unit = "秒" if target["unit"] == "second" else "次"
    return (f"AICost {target['group']}；最多 {target['images']} 图、"
            f"{target['videos']} 视频、{target['audios']} 音频参考；"
            f"支持 {duration} 秒；{normalized_price(expected_price)} 元/{unit}。")


def desired_duration(target):
    if "seconds" in target:
        options = target["seconds"]
        if (not isinstance(options, list) or not options or
                any(type(value) is not int or value < 1 for value in options) or
                options != sorted(set(options))):
            raise ValueError(f"invalid duration options for {target['model']}")
        return min(options), max(options), options
    minimum = target.get("seconds_min")
    maximum = target.get("seconds_max")
    if (type(minimum) is not int or type(maximum) is not int or
            minimum < 1 or maximum < minimum):
        raise ValueError(f"invalid duration bounds for {target['model']}")
    return minimum, maximum, None


def update_projections(request, target, blockers):
    projections = request.setdefault("content_projections", [])
    if not isinstance(projections, list):
        blockers.append("adapter.request.content_projections is not a list")
        return
    for field, content_type, default_target in MEDIA:
        matching = [item for item in projections if content_type in item.get("types", [])]
        if target[field] == 0:
            for item in matching:
                if item.get("types") == [content_type]:
                    projections.remove(item)
                else:
                    blockers.append(f"shared {content_type} projection needs manual review")
            continue
        if any(item.get("source") == "url" and item.get("output") == "array"
               for item in matching):
            continue
        if matching:
            # Cached AICost /api-docs specifies audios: string[] for this model.
            verified_minimax_audio = (
                target["model"] == "minimax-h3-768p-930-8-22" and
                field == "audios" and len(matching) == 1 and
                matching[0].get("types") == ["audio_url"] and
                matching[0].get("source") == "url" and
                matching[0].get("output") == "scalar" and
                matching[0].get("target") == "audio_reference" and
                matching[0].get("index", 0) == 0 and
                not matching[0].get("roles") and not matching[0].get("models"))
            if not verified_minimax_audio:
                blockers.append(f"{content_type} projection is not a URL array")
                continue
            projections.remove(matching[0])
        if any(item.get("target") == default_target for item in projections):
            blockers.append(f"projection target {default_target} is already used")
            continue
        projections.append({"types": [content_type], "output": "array",
                            "source": "url", "target": default_target})


def update_constraints(old, target, blockers):
    proposed = copy.deepcopy(old)
    adapter = proposed.get("adapter")
    if not isinstance(adapter, dict):
        blockers.append("missing generic adapter configuration")
        return proposed
    validation = adapter.get("validation", {}).get("models", {}).get(target["model"])
    if not isinstance(validation, dict):
        blockers.append("missing model-specific generic validation")
        return proposed
    for field, _, _ in MEDIA:
        value = target[field]
        if type(value) is not int or value < 0:
            raise ValueError(f"invalid {field} limit for {target['model']}")
        key = "max_" + field
        proposed[key] = value
        validation[key] = value
    minimum, maximum, options = desired_duration(target)
    for configured in (proposed, validation):
        configured["duration_min"] = minimum
        configured["duration_max"] = maximum
        if options is None:
            configured.pop("duration_options", None)
        else:
            configured["duration_options"] = options[:]
    references = any(target[field] > 0 for field, _, _ in MEDIA)
    proposed["task_types"] = ["text", "multimodal"] if references else ["text"]
    validation["task_modes"] = ["text", "references"] if references else ["text"]
    request = adapter.get("request")
    if not isinstance(request, dict):
        blockers.append("missing generic request configuration")
    else:
        update_projections(request, target, blockers)
    return proposed


def rate_plan(rows, expected_unit, expected_price, label, blockers):
    if not rows:
        blockers.append(f"missing {label} rate")
    result = []
    for row in rows:
        old = copy.deepcopy(row)
        proposed = copy.deepcopy(row)
        if row.get("pricing_mode") != "flat" or row.get("unit_code") != expected_unit:
            blockers.append(f"{label} rate {row.get('id')} has incompatible pricing mode or unit")
        else:
            if price(row.get("unit_price"), f"{label} rate {row.get('id')}") != expected_price:
                proposed["unit_price"] = normalized_price(expected_price)
        result.append({"old": old, "proposed": proposed,
                       "changed": proposed != old})
    return result


def build_plan(target_data, prism, inventory):
    for endpoint in ("/api/pricing", "/api/user/models"):
        if inventory.get("statuses", {}).get(endpoint) != 200:
            raise ValueError(f"incomplete AICost snapshot: {endpoint}")
    targets = unique_map(target_data["models"], "model", "target")
    pricing = unique_map(inventory["pricing"], "model_name", "pricing")
    listed = set(inventory["models"])
    products = {}
    for product in prism["products"]:
        products.setdefault(product["vendor_model"], []).append(product)
    costs = {}
    for rate in prism["costs"]:
        costs.setdefault(rate["parent_id"], []).append(rate)
    sells = {}
    for rate in prism["sells"]:
        sells.setdefault(rate["parent_id"], []).append(rate)
    entries_by_product = {}
    pool_codes_by_name = {}
    for entry in prism["entries"]:
        for product in entry.get("products", []):
            entries_by_product.setdefault(product["id"], []).append(entry)
            pool_codes_by_name.setdefault(product.get("pool_name"), set()).add(product["pool_code"])

    changes = []
    not_created = []
    for name, target in sorted(targets.items()):
        blockers = []
        if name not in listed:
            blockers.append("model absent from AICost user model list")
        upstream = pricing.get(name)
        if upstream is None:
            blockers.append("model absent from AICost pricing")
            upstream_price = None
        else:
            upstream_price = price(upstream["model_price"], f"AICost {name}")
            if target["group"] not in pricing_groups(upstream.get("enable_groups", [])):
                blockers.append("target group absent from AICost pricing groups")
        table_price = price(target["price"], f"target {name}")
        if upstream_price is not None and table_price != upstream_price:
            blockers.append("target price differs from AICost snapshot; snapshot price proposed")
        expected_price = upstream_price if upstream_price is not None else table_price
        description = proposed_description(target, expected_price)
        existing = products.get(name, [])
        if not existing:
            candidates = sorted(pool_codes_by_name.get(target["group"], set()))
            if not candidates:
                blockers.append("no Prism credential pool mapping for target group in snapshot")
            not_created.append({"target": target, "aicost_listed": name in listed,
                                "aicost_pricing": upstream,
                                "proposed_public_description": description,
                                "credential_pool_candidates": candidates,
                                "blockers": blockers + ["no Prism product; no configuration inferred"]})
            continue
        for product in existing:
            product_blockers = blockers[:]
            old_constraints = product.get("capability_constraints")
            if not isinstance(old_constraints, dict):
                product_blockers.append("missing product capability constraints")
                old_constraints = {}
            proposed_constraints = update_constraints(old_constraints, target, product_blockers)
            entries = entries_by_product.get(product["id"], [])
            sku_ids = {sku["id"] for entry in entries for sku in entry.get("skus", [])
                       if any(item["id"] == product["id"] and
                              sku["id"] in item.get("sku_ids", [])
                              for item in entry.get("products", []))}
            if not sku_ids:
                product_blockers.append("no SKU route found for product")
            cost_rows = costs.get(product["cost_plan_id"], [])
            sell_rows = [rate for sku_id in sorted(sku_ids) for rate in sells.get(sku_id, [])]
            old_product = copy.deepcopy(product)
            proposed_product = copy.deepcopy(product)
            proposed_product["capability_constraints"] = proposed_constraints
            proposed_product["offering_state"] = "active"
            identity = [{"model_code": entry["model_code"],
                         "description": entry.get("description", ""),
                         "sku_api_names": [sku["api_name"] for sku in entry.get("skus", [])
                                           if sku["id"] in sku_ids]}
                        for entry in entries]
            changes.append({
                "model": name,
                "document_name": target.get("document_name"),
                "group": target["group"],
                "product_id": product["id"],
                "offering_id": product["offering_id"],
                "public_identity": identity,
                "proposed_public_identity": {"model_code": name, "sku_api_name": name},
                "proposed_public_description": description,
                "identity_needs_review": any(
                    item["model_code"] != name or any(api != name for api in item["sku_api_names"])
                    for item in identity),
                "old_product": old_product,
                "proposed_product": proposed_product,
                "cost_rates": rate_plan(cost_rows, target["unit"], expected_price,
                                        "cost", product_blockers),
                "sell_rates": rate_plan(sell_rows, target["unit"], expected_price,
                                        "sell", product_blockers),
                "blockers": product_blockers,
            })
    channel_ids = {row["old_product"]["channel_id"] for row in changes}
    outside = sorted(({"model": product["vendor_model"], "product_id": product["id"],
                       "offering_id": product["offering_id"],
                       "offering_state": product["offering_state"]}
                      for product in prism["products"]
                      if product.get("channel_id") in channel_ids and
                      product.get("protocol") == "video_generation" and
                      product["vendor_model"] not in targets),
                     key=lambda row: (row["model"], row["product_id"]))
    return {
        "scope": "offline proposal only; no database, network or Token call",
        "snapshot_captured_at": {"prism": prism.get("captured_at"),
                                 "aicost": inventory.get("captured_at")},
        "release": prism.get("release"),
        "summary": {"target_models": len(targets), "existing_products": len(changes),
                    "not_created": len(not_created),
                    "capability_changes": sum(
                        row["old_product"]["capability_constraints"] !=
                        row["proposed_product"]["capability_constraints"]
                        for row in changes),
                    "offering_activations": sum(
                        row["old_product"]["offering_state"] != "active" for row in changes),
                    "cost_price_changes": sum(rate["changed"] for row in changes
                                              for rate in row["cost_rates"]),
                    "sell_price_changes": sum(rate["changed"] for row in changes
                                              for rate in row["sell_rates"]),
                    "public_identity_changes": sum(row["identity_needs_review"]
                                                   for row in changes),
                    "blocked_products": sum(bool(row["blockers"]) for row in changes),
                    "outside_target_active": sum(row["offering_state"] == "active" for row in outside)},
        "changes": changes,
        "not_created": not_created,
        "outside_target": outside,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", type=Path,
                        default=ROOT / ".temp/aicost_video_target_20260924.json")
    parser.add_argument("--prism", type=Path,
                        default=ROOT / ".temp/prism_live_video_catalog_snapshot_20260923.json")
    parser.add_argument("--aicost", type=Path,
                        default=ROOT / ".temp/aicost_inventory_snapshot_20260924.json")
    parser.add_argument("--output", type=Path,
                        help="write proposal JSON locally; default is stdout")
    args = parser.parse_args()
    plan = build_plan(load_json(args.target), load_json(args.prism), load_json(args.aicost))
    encoded = json.dumps(plan, ensure_ascii=False, indent=2) + "\n"
    if args.output is None:
        print(encoded, end="")
    else:
        args.output.write_text(encoded, encoding="utf-8")


if __name__ == "__main__":
    main()

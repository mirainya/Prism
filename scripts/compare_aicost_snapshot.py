"""Compare a local AICost inventory snapshot with a transcribed Feishu reference.

Offline and read-only. Presence in the user model list does not prove a group's
Token can call the model; this tool intentionally makes no such assertion.
"""

import argparse
import json
from decimal import Decimal, InvalidOperation
from pathlib import Path


def load_json(path):
    with Path(path).open(encoding="utf-8") as source:
        return json.load(source)


def unique_map(rows, key, label):
    result = {}
    for row in rows:
        name = row[key]
        if not isinstance(name, str) or not name:
            raise ValueError(f"invalid {label} model name: {name!r}")
        if name in result:
            raise ValueError(f"duplicate {label} model name: {name}")
        result[name] = row
    return result


def compare(reference, snapshot):
    statuses = snapshot.get("statuses", {})
    for endpoint in ("/api/pricing", "/api/user/models"):
        if statuses and statuses.get(endpoint) != 200:
            raise ValueError(f"incomplete snapshot: {endpoint} returned {statuses.get(endpoint)!r}")
    video = unique_map(reference["video"], "model", "reference video")
    images = unique_map(({"model": name} for name in reference["image"]), "model", "reference image")
    overlap = video.keys() & images.keys()
    if overlap:
        raise ValueError(f"duplicate reference model across categories: {sorted(overlap)}")
    pricing = unique_map(snapshot["pricing"], "model_name", "snapshot pricing")
    model_list = snapshot["models"]
    if not isinstance(model_list, list) or not all(isinstance(name, str) for name in model_list):
        raise ValueError("snapshot models must be a list of names")
    visible = set(model_list)
    names = video.keys() | images.keys()
    differences = []
    for name, expected in sorted(video.items()):
        actual = pricing.get(name)
        if actual is None:
            continue
        try:
            expected_price = Decimal(str(expected["price_cny"]))
            actual_price = Decimal(str(actual["model_price"]))
        except (InvalidOperation, KeyError) as error:
            raise ValueError(f"invalid price for {name}") from error
        if not expected_price.is_finite() or not actual_price.is_finite():
            raise ValueError(f"non-finite price for {name}")
        if expected_price != actual_price:
            differences.append({"model": name, "document": str(expected_price), "snapshot": str(actual_price)})
    return {
        "reference_count": len(names),
        "missing_from_pricing": sorted(names - pricing.keys()),
        "missing_from_user_models": sorted(names - visible),
        "price_differences": differences,
    }


def compare_prism(reference, prism):
    """Compare the document with a local Prism catalog export, including disabled rows."""
    video = unique_map(reference["video"], "model", "reference video")
    names = set(video) | set(reference["image"])
    products = prism["products"]
    costs = prism["costs"]
    by_model = {}
    for product in products:
        by_model.setdefault(product["vendor_model"], []).append(product)
    by_product = {}
    for rate in costs:
        by_product.setdefault(rate["product_id"], []).append(rate)

    outside = [
        {"model": product["vendor_model"], "product_id": product["id"],
         "offering_id": product["offering_id"], "state": product["offering_state"]}
        for product in products if product["vendor_model"] not in names
    ]
    outside.sort(key=lambda item: (item["model"], item["product_id"], item["offering_id"]))
    disabled = sorted(name for name in names if name in by_model and
                      all(row["offering_state"] != "active" for row in by_model[name]))
    price_differences = []
    unit_mismatches = []
    missing_costs = []
    capability_differences = []
    field_names = {
        "duration_options": "duration_options",
        "duration_min_seconds": "duration_min",
        "duration_max_seconds": "duration_max",
        "max_images": "max_images",
        "max_videos": "max_videos",
        "max_audios": "max_audios",
    }
    for name, expected in sorted(video.items()):
        for product in by_model.get(name, []):
            constraints = product.get("capability_constraints") or {}
            model_validation = (((constraints.get("adapter") or {}).get("validation") or {})
                                .get("models") or {}).get(name) or {}
            for source, configured in (("public", constraints), ("adapter", model_validation)):
                for document_field, config_field in field_names.items():
                    if document_field not in expected:
                        continue
                    document_value = expected[document_field]
                    actual_value = configured.get(config_field)
                    if actual_value != document_value:
                        capability_differences.append({
                            "model": name, "product_id": product["id"], "source": source,
                            "field": config_field, "document": document_value,
                            "prism": actual_value,
                        })
            request = (constraints.get("adapter") or {}).get("request") or {}
            projections = request.get("content_projections") or []
            media_fields = (("max_images", "image_url", "images"),
                            ("max_videos", "video_url", "videos"),
                            ("max_audios", "audio_url", "audios"))
            for limit_field, content_type, target in media_fields:
                if limit_field not in expected or expected[limit_field] <= 0:
                    continue
                # include_content emits an array of objects at content_path, not
                # URL arrays at images/videos/audios. A dedicated projection is
                # still needed, even when include_content is enabled.
                mapped = any(
                    projection.get("target") == target and
                    projection.get("source") == "url" and
                    projection.get("output") == "array" and
                    projection.get("types") == [content_type] and
                    not projection.get("roles") and
                    (not projection.get("models") or name in projection["models"])
                    for projection in projections
                )
                if not mapped:
                    capability_differences.append({
                        "model": name, "product_id": product["id"], "source": "adapter.request",
                        "field": f"content_projections.{target}",
                        "document": f"{content_type} URL array", "prism": None,
                    })
            rates = by_product.get(product["id"], [])
            if not rates:
                missing_costs.append({"model": name, "product_id": product["id"]})
            for rate in rates:
                entry = {"model": name, "product_id": product["id"],
                         "offering_id": rate["offering_id"]}
                if rate["unit_code"] != expected["unit"]:
                    unit_mismatches.append({**entry, "document": expected["unit"],
                                            "prism": rate["unit_code"]})
                    continue
                if rate["pricing_mode"] != "flat":
                    raise ValueError(f"unsupported cost pricing mode for {name}")
                try:
                    document_price = Decimal(str(expected["price_cny"]))
                    prism_price = Decimal(str(rate["unit_price"]))
                except (InvalidOperation, KeyError) as error:
                    raise ValueError(f"invalid cost price for {name}") from error
                if not document_price.is_finite() or not prism_price.is_finite():
                    raise ValueError(f"non-finite cost price for {name}")
                if document_price != prism_price:
                    price_differences.append({**entry, "document": str(document_price),
                                              "prism": str(prism_price)})
    return {
        "prism_product_count": len(products),
        "outside_document_products": outside,
        "missing_from_prism": sorted(names - by_model.keys()),
        "disabled_in_prism": disabled,
        "cost_price_differences": price_differences,
        "cost_unit_mismatches": unit_mismatches,
        "missing_costs": missing_costs,
        "capability_differences": capability_differences,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reference", type=Path, help="local normalized Feishu reference JSON")
    parser.add_argument("snapshot", type=Path, help="local AICost snapshot JSON")
    parser.add_argument("prism", nargs="?", type=Path, help="optional local Prism AICost catalog JSON")
    args = parser.parse_args()
    reference = load_json(args.reference)
    result = compare(reference, load_json(args.snapshot))
    if args.prism is not None:
        result["prism"] = compare_prism(reference, load_json(args.prism))
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()

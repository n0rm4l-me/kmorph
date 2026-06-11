#!/usr/bin/env python3
"""
Remove 'type: object' from the 'patch' field in ClusterProfile CRD.
This allows the field to accept both JSON objects (merge/strategic patch)
and JSON arrays (RFC 6902 JSON patch).
"""
import re
import sys

CRD = "config/crd/bases/config.kmorph.io_clusterprofiles.yaml"

with open(CRD) as f:
    content = f.read()

# Match the pattern:
#   type: object
#   x-kubernetes-preserve-unknown-fields: true
# only where preceded by the patch field description lines.
# We use a negative-lookbehind-style approach: find 'type: object\n' immediately
# followed by 'x-kubernetes-preserve-unknown-fields: true' and remove it.
patched = re.sub(
    r"( +)type: object\n(\1x-kubernetes-preserve-unknown-fields: true)",
    r"\2",
    content,
)

if patched == content:
    print(f"WARNING: {CRD}: pattern not found, CRD may already be patched or format changed", file=sys.stderr)
else:
    with open(CRD, "w") as f:
        f.write(patched)
    print(f"Patched {CRD}: removed 'type: object' before x-kubernetes-preserve-unknown-fields")

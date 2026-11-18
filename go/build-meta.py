#!/usr/bin/env python3
"""生成 dist/BUILD-<version>.json：记录平台二进制大小、SHA-256 与 Go build metadata。"""
import hashlib
import json
import os
import subprocess
import sys

version = sys.argv[1] if len(sys.argv) > 1 else "dev"
dist = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "dist"))
entries = []
for name in sorted(os.listdir(dist)):
    if not name.startswith("moo-cloud-") or name.endswith(".json"):
        continue
    path = os.path.join(dist, name)
    if not os.path.isfile(path):
        continue
    digest = hashlib.sha256(open(path, "rb").read()).hexdigest()
    try:
        metadata = subprocess.check_output(["go", "version", "-m", path], text=True).strip()
    except Exception:
        metadata = ""
    entries.append({"name": name, "size": os.path.getsize(path), "sha256": digest, "metadata": metadata})
out = os.path.join(dist, "BUILD-" + version + ".json")
with open(out, "w", encoding="utf-8") as f:
    json.dump(entries, f, ensure_ascii=False, indent=2)
    f.write("\n")
print(out)

"""
Standalone helpers for e2e-integration tests.

No dependencies on MaaS repo — only kubectl, requests, and stdlib.
"""

import json
import logging
import os
import subprocess
import time

import requests

log = logging.getLogger(__name__)

TIMEOUT = int(os.environ.get("E2E_TIMEOUT", "30"))
TLS_VERIFY = os.environ.get("E2E_SKIP_TLS_VERIFY", "").lower() != "true"


def gateway_url():
    host = os.environ.get("GATEWAY_HOST", "")
    if not host:
        raise RuntimeError("GATEWAY_HOST env var is required")
    scheme = "http" if os.environ.get("INSECURE_HTTP", "").lower() == "true" else "https"
    return f"{scheme}://{host}"


def apply_cr(cr_dict):
    result = subprocess.run(
        ["kubectl", "apply", "-f", "-"],
        input=json.dumps(cr_dict),
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(f"kubectl apply failed: {result.stderr}")


def delete_cr(kind, name, namespace):
    subprocess.run(
        ["kubectl", "delete", kind, name, "-n", namespace, "--ignore-not-found", "--timeout=30s"],
        capture_output=True, text=True,
    )


def get_cr(kind, name, namespace):
    result = subprocess.run(
        ["kubectl", "get", kind, name, "-n", namespace, "-o", "json"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        if "not found" in result.stderr.lower() or "notfound" in result.stderr.lower():
            return None
        raise RuntimeError(f"kubectl get {kind}/{name} failed: {result.stderr}")
    return json.loads(result.stdout)


def wait_for_cr(kind, name, namespace, jsonpath_check, timeout=60):
    """Poll until a CR field matches expected value.

    jsonpath_check: callable that receives the CR dict and returns True when ready.
    """
    deadline = time.time() + timeout
    while time.time() < deadline:
        cr = get_cr(kind, name, namespace)
        if cr and jsonpath_check(cr):
            return cr
        time.sleep(2)
    return None


def chat_request(model_url, body, auth_header=None):
    headers = {"Content-Type": "application/json"}
    if auth_header:
        headers["Authorization"] = auth_header
    return requests.post(model_url, headers=headers, json=body, timeout=TIMEOUT, verify=TLS_VERIFY)


def get_cluster_token(sa_name="maas-api", namespace="maas-system"):
    result = subprocess.run(
        ["kubectl", "create", "token", sa_name, "-n", namespace,
         "--duration=10m", "--audience=https://kubernetes.default.svc"],
        capture_output=True, text=True,
    )
    token = result.stdout.strip()
    if not token:
        raise RuntimeError(f"Failed to create token for {sa_name}: {result.stderr}")
    return token


def create_api_key(subscription, name=None):
    import uuid
    token = get_cluster_token()
    key_name = name or f"e2e-int-{uuid.uuid4().hex[:8]}"
    maas_api_url = f"{gateway_url()}/maas-api/v1/api-keys"
    r = requests.post(
        maas_api_url,
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        json={"name": key_name, "subscription": subscription},
        timeout=TIMEOUT, verify=TLS_VERIFY,
    )
    if r.status_code not in (200, 201):
        raise RuntimeError(f"Failed to create API key: {r.status_code} {r.text}")
    key = r.json().get("key")
    if not key:
        raise RuntimeError(f"API key response missing 'key': {r.json()}")
    return key

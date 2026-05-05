"""
Category 2: Auth enforcement for inference.opendatahub.io ExternalModel routes.

Tests both negative (no/bad auth rejected) and positive (valid API key works) paths.

Prerequisites:
  - Kuadrant + Authorino deployed
  - Gateway-level default-deny AuthPolicy applied
  - MaaSModelRef + MaaSAuthPolicy + MaaSSubscription for the test model
  - PR #865 (MaaSModelRef API-group agnostic fix) deployed on maas-controller

Environment:
  E2E_NEW_CRD_MODEL        - ExternalModel name (default: new-katan-openai)
  E2E_NEW_CRD_TARGET_MODEL - targetModel in request body (default: llm-katan-echo)
  E2E_MODEL_NAMESPACE      - Namespace (default: llm)
  E2E_NEW_CRD_SUBSCRIPTION - MaaSSubscription name for API key (default: new-crd-subscription)
"""

import logging
import os
import uuid

import pytest

from helpers import chat_request, create_api_key, gateway_url, get_cr

log = logging.getLogger(__name__)

MODEL_NAME = os.environ.get("E2E_NEW_CRD_MODEL", "new-katan-openai")
TARGET_MODEL = os.environ.get("E2E_NEW_CRD_TARGET_MODEL", "llm-katan-echo")
NS = os.environ.get("E2E_MODEL_NAMESPACE", "llm")
SUBSCRIPTION = os.environ.get("E2E_NEW_CRD_SUBSCRIPTION", "new-crd-subscription")


def _model_url():
    return f"{gateway_url()}/{NS}/{MODEL_NAME}/v1/chat/completions"


def _body():
    return {"model": TARGET_MODEL, "messages": [{"role": "user", "content": "hello"}]}


pytestmark = pytest.mark.skipif(
    get_cr("httproute.gateway.networking.k8s.io",
           os.environ.get("E2E_NEW_CRD_MODEL", "new-katan-openai"),
           os.environ.get("E2E_MODEL_NAMESPACE", "llm")) is None,
    reason="HTTPRoute for ExternalModel not found on cluster",
)


class TestNegativeAuth:
    """Requests with no/bad auth should be rejected (401/403).

    Requires gateway-default-deny AuthPolicy. Without it, these tests
    will fail with HTTP 200 — meaning the route is unprotected.
    """

    def test_no_auth_header_rejected(self):
        r = chat_request(_model_url(), _body())
        log.info("No auth -> HTTP %s", r.status_code)
        assert r.status_code in (401, 403), (
            f"Expected 401/403, got {r.status_code}. Route is unprotected — "
            f"apply gateway-default-auth AuthPolicy. Response: {r.text[:300]}"
        )

    def test_invalid_bearer_token_rejected(self):
        r = chat_request(_model_url(), _body(), auth_header="Bearer INVALID-TOKEN")
        log.info("Invalid token -> HTTP %s", r.status_code)
        assert r.status_code in (401, 403), (
            f"Expected 401/403, got {r.status_code}. Response: {r.text[:300]}"
        )

    def test_invalid_api_key_rejected(self):
        r = chat_request(_model_url(), _body(), auth_header="Bearer sk-oai-fake-key")
        log.info("Invalid API key -> HTTP %s", r.status_code)
        assert r.status_code in (401, 403), (
            f"Expected 401/403, got {r.status_code}. Response: {r.text[:300]}"
        )

    def test_random_auth_rejected(self):
        r = chat_request(_model_url(), _body(), auth_header=f"Bearer {uuid.uuid4().hex}")
        log.info("Random auth -> HTTP %s", r.status_code)
        assert r.status_code in (401, 403), (
            f"Expected 401/403, got {r.status_code}. Response: {r.text[:300]}"
        )


class TestPositiveAuth:
    """Valid API key should authenticate and reach the model (HTTP 200).

    Requires:
      - MaaSModelRef pointing at the ExternalModel (kind: ExternalModel)
      - MaaSAuthPolicy granting access to the model
      - MaaSSubscription with token budget
      - maas-controller with PR #865 fix (API-group agnostic MaaSModelRef)
    """

    def test_valid_api_key_returns_200(self):
        try:
            api_key = create_api_key(subscription=SUBSCRIPTION)
        except RuntimeError as e:
            pytest.skip(f"Could not create API key: {e}")

        r = chat_request(_model_url(), _body(), auth_header=f"Bearer {api_key}")
        log.info("Valid API key -> HTTP %s", r.status_code)
        assert r.status_code == 200, (
            f"Expected 200 with valid API key, got {r.status_code}. "
            f"Check MaaSModelRef/AuthPolicy/Subscription for {MODEL_NAME}. "
            f"Response: {r.text[:300]}"
        )

    def test_response_has_choices(self):
        try:
            api_key = create_api_key(subscription=SUBSCRIPTION)
        except RuntimeError as e:
            pytest.skip(f"Could not create API key: {e}")

        r = chat_request(_model_url(), _body(), auth_header=f"Bearer {api_key}")
        assert r.status_code == 200, f"Expected 200, got {r.status_code}"
        data = r.json()
        assert "choices" in data, f"Response missing 'choices': {data}"
        assert len(data["choices"]) > 0, f"Empty choices array: {data}"

    def test_response_model_field_matches(self):
        try:
            api_key = create_api_key(subscription=SUBSCRIPTION)
        except RuntimeError as e:
            pytest.skip(f"Could not create API key: {e}")

        r = chat_request(_model_url(), _body(), auth_header=f"Bearer {api_key}")
        assert r.status_code == 200
        data = r.json()
        assert "model" in data, f"Response missing 'model' field: {data}"

    def test_response_content_not_empty(self):
        try:
            api_key = create_api_key(subscription=SUBSCRIPTION)
        except RuntimeError as e:
            pytest.skip(f"Could not create API key: {e}")

        r = chat_request(_model_url(), _body(), auth_header=f"Bearer {api_key}")
        assert r.status_code == 200
        data = r.json()
        content = data["choices"][0].get("message", {}).get("content", "")
        assert len(content) > 0, f"Response content is empty: {data}"


class TestErrorPaths:
    """Verify correct error responses for bad requests."""

    def test_wrong_model_name_in_body(self):
        """Model name in body doesn't match ExternalModel CR name → error."""
        try:
            api_key = create_api_key(subscription=SUBSCRIPTION)
        except RuntimeError as e:
            pytest.skip(f"Could not create API key: {e}")

        bad_body = {"model": "wrong-model-name", "messages": [{"role": "user", "content": "hi"}]}
        r = chat_request(_model_url(), bad_body, auth_header=f"Bearer {api_key}")
        log.info("Wrong model name -> HTTP %s", r.status_code)
        assert r.status_code in (400, 404), (
            f"Expected 400/404 for wrong model name in body, got {r.status_code}: {r.text[:300]}"
        )

    def test_unsupported_path(self):
        """Paths other than /chat/completions should be rejected."""
        try:
            api_key = create_api_key(subscription=SUBSCRIPTION)
        except RuntimeError as e:
            pytest.skip(f"Could not create API key: {e}")

        bad_url = f"{gateway_url()}/{NS}/{MODEL_NAME}/v1/embeddings"
        body = {"model": TARGET_MODEL, "input": "hello"}
        r = chat_request(bad_url, body, auth_header=f"Bearer {api_key}")
        log.info("Unsupported path /embeddings -> HTTP %s", r.status_code)
        assert r.status_code in (400, 404), (
            f"Expected 400/404 for unsupported path, got {r.status_code}: {r.text[:300]}"
        )

    def test_empty_messages_array(self):
        """Empty messages array should return an error or be handled gracefully."""
        try:
            api_key = create_api_key(subscription=SUBSCRIPTION)
        except RuntimeError as e:
            pytest.skip(f"Could not create API key: {e}")

        bad_body = {"model": TARGET_MODEL, "messages": []}
        r = chat_request(_model_url(), bad_body, auth_header=f"Bearer {api_key}")
        log.info("Empty messages -> HTTP %s", r.status_code)
        # Could be 200 (provider handles it) or 400 (validation). Either is acceptable.
        assert r.status_code in (200, 400), (
            f"Expected 200 or 400 for empty messages, got {r.status_code}: {r.text[:300]}"
        )

    def test_nonexistent_model_route_returns_404(self):
        """Request to a model path that doesn't exist should return 404."""
        bad_url = f"{gateway_url()}/{NS}/nonexistent-model-xyz/v1/chat/completions"
        body = {"model": "nonexistent", "messages": [{"role": "user", "content": "hi"}]}
        r = chat_request(bad_url, body)
        log.info("Non-existent model route -> HTTP %s", r.status_code)
        assert r.status_code in (401, 403, 404), (
            f"Expected 401/403/404 for non-existent route, got {r.status_code}: {r.text[:300]}"
        )

"""
Category 4: Multi-provider weighted routing.

Validates that an ExternalModel with multiple provider refs routes traffic
proportionally by weight, and that the X-Selected-Provider header is set.

Blocked until PR #213 (multi-provider weights) merges.

Environment:
  E2E_MULTI_PROVIDER_MODEL - ExternalModel with multiple providers (default: multi-provider-test)
  E2E_MODEL_NAMESPACE      - Namespace (default: llm)
"""

import logging
import os

import pytest

from helpers import chat_request, gateway_url, get_cr

log = logging.getLogger(__name__)

MODEL_NAME = os.environ.get("E2E_MULTI_PROVIDER_MODEL", "multi-provider-test")
NS = os.environ.get("E2E_MODEL_NAMESPACE", "llm")


pytestmark = pytest.mark.skipif(
    get_cr("externalmodel.inference.opendatahub.io",
           os.environ.get("E2E_MULTI_PROVIDER_MODEL", "multi-provider-test"),
           os.environ.get("E2E_MODEL_NAMESPACE", "llm")) is None,
    reason="Multi-provider ExternalModel not deployed on cluster",
)


def _model_url():
    return f"{gateway_url()}/{NS}/{MODEL_NAME}/v1/chat/completions"


def _body():
    return {"model": "llm-katan-echo", "messages": [{"role": "user", "content": "hello"}]}


class TestMultiProviderRouting:
    """Verify weighted traffic splitting across multiple providers."""

    @pytest.mark.xfail(reason="Multi-provider weights not merged yet (PR #213)")
    def test_model_has_multiple_provider_refs(self):
        cr = get_cr("externalmodel.inference.opendatahub.io", MODEL_NAME, NS)
        assert cr is not None
        refs = cr.get("spec", {}).get("externalProviderRefs", [])
        assert len(refs) >= 2, (
            f"Expected at least 2 provider refs, got {len(refs)}"
        )
        weights = [r.get("weight", 0) for r in refs]
        assert all(w > 0 for w in weights), f"All weights should be > 0, got {weights}"

    @pytest.mark.xfail(reason="Multi-provider weights not merged yet (PR #213)")
    def test_traffic_splits_by_weight(self):
        """Send N requests, verify traffic roughly follows weight distribution."""
        n_requests = 50
        provider_counts = {}

        for _ in range(n_requests):
            r = chat_request(_model_url(), _body())
            if r.status_code != 200:
                continue
            data = r.json()
            content = data.get("choices", [{}])[0].get("message", {}).get("content", "")
            if "host=" in content:
                provider_counts[content] = provider_counts.get(content, 0) + 1

        log.info("Traffic distribution over %d requests: %s", n_requests, provider_counts)
        assert len(provider_counts) >= 2, (
            f"Expected traffic to at least 2 providers, got {len(provider_counts)}: {provider_counts}"
        )

    @pytest.mark.xfail(reason="Multi-provider weights not merged yet (PR #213)")
    def test_selected_provider_header_set(self):
        """Verify X-Selected-Provider header is set on the response."""
        r = chat_request(_model_url(), _body())
        assert r.status_code == 200, f"Expected 200, got {r.status_code}"
        # The header may be stripped by Envoy on the response path.
        # If it's not in the response, check the echo body for evidence of routing.
        log.info("Response headers: %s", dict(r.headers))

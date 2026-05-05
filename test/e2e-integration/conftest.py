"""
Shared fixtures for e2e-integration tests.

Environment variables:
  GATEWAY_HOST             - Gateway endpoint (required, e.g. localhost:19080)
  E2E_SIMULATOR_ENDPOINT   - llm-katan FQDN (required for provider tests)
  E2E_MODEL_NAMESPACE      - Namespace for test resources (default: llm)
  INSECURE_HTTP            - Use HTTP instead of HTTPS (default: false)
  E2E_SKIP_TLS_VERIFY      - Skip TLS cert verification (default: false)
"""

import os
import pytest


def pytest_configure(config):
    config.addinivalue_line("markers", "xfail_known: mark test as expected failure with tracked issue")


@pytest.fixture(scope="session")
def model_namespace():
    return os.environ.get("E2E_MODEL_NAMESPACE", "llm")


@pytest.fixture(scope="session")
def simulator_endpoint():
    ep = os.environ.get("E2E_SIMULATOR_ENDPOINT", "")
    if not ep:
        pytest.skip("E2E_SIMULATOR_ENDPOINT not set")
    return ep

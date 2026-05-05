"""
Category 1: CRD reconciler correctness.

Validates that the BBR ExternalModel controller creates the expected
networking resources when ExternalProvider + ExternalModel CRs are deployed.

Tests against pre-deployed resources on the cluster — does not create/delete CRs.

Environment:
  E2E_NEW_CRD_MODEL       - ExternalModel name to test (default: new-katan-openai)
  E2E_NEW_CRD_PROVIDER    - ExternalProvider name (default: katan-openai-provider)
  E2E_MODEL_NAMESPACE      - Namespace (default: llm)
"""

import logging
import os

import pytest

from helpers import get_cr

log = logging.getLogger(__name__)

MODEL_NAME = os.environ.get("E2E_NEW_CRD_MODEL", "new-katan-openai")
PROVIDER_NAME = os.environ.get("E2E_NEW_CRD_PROVIDER", "katan-openai-provider")
NS = os.environ.get("E2E_MODEL_NAMESPACE", "llm")


def _skip_if_not_deployed():
    cr = get_cr("externalmodel.inference.opendatahub.io", MODEL_NAME, NS)
    if cr is None:
        pytest.skip(f"ExternalModel {MODEL_NAME} not deployed in {NS}")


pytestmark = pytest.mark.skipif(
    get_cr("externalmodel.inference.opendatahub.io",
           os.environ.get("E2E_NEW_CRD_MODEL", "new-katan-openai"),
           os.environ.get("E2E_MODEL_NAMESPACE", "llm")) is None,
    reason="ExternalModel not deployed on cluster",
)


class TestExternalProviderReconciler:
    """Verify ExternalProvider reconciler creates shared networking resources."""

    def test_provider_phase_ready(self):
        cr = get_cr("externalprovider.inference.opendatahub.io", PROVIDER_NAME, NS)
        assert cr is not None, f"ExternalProvider {PROVIDER_NAME} not found"
        phase = cr.get("status", {}).get("phase", "")
        assert phase == "Ready", f"ExternalProvider phase={phase}, expected Ready"

    def test_service_created(self):
        svc = get_cr("service", PROVIDER_NAME, NS)
        assert svc is not None, (
            f"Service {PROVIDER_NAME} not found — ExternalProvider reconciler should create it"
        )
        assert svc["spec"]["type"] == "ExternalName", (
            f"Service type={svc['spec']['type']}, expected ExternalName"
        )

    def test_service_entry_created(self):
        se = get_cr("serviceentry.networking.istio.io", PROVIDER_NAME, NS)
        assert se is not None, (
            f"ServiceEntry {PROVIDER_NAME} not found — ExternalProvider reconciler should create it"
        )

    def test_destination_rule_created(self):
        dr = get_cr("destinationrule.networking.istio.io", PROVIDER_NAME, NS)
        assert dr is not None, (
            f"DestinationRule {PROVIDER_NAME} not found — ExternalProvider reconciler should create it"
        )

    def test_service_owned_by_provider(self):
        svc = get_cr("service", PROVIDER_NAME, NS)
        assert svc is not None
        owners = svc.get("metadata", {}).get("ownerReferences", [])
        provider_owner = [o for o in owners if o.get("kind") == "ExternalProvider"]
        assert len(provider_owner) == 1, (
            f"Service should be owned by ExternalProvider, got owners: {owners}"
        )


class TestExternalModelReconciler:
    """Verify ExternalModel reconciler creates HTTPRoute."""

    def test_model_phase_ready(self):
        cr = get_cr("externalmodel.inference.opendatahub.io", MODEL_NAME, NS)
        assert cr is not None
        phase = cr.get("status", {}).get("phase", "")
        assert phase == "Ready", f"ExternalModel phase={phase}, expected Ready"

    def test_httproute_created(self):
        route = get_cr("httproute.gateway.networking.k8s.io", MODEL_NAME, NS)
        assert route is not None, (
            f"HTTPRoute {MODEL_NAME} not found — ExternalModel reconciler should create it"
        )

    def test_httproute_targets_gateway(self):
        route = get_cr("httproute.gateway.networking.k8s.io", MODEL_NAME, NS)
        assert route is not None
        parent_refs = route.get("spec", {}).get("parentRefs", [])
        assert len(parent_refs) > 0, "HTTPRoute has no parentRefs"
        gateway_names = [str(p.get("name", "")) for p in parent_refs]
        assert "maas-default-gateway" in gateway_names, (
            f"HTTPRoute should target maas-default-gateway, got: {gateway_names}"
        )

    def test_httproute_owned_by_model(self):
        route = get_cr("httproute.gateway.networking.k8s.io", MODEL_NAME, NS)
        assert route is not None
        owners = route.get("metadata", {}).get("ownerReferences", [])
        model_owner = [o for o in owners if o.get("kind") == "ExternalModel"]
        assert len(model_owner) == 1, (
            f"HTTPRoute should be owned by ExternalModel, got owners: {owners}"
        )

    def test_httproute_path_matches_model(self):
        route = get_cr("httproute.gateway.networking.k8s.io", MODEL_NAME, NS)
        assert route is not None
        rules = route.get("spec", {}).get("rules", [])
        assert len(rules) > 0, "HTTPRoute has no rules"
        matches = rules[0].get("matches", [])
        assert len(matches) > 0, "HTTPRoute rule has no matches"
        path = matches[0].get("path", {}).get("value", "")
        assert f"/{NS}/{MODEL_NAME}" in path, (
            f"HTTPRoute path should contain /{NS}/{MODEL_NAME}, got: {path}"
        )


class TestReconcilerNegativeCases:
    """Verify reconciler handles bad input correctly."""

    def test_model_with_nonexistent_provider_goes_failed(self):
        """ExternalModel referencing a non-existent provider should not reach Ready."""
        from helpers import apply_cr, delete_cr, wait_for_cr
        import time

        ghost_model = "e2e-ghost-provider-model"
        try:
            apply_cr({
                "apiVersion": "inference.opendatahub.io/v1alpha1",
                "kind": "ExternalModel",
                "metadata": {"name": ghost_model, "namespace": NS},
                "spec": {
                    "externalProviderRefs": [{
                        "ref": {"name": "nonexistent-provider-xyz"},
                        "targetModel": "test",
                        "apiFormat": "chat-completions",
                    }],
                },
            })
            time.sleep(15)

            cr = get_cr("externalmodel.inference.opendatahub.io", ghost_model, NS)
            assert cr is not None
            phase = cr.get("status", {}).get("phase", "")
            assert phase != "Ready", (
                f"ExternalModel with non-existent provider should not be Ready, got phase={phase}"
            )
        finally:
            delete_cr("externalmodel.inference.opendatahub.io", ghost_model, NS)

    def test_provider_with_missing_secret_goes_failed(self):
        """ExternalProvider referencing a non-existent Secret should go Failed."""
        from helpers import apply_cr, delete_cr, wait_for_cr
        import time

        ghost_provider = "e2e-ghost-secret-provider"
        try:
            apply_cr({
                "apiVersion": "inference.opendatahub.io/v1alpha1",
                "kind": "ExternalProvider",
                "metadata": {"name": ghost_provider, "namespace": NS},
                "spec": {
                    "provider": "openai",
                    "endpoint": "api.openai.com",
                    "auth": {"secretRef": {"name": "nonexistent-secret-xyz"}},
                },
            })
            time.sleep(15)

            cr = get_cr("externalprovider.inference.opendatahub.io", ghost_provider, NS)
            assert cr is not None
            phase = cr.get("status", {}).get("phase", "")
            assert phase == "Failed", (
                f"ExternalProvider with missing Secret should be Failed, got phase={phase}"
            )
        finally:
            delete_cr("externalprovider.inference.opendatahub.io", ghost_provider, NS)


class TestMultipleProviderTypes:
    """Verify reconciler works for different provider types (not just openai)."""

    def test_anthropic_provider_ready(self):
        cr = get_cr("externalprovider.inference.opendatahub.io", "katan-anthropic-provider", NS)
        if cr is None:
            pytest.skip("katan-anthropic-provider not deployed")
        phase = cr.get("status", {}).get("phase", "")
        assert phase == "Ready", f"Anthropic provider phase={phase}, expected Ready"

    def test_anthropic_model_ready(self):
        cr = get_cr("externalmodel.inference.opendatahub.io", "new-katan-anthropic", NS)
        if cr is None:
            pytest.skip("new-katan-anthropic not deployed")
        phase = cr.get("status", {}).get("phase", "")
        assert phase == "Ready", f"Anthropic model phase={phase}, expected Ready"

    def test_anthropic_httproute_created(self):
        route = get_cr("httproute.gateway.networking.k8s.io", "new-katan-anthropic", NS)
        if route is None:
            pytest.skip("new-katan-anthropic HTTPRoute not found")
        assert route is not None

    def test_vertex_provider_ready(self):
        cr = get_cr("externalprovider.inference.opendatahub.io", "katan-vertex-provider", NS)
        if cr is None:
            pytest.skip("katan-vertex-provider not deployed")
        phase = cr.get("status", {}).get("phase", "")
        assert phase == "Ready", f"Vertex provider phase={phase}, expected Ready"

    def test_vertex_model_ready(self):
        cr = get_cr("externalmodel.inference.opendatahub.io", "new-katan-vertex-openai", NS)
        if cr is None:
            pytest.skip("new-katan-vertex-openai not deployed")
        phase = cr.get("status", {}).get("phase", "")
        assert phase == "Ready", f"Vertex model phase={phase}, expected Ready"

"""
Category 5: Migration v1alpha1 → v1alpha2.

Validates that existing maas.opendatahub.io/v1alpha1 ExternalModel CRs
are automatically converted to ExternalProvider + ExternalModel v1alpha2
by the migration controller.

Not implemented yet — all tests are xfail.
Tracked in refinement doc requirement #4.
"""

import logging
import os

import pytest

from helpers import get_cr

log = logging.getLogger(__name__)

NS = os.environ.get("E2E_MODEL_NAMESPACE", "llm")


class TestMigrationV1alpha1ToV1alpha2:
    """Verify automatic migration of old ExternalModel CRs."""

    @pytest.mark.xfail(reason="Migration controller not implemented yet (refinement requirement #4)")
    def test_v1alpha1_externalmodel_creates_provider(self):
        """Old ExternalModel should produce an ExternalProvider with same endpoint/creds."""
        old_model = get_cr("externalmodel.maas.opendatahub.io", "llm-katan-openai", NS)
        if old_model is None:
            pytest.skip("No v1alpha1 ExternalModel deployed")

        endpoint = old_model.get("spec", {}).get("endpoint", "")
        provider_name = f"migrated-{old_model['metadata']['name']}"

        new_provider = get_cr("externalprovider.inference.opendatahub.io", provider_name, NS)
        assert new_provider is not None, (
            f"Migration controller should create ExternalProvider '{provider_name}' "
            f"from v1alpha1 ExternalModel"
        )
        new_endpoint = new_provider.get("spec", {}).get("endpoint", "")
        assert new_endpoint == endpoint, (
            f"Migrated provider endpoint={new_endpoint}, expected {endpoint}"
        )

    @pytest.mark.xfail(reason="Migration controller not implemented yet (refinement requirement #4)")
    def test_v1alpha1_externalmodel_creates_v1alpha2_model(self):
        """Old ExternalModel should produce a v1alpha2 ExternalModel with provider ref."""
        old_model = get_cr("externalmodel.maas.opendatahub.io", "llm-katan-openai", NS)
        if old_model is None:
            pytest.skip("No v1alpha1 ExternalModel deployed")

        model_name = old_model["metadata"]["name"]
        new_model = get_cr("externalmodel.inference.opendatahub.io", model_name, NS)
        assert new_model is not None, (
            f"Migration controller should create inference.opendatahub.io ExternalModel '{model_name}'"
        )
        refs = new_model.get("spec", {}).get("externalProviderRefs", [])
        assert len(refs) >= 1, "Migrated model should have at least one provider ref"

    @pytest.mark.xfail(reason="Migration controller not implemented yet (refinement requirement #4)")
    def test_shared_provider_deduplication(self):
        """Multiple v1alpha1 ExternalModels on same endpoint should share one ExternalProvider."""
        # Both llm-katan-openai and llm-katan-vertex-openai use the same endpoint
        providers = []
        for kind_name in ["llm-katan-openai", "llm-katan-vertex-openai"]:
            old = get_cr("externalmodel.maas.opendatahub.io", kind_name, NS)
            if old:
                providers.append(old.get("spec", {}).get("endpoint", ""))

        if len(providers) < 2:
            pytest.skip("Need at least 2 v1alpha1 ExternalModels for deduplication test")

        if providers[0] == providers[1]:
            # Same endpoint — migration should create only one ExternalProvider
            # Check that both migrated models reference the same provider
            pass

        pytest.fail("Deduplication validation not yet implemented")

    @pytest.mark.xfail(reason="Migration controller not implemented yet (refinement requirement #4)")
    def test_migration_preserves_credentials(self):
        """Migrated ExternalProvider should reference the same Secret as the v1alpha1 model."""
        old_model = get_cr("externalmodel.maas.opendatahub.io", "llm-katan-openai", NS)
        if old_model is None:
            pytest.skip("No v1alpha1 ExternalModel deployed")

        old_cred = old_model.get("spec", {}).get("credentialRef", {}).get("name", "")
        provider_name = f"migrated-{old_model['metadata']['name']}"

        new_provider = get_cr("externalprovider.inference.opendatahub.io", provider_name, NS)
        assert new_provider is not None, "Migrated provider should exist"
        new_cred = new_provider.get("spec", {}).get("auth", {}).get("secretRef", {}).get("name", "")
        assert new_cred == old_cred, (
            f"Migrated provider should reference same Secret: got {new_cred}, expected {old_cred}"
        )

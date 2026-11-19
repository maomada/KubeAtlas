package cloudinstall

import (
	"errors"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/helm"
)

// Both presenters render Inspect as a copyable shell block, so prose there
// would be pasted straight into a terminal. Every entry, in every guidance
// variant, must be a runnable command.
func TestRecoveryGuidanceInspectHoldsOnlyRunnableCommands(t *testing.T) {
	deployment := helm.DeploymentRef{Namespace: "kubeatlas", Name: "kubeatlas"}
	adopt := ProvisionRecovery{
		Mode: ProvisionAdopt, ReleaseName: "kubeatlas", Namespace: "kubeatlas",
		Deployment: deployment, CurrentRevision: 3,
	}
	fresh := ProvisionRecovery{
		Mode: ProvisionFresh, ReleaseName: "kubeatlas", Namespace: "kubeatlas",
		Deployment: deployment,
	}
	target := CommandTarget{}
	guidances := []RecoveryGuidance{
		CanceledAfterApprovalGuidance("clus_1", "https://hub/c/clus_1", "Rerun."),
		PostApprovalProvisionGuidance("clus_1", "https://hub/c/clus_1", adopt, errors.New("boom"), target),
		PostApprovalProvisionGuidance("clus_1", "https://hub/c/clus_1", adopt, &ProvisionPreMutationError{}, target),
		PostApprovalProvisionGuidance("clus_1", "https://hub/c/clus_1", fresh, errors.New("boom"), target),
		TunnelConfirmationGuidance(errors.New("boom"), "clus_1", "wss://hub/agent", "https://hub/c/clus_1", deployment, target),
		AdoptionRollbackGuidance(adopt, "https://hub/c/clus_1", target),
	}
	for _, g := range guidances {
		for _, cmd := range g.Inspect {
			fields := strings.Fields(cmd)
			if len(fields) == 0 {
				t.Errorf("empty Inspect entry (guidance %q)", g.Summary)
				continue
			}
			switch fields[0] {
			case "helm", "kubectl", "kubeatlas":
			default:
				t.Errorf("Inspect entry is not a runnable command: %q (guidance %q)", cmd, g.Summary)
			}
		}
	}
}

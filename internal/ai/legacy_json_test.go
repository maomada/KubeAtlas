package ai

import (
	"encoding/json"
	"testing"
)

func TestStoredProofFlagCompatibilityDoesNotBypassLiveValidator(t *testing.T) {
	var step StepInfo
	if err := json.Unmarshal([]byte(`{"id":"old","radarEvidence":true}`), &step); err != nil || !step.KubeAtlasEvidence {
		t.Fatalf("stored proof flag lost:%v", err)
	}
	// The live validator owns the flag regardless of its serialized spelling.
	var live investigationEvidenceValidator
	live.validate(StreamEvent{Type: "step", Step: &step})
	if step.KubeAtlasEvidence {
		t.Fatal("serialized proof flag escaped live validation")
	}
	if json.Unmarshal([]byte(`{"radarEvidence":true,"kubeatlasEvidence":false}`), &step) == nil {
		t.Fatal("conflicting proof flags accepted")
	}
}

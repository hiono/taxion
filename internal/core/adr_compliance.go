package core

import (
	"slices"
	"strings"
)

const ADRCompliancePrefix = "adr-compliance:"
const ADRNotApplicableCriterion = ADRCompliancePrefix + "not-applicable"

// ADRComplianceCriterion binds an acceptance decision to an approved source
// snapshot, including its version, quoted content and declared use.
func ADRComplianceCriterion(spec Specification) (string, error) {
	hash, err := SpecificationHash(spec)
	if err != nil {
		return "", err
	}
	return ADRCompliancePrefix + spec.ID + ":" + hash, nil
}

func ADRComplianceRequirements(acceptance []string, specs []Specification) ([]string, error) {
	var required []string
	for _, criterion := range acceptance {
		if !strings.HasPrefix(criterion, ADRCompliancePrefix) {
			continue
		}
		if slices.Contains(required, criterion) {
			return nil, graphError("duplicate ADR compliance criterion")
		}
		required = append(required, criterion)
		if criterion == ADRNotApplicableCriterion {
			continue
		}
		if err := validateSpecifications(specs); err != nil {
			return nil, err
		}
		found := false
		for _, spec := range specs {
			expected, err := ADRComplianceCriterion(spec)
			if err != nil {
				return nil, err
			}
			found = found || expected == criterion
		}
		if !found {
			return nil, graphError("ADR compliance criterion lacks its exact specification snapshot")
		}
	}
	if len(required) > 1 && slices.Contains(required, ADRNotApplicableCriterion) {
		return nil, graphError("ADR non-applicability conflicts with required ADR decisions")
	}
	return required, nil
}

func validateADRReviewerAcceptance(d GraphDispatch, result *DispatchResult) error {
	required, err := ADRComplianceRequirements(d.Instruction.Acceptance, d.Instruction.Specifications)
	if err != nil {
		return err
	}
	for _, criterion := range required {
		for _, decision := range result.Acceptance {
			if decision.Criterion != criterion {
				continue
			}
			if !slices.Contains(decision.Evidence, criterion) {
				return graphError("ADR compliance decision requires evidence referencing the exact bound criterion")
			}
			if criterion == ADRNotApplicableCriterion && decision.Status == "PASS" && !nonempty(result.WorkReport) {
				return graphError("ADR non-applicability requires an independent explanation in workReport")
			}
		}
	}
	return nil
}

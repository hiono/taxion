package config

import "fmt"

var environmentFields = map[string]string{
	"TAXION_PROJECT_ID":                "project.id",
	"TAXION_REPOSITORY_PROVIDER":       "repository.provider",
	"TAXION_REPOSITORY_HOST":           "repository.host",
	"TAXION_REPOSITORY_PATH":           "repository.path",
	"TAXION_REPOSITORY_ACCOUNT":        "repository.account",
	"TAXION_REPOSITORY_CREDENTIAL_REF": "repository.credentialRef",
	"TAXION_RUNTIME_PROVIDER":          "runtime.provider",
	"TAXION_RUNTIME_ACCOUNT":           "runtime.account",
	"TAXION_RUNTIME_CREDENTIAL_REF":    "runtime.credentialRef",
	"TAXION_RUNTIME_CONTRACT_REF":      "runtime.contractRef",
	"TAXION_RUNTIME_ACP_COMMAND":       "runtime.acpCommand",
	"TAXION_RUNTIME_ACP_COMMAND_HASH":  "runtime.acpCommandHash",
	"TAXION_RUNTIME_ACP_CONFIG":        "runtime.acpConfig",
	"TAXION_RUNTIME_ACP_CONFIG_HASH":   "runtime.acpConfigHash",
	"TAXION_EXECUTION_REQUESTER":       "execution.requester",
	"TAXION_EXECUTION_WORKER":          "execution.worker",
	"TAXION_EXECUTION_APPROVAL_REF":    "execution.approvalRef",
	"TAXION_EXECUTION_BUDGET_GUIDANCE": "execution.budgetGuidance",
}

// EnvironmentLayer uses only supported setting variables. Credential values
// and unrelated environment variables are not read.
func EnvironmentLayer(lookup func(string) (string, bool)) (Layer, bool, error) {
	values := map[string]string{}
	for variable, field := range environmentFields {
		text, present := lookup(variable)
		if !present {
			continue
		}
		if text == "" {
			return Layer{}, false, fmt.Errorf("%s requires a nonempty value", variable)
		}
		values[field] = text
	}
	if len(values) == 0 {
		return Layer{}, false, nil
	}
	layer, err := ValuesLayer("environment", values)
	if err != nil {
		return Layer{}, false, fmt.Errorf("environment configuration: %w", err)
	}
	return layer, true, nil
}

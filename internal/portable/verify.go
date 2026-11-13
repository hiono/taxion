package portable

import (
	"context"
	"fmt"
	"os"

	"github.com/hiono/taxion/internal/config"
)

// VerifyReport summarizes a snapshot revalidation.
type VerifyReport struct {
	Version              int `json:"version"`
	LegacyRepositories   int `json:"legacyRepositories"`
	EndpointRepositories int `json:"endpointRepositories"`
}

// VerifySnapshot revalidates a saved snapshot without writing to it. Stage A
// checks the archive against the values saved inside it, so the saved endpoint
// values are canonical. Stage B compares those values with the effective
// configuration after the documented precedence order; a difference in config
// source alone is not drift, while a difference in effective value fails closed
// with REP-007. A version 1 snapshot stays on the legacy verifier and is never
// backfilled.
func VerifySnapshot(ctx context.Context, input string, current config.Config) (VerifyReport, error) {
	tmp, err := os.MkdirTemp("", "taxion-portable-verify-")
	if err != nil {
		return VerifyReport{}, err
	}
	defer os.RemoveAll(tmp)
	m, _, _, err := readArchive(input, tmp)
	if err != nil {
		return VerifyReport{}, err
	}
	result := VerifyReport{Version: m.Version}
	for _, repository := range m.Repositories {
		if repository.Endpoints == nil {
			result.LegacyRepositories++
		} else {
			result.EndpointRepositories++
		}
	}
	if m.Version != 2 {
		return result, nil
	}
	if !current.HasEndpoints() {
		return result, fmt.Errorf("REP-007 snapshot endpoint values have no effective configuration")
	}
	for _, repository := range m.Repositories {
		if repository.Endpoints == nil || repository.Endpoints.Empty() {
			return result, fmt.Errorf("REP-007 endpoint snapshot requires saved endpoint values")
		}
		if repository.ID != current.Repository.Path {
			return result, fmt.Errorf("REP-007 snapshot repository path differs from the effective configuration")
		}
		if *repository.Endpoints != current.Endpoints {
			return result, fmt.Errorf("REP-007 snapshot endpoint values differ from the effective configuration")
		}
	}
	return result, nil
}

// checkSnapshotOrigin validates a saved origin against the snapshot version.
func checkSnapshotOrigin(version int, repository repository) error {
	if version == 1 {
		if repository.Endpoints != nil {
			return fmt.Errorf("REP-007 legacy snapshot cannot carry endpoint values")
		}
		if origin, err := safeOrigin(repository.Origin, repository.ID); err != nil || origin != repository.Origin {
			return fmt.Errorf("invalid portable repository origin")
		}
		return nil
	}
	if repository.Endpoints == nil || repository.Endpoints.Empty() {
		return fmt.Errorf("REP-007 endpoint snapshot requires endpoint values")
	}
	expected, err := bindOrigin(repository.Origin, repository.ID, repository.Endpoints)
	if err != nil || expected != repository.Origin {
		return fmt.Errorf("invalid portable repository origin")
	}
	return nil
}

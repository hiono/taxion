package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/provenance"
	"github.com/hiono/taxion/internal/publictext"
	"gopkg.in/yaml.v3"
)

// The release workflow publishes only this checked, non-templated body. It
// disables GoReleaser's publisher so generated text cannot bypass this gate.
func releasePresentation(tag string) (publictext.Mutation, error) {
	return releaseSource(tag, true)
}

func releaseSource(tag string, requireReceipt bool) (publictext.Mutation, error) {
	profile, err := publictext.LoadProfile("github", "github.com", "hiono/taxion")
	if err != nil {
		return publictext.Mutation{}, err
	}
	_, guidance, err := publictext.SelectGuidance(profile, "release")
	if err != nil {
		return publictext.Mutation{}, err
	}
	if !provenance.ValidReleaseVersion(tag) {
		return publictext.Mutation{}, fmt.Errorf("release presentation requires a stable or rc.N version tag")
	}
	version, err := os.ReadFile("VERSION")
	if err != nil || "v"+strings.TrimSpace(string(version)) != tag {
		return publictext.Mutation{}, fmt.Errorf("release presentation tag differs from VERSION")
	}
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		return publictext.Mutation{}, err
	}
	var config struct {
		Version int `yaml:"version"`
		Release struct {
			Header string `yaml:"header"`
			Footer string `yaml:"footer"`
		} `yaml:"release"`
		Changelog struct {
			Disable bool `yaml:"disable"`
		} `yaml:"changelog"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return publictext.Mutation{}, err
	}
	if config.Version != 2 || !config.Changelog.Disable || config.Release.Footer != "" ||
		strings.TrimSpace(config.Release.Header) == "" || strings.Contains(config.Release.Header, "{{") {
		return publictext.Mutation{}, fmt.Errorf("release presentation requires an explicit static body without generated changelog or footer")
	}
	mutation := publictext.Mutation{Provider: "github", Host: "github.com", Repository: "hiono/taxion",
		GuidanceDigest: guidance,
		Surface:        "release", Title: "Taxion " + strings.TrimPrefix(tag, "v"), Body: config.Release.Header,
		Statement: publictext.StatementContext{At: time.Now().UTC()}}
	if profile.RequireSemanticReview && requireReceipt {
		data, err := os.ReadFile(".github/release-semantic-review.json")
		if err != nil {
			return publictext.Mutation{}, fmt.Errorf("release requires independent exact-text semantic review")
		}
		var review publictext.SemanticReview
		if err := json.Unmarshal(data, &review); err != nil {
			return publictext.Mutation{}, err
		}
		if review.Statement == nil || review.Statement.At.IsZero() || review.Statement.At.After(time.Now().UTC()) {
			return publictext.Mutation{}, fmt.Errorf("release review requires the exact nonfuture statement context")
		}
		mutation.Statement = *review.Statement
		mutation.Review = &review
	}
	return mutation, nil
}

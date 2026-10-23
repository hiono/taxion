package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/publictext"
	"gopkg.in/yaml.v3"
)

// The release workflow publishes only this checked, non-templated body. It
// disables GoReleaser's publisher so generated text cannot bypass this gate.
func releasePresentation(tag string) (publictext.Mutation, error) {
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(tag) {
		return publictext.Mutation{}, fmt.Errorf("release presentation requires a stable version tag")
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
	return publictext.Mutation{Provider: "github", Host: "github.com", Repository: "hiono/taxion",
		Surface: "release", Title: "Taxion " + strings.TrimPrefix(tag, "v"), Body: config.Release.Header,
		Statement: publictext.StatementContext{At: time.Now().UTC()}}, nil
}

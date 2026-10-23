package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hiono/taxion/internal/publictext"
)

// Review preparation emits evidence only. It never creates an approval.
func reviewBasis(args []string, output io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("review basis requires an exact mutation JSON file or release tag")
	}
	var m publictext.Mutation
	if args[0] == "-release-review-basis" {
		var err error
		m, err = releaseSource(args[1], false)
		if err != nil {
			return err
		}
	} else {
		file, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer file.Close()
		decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&m); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("one mutation required")
		}
	}
	p, err := publictext.LoadProfile(m.Provider, m.Host, m.Repository)
	if err != nil {
		return err
	}
	_, digest, err := publictext.SelectGuidance(p, m.Surface)
	if err != nil {
		return err
	}
	if m.GuidanceDigest != digest {
		return fmt.Errorf("select compatible guidance before writing the mutation")
	}
	if err := publictext.CheckPresentation(p, m.Title, m.Body); err != nil {
		return err
	}
	protected := strings.Fields(os.Getenv("TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256"))
	if len(protected) == 0 {
		return fmt.Errorf("review basis requires protected identifier policy")
	}
	if err := publictext.CheckIdentifiers(m.Title+"\n"+m.Body, protected); err != nil {
		return err
	}
	m.Review = nil
	return json.NewEncoder(output).Encode(struct {
		Mutation      publictext.Mutation `json:"mutation"`
		Profile       publictext.Profile  `json:"profile"`
		ProfileHash   string              `json:"profileHash"`
		ContentHash   string              `json:"contentHash"`
		Authorization bool                `json:"authorization"`
	}{m, p, publictext.ProfileHash(p), publictext.SemanticHash(m), false})
}

package main

import (
	"fmt"

	"github.com/IamAlexandros/0type/internal/modelstore"
)

// runSetup downloads (or verifies the checksum of) every file the ASR
// pipeline needs into the local model cache.
func runSetup(args []string) error {
	dir, err := modelstore.Dir(modelstore.ParakeetTDTv2.Name)
	if err != nil {
		return fmt.Errorf("resolve model cache dir: %w", err)
	}
	fmt.Printf("model cache: %s\n", dir)

	err = modelstore.EnsureDownloaded(dir, modelstore.ParakeetTDTv2.Files, func(file string, downloaded bool) {
		if downloaded {
			fmt.Printf("downloaded %s\n", file)
		} else {
			fmt.Printf("cached      %s (checksum OK)\n", file)
		}
	})
	if err != nil {
		return err
	}
	fmt.Println("setup complete")
	return nil
}

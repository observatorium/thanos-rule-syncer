package main

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"
)

type tenantsSetter interface {
	SetTenants(tenants []string)
}

type tenantsReader func() ([]string, error)

// newTenantsFileReloader reloads tenants at a given interval and sets them on the given tenantsSetter.
// It returns an error if the tenants file cannot be read 3 times in a row.
// It stops reloading when the context is cancelled.
func newTenantsFileReloader(ctx context.Context, readTenants tenantsReader, interval time.Duration, tenset tenantsSetter) error {
	var tenants []string
	var err error
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Count successive errors and fail if we get 3 in a row.
	var errorCount uint

	for {
		select {
		case <-ticker.C:
			tenants, err = readTenants()
			if err != nil {
				log.Printf("failed to read tenants file: %v", err)
				errorCount++

				if errorCount >= 3 {
					return fmt.Errorf("failed to read tenants file 3 times in a row")
				}

				continue
			} else {
				errorCount = 0
			}

			tenset.SetTenants(tenants)
		case <-ctx.Done():
			log.Printf("tenants file reloader exiting: %v", ctx.Err())
			return nil
		}
	}
}

// readTenantsFile reads tenants from a file.
func readTenantsFile(file string) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("failed to open tenants file: %w", err)
	}
	defer f.Close()

	return scanFile(f)
}

// scanFile scans a file and returns a slice of tenants.
// It expects one tenant per line and ignores empty lines.
// Returned tenants are deduplicated.
func scanFile(f fs.File) ([]string, error) {
	var tenants []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		tenants = append(tenants, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read tenants file: %w", err)
	}

	// Deduplicate tenants, remove empty lines
	tenantsSet := make(map[string]struct{}, len(tenants))
	duplicates := []string{}
	for _, tenant := range tenants {
		if tenant != "" {
			if _, ok := tenantsSet[tenant]; ok {
				duplicates = append(duplicates, tenant)
				continue
			}

			tenantsSet[tenant] = struct{}{}
		}
	}

	if len(duplicates) > 0 {
		log.Printf("WARNING: found duplicate tenants in file: %v", duplicates)
	}

	ret := make([]string, 0, len(tenantsSet))
	for tenant := range tenantsSet {
		ret = append(ret, tenant)
	}

	if len(ret) == 0 {
		return nil, fmt.Errorf("no tenants found in file")
	}

	return ret, nil
}

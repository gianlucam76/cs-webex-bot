package utils

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gianlucam76/cs-e2e-result/es_utils"
	"github.com/go-logr/logr"
)

// BuildUCSTests creates:
// - a slice containing all test names
// - a map containing for each test its descriptions
func BuildUCSTests(ctx context.Context, logger logr.Logger) (testName []string, err error) {
	testName = make([]string, 0)

	results, err := es_utils.GetResults(ctx, logger,
		"",    // no filter on this run
		"",    // no specific test
		false, // no vcs. VCS has subsets of tests.
		true,  // from ucs. UCS has all tests.
		false, // no filter passed tests
		false, // no filter failed tests
		false, // no filter skipped tests
		200,
	)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get failed test in ucs from elastic DB. Err: %v", err))
		return nil, err
	}

	testNameMap := make(map[string]bool)

	var rtyp es_utils.Result
	for _, item := range results.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		testNameMap[r.Name] = true
	}

	for k := range testNameMap {
		testName = append(testName, k)
	}

	return
}

// BuildVCSTests creates:
// - a slice containing all test names
// - a map containing for each test its descriptions
func BuildVCSTests(ctx context.Context, logger logr.Logger) (testName []string, err error) {
	testName = make([]string, 0)

	results, err := es_utils.GetResults(ctx, logger,
		"",    // no filter on this run
		"",    // no specific test
		true,  // from vcs. VCS has subsets of tests.
		false, // no ucs. UCS has all tests.
		false, // no filter passed tests
		false, // no filter failed tests
		false, // no filter skipped tests
		200,
	)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get failed test in ucs from elastic DB. Err: %v", err))
		return nil, err
	}

	testNameMap := make(map[string]bool)

	var rtyp es_utils.Result
	for _, item := range results.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		testNameMap[r.Name] = true
	}

	for k := range testNameMap {
		testName = append(testName, k)
	}

	return
}

// GetLastRun returns last run ID.
// vcs bool controls whether that is going to be for last VCS run or UCS run
func GetLastRun(ctx context.Context, vcs bool, logger logr.Logger) (int64, error) {
	elasticClient, err := es_utils.GetClient()
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get elastic client. Err: %v", err))
		return 0, err
	}

	match := "ucs"
	if vcs {
		match = "vcs"
	}

	b, err := es_utils.GetAvailableRuns(ctx, elasticClient, match, 10, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get available %s runs from elastic DB. Err: %v", match, err))
		return 0, err
	}

	var lastRun int64
	for _, bucket := range b.Buckets {
		if rID, err := bucket.KeyNumber.Int64(); err == nil {
			if lastRun == 0 || lastRun < rID {
				lastRun = rID
			}
		}
	}

	return lastRun, nil
}

package utils

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gianlucam76/cs-e2e-result/es_utils"
	"github.com/go-logr/logr"
)

const ReportTypeSeparator = "---"

// BuildUCSTests creates:
// - a slice containing all test names
func BuildUCSTests(ctx context.Context, logger logr.Logger) ([]string, error) {
	testName := make([]string, 0)

	runs, err := GetLastNRuns(ctx, false, 5, logger)
	if err != nil {
		return nil, err
	}

	testNameMap := make(map[string]bool)
	for i := range runs {
		results, err := es_utils.GetResults(ctx, logger,
			fmt.Sprintf("%d", runs[i]), // filter on this run
			"",                         // no specific test
			false,                      // no vcs. VCS has subsets of tests.
			true,                       // from ucs. UCS has all tests.
			false,                      // no filter passed tests
			false,                      // no filter failed tests
			false,                      // no filter skipped tests
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in ucs from elastic DB. Err: %v", err))
			continue
		}

		var rtyp es_utils.Result
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Result)
			testNameMap[r.Name] = true
		}
	}

	for k := range testNameMap {
		testName = append(testName, k)
	}

	return testName, nil
}

// BuildVCSTests creates:
// - a slice containing all test names
func BuildVCSTests(ctx context.Context, logger logr.Logger) ([]string, error) {
	testName := make([]string, 0)

	runs, err := GetLastNRuns(ctx, true, 5, logger)
	if err != nil {
		return nil, err
	}

	testNameMap := make(map[string]bool)
	for i := range runs {
		results, err := es_utils.GetResults(ctx, logger,
			fmt.Sprintf("%d", runs[i]), // filter on this run
			"",                         // no specific test
			true,                       // from vcs. VCS has subsets of tests.
			false,                      // no ucs. UCS has all tests.
			false,                      // no filter passed tests
			false,                      // no filter failed tests
			false,                      // no filter skipped tests
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in ucs from elastic DB. Err: %v", err))
			continue
		}

		var rtyp es_utils.Result
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Result)
			testNameMap[r.Name] = true
		}
	}

	for k := range testNameMap {
		testName = append(testName, k)
	}

	return testName, nil
}

// BuildUCSReports creates:
// - a slice containing all reports types (type:subType if subType is defined, just type otherwise)
func BuildUCSReports(ctx context.Context, logger logr.Logger) ([]string, error) {
	reportType := make([]string, 0)

	runs, err := GetLastNRuns(ctx, false, 5, logger)
	if err != nil {
		return nil, err
	}

	reportTypeMap := make(map[string]bool)

	for i := range runs {
		reports, err := es_utils.GetReports(ctx, logger,
			fmt.Sprintf("%d", runs[i]), // filter on this run
			"",                         // no specific report type
			"",                         // no specific report subtype
			"",                         // no specific report name
			false,                      // no vcs. VCS has subsets of tests.
			true,                       // from ucs. UCS has all tests.
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed reports in ucs from elastic DB. Err: %v", err))
			continue
		}

		var rtyp es_utils.Report
		for _, item := range reports.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Report)
			if r.SubType != "" {
				reportTypeMap[fmt.Sprintf("%s%s%s", r.Type, ReportTypeSeparator, r.SubType)] = true
			} else {
				reportTypeMap[r.Type] = true
			}
		}
	}

	for k := range reportTypeMap {
		reportType = append(reportType, k)
	}

	return reportType, nil
}

// GetLastRun returns last run ID.
// vcs bool controls whether that is going to be for last VCS run or UCS run
func GetLastRun(ctx context.Context, vcs bool, logger logr.Logger) (int64, error) {
	match := "ucs"
	if vcs {
		match = "vcs"
	}

	b, err := es_utils.GetAvailableRuns(ctx, match, 10, logger)
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

// GetLastNRuns returns the last N run
// vcs bool controls whether that is going to be for last VCS run or UCS run
func GetLastNRuns(ctx context.Context, vcs bool, runs int, logger logr.Logger) ([]int64, error) {
	match := "ucs"
	if vcs {
		match = "vcs"
	}

	b, err := es_utils.GetAvailableRuns(ctx, match, runs, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get available %s runs from elastic DB. Err: %v", match, err))
		return nil, err
	}

	lastRuns := make([]int64, 0)
	for _, bucket := range b.Buckets {
		if rID, err := bucket.KeyNumber.Int64(); err == nil {
			lastRuns = append(lastRuns, rID)
		}
	}

	return lastRuns, nil
}

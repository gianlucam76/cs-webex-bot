package utils

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"

	"github.com/gianlucam76/cs-e2e-result/es_utils"
	jira_utils "github.com/gianlucam76/jira_utils/jira"
)

const (
	ReportTypeSeparator        = "---"
	AtomUser            string = "atom-ci.gen"
	LCSBoardName        string = "CloudStack - LCS"
)

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

// GetOpenIssues returns open issues filed by atom user during e2e tagging sanity
func GetOpenIssues(ctx context.Context, jiraClient *jira.Client, logger logr.Logger) ([]jira.Issue, error) {
	project, err := jira_utils.GetJiraProject(ctx, jiraClient, "", logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project. Err: %v", err))
		return nil, err
	}

	jql := fmt.Sprintf("Status NOT IN (Resolved,Closed) and reporter = atom-ci.gen and project = %s",
		project.Name)
	issues, err := jira_utils.GetJiraIssues(ctx, jiraClient, jql, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get open issues. Err: %v", err))
		return nil, err
	}

	return issues, nil
}

// SplitIssue gets all comments for passed issue.
// Any comment added by atomUser represents an e2e tagging sanity failures.
// If different failures are identified, new issue is filed.
func SplitIssue(ctx context.Context, jiraClient *jira.Client, issue *jira.Issue, logger logr.Logger) ([]string, error) {
	project, err := jira_utils.GetJiraProject(ctx, jiraClient, "", logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project. Err: %v", err))
		return nil, err
	}

	board, err := jira_utils.GetJiraBoard(ctx, jiraClient, project.Key, LCSBoardName, logger)
	if err != nil || board == nil {
		logger.Info(fmt.Sprintf("Failed to get jira board. Err %v", err))
		return nil, err
	}

	activeSprint, err := jira_utils.GetJiraActiveSprint(ctx, jiraClient, fmt.Sprintf("%d", board.ID), logger)
	if err != nil || activeSprint == nil {
		logger.Info(fmt.Sprintf("Failed to get active sprint. Err: %v", err))
		return nil, err
	}

	priority := jira.Priority{Name: "P1"}

	options := &jira.GetQueryOptions{Expand: "renderedFields"}
	u, _, err := jiraClient.Issue.Get(issue.Key, options)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to query renderedFields. Error: %v\n", err))
		return nil, err
	}

	// Walk all comment added by Atom user to this issue.
	// Any comment is a failure (different run for sure, possibly different method as well)
	// For any comment representing a different location where failure happened, create a new issue.

	var originalIssue string

	// This map contains per each failure the corresponding new jira issue (key) filed
	newIssues := make(map[string]string)
	for _, c := range u.RenderedFields.Comments.Comments {
		if c.Author.Name == AtomUser { // Consider only comment added by Atom user
			if tmp, err := GetFunctionName(c, logger); err == nil {
				if tmp == "" {
					// Nothing to do. We were not able to identify in which method issue failed
					// when this comment was added
					continue
				} else if originalIssue == "" || tmp == originalIssue {
					// This comment was added for a different run. But the method were failure happened
					// matches first failure reported in this issue.
					originalIssue = tmp
				} else if v, ok := newIssues[tmp]; ok {
					// This comment was added for a failure that happened in the same test but in a different
					// method.
					// We have already seen other comments like this one. So a new jira issue has been already
					// created. Simply add this comment to the new jira issue and remove it from the issue we
					// were request to split into multiple.
					if _, _, err := jiraClient.Issue.AddCommentWithContext(ctx, issue.ID, c); err != nil {
						logger.Info("Failed to add new comment to issue %s", v)
						continue
					}
					// Remove comment from issue
					if err := jiraClient.Issue.DeleteCommentWithContext(ctx, issue.ID, c.ID); err != nil {
						logger.Info("Failed to delete comment from issue %s", issue.Key)
						continue
					}
				} else {
					// This comment is referencing a failure never seen so far. So first create new issue then move comment.
					if newIssue, err := jira_utils.CreateIssue(ctx, jiraClient, activeSprint, &priority, project.Key, "e2e",
						issue.Fields.Assignee.Name, "", issue.Fields.Summary, logger); err != nil {
						logger.Info("Failed to create new issue. Err: %v", err)
						continue
					} else {
						if _, _, err := jiraClient.Issue.AddCommentWithContext(ctx, newIssue.ID, c); err != nil {
							logger.Info("Failed to add new comment to issue %s", newIssue.ID)
							continue
						}
						// Remove comment from issue
						if err := jiraClient.Issue.DeleteCommentWithContext(ctx, issue.ID, c.ID); err != nil {
							logger.Info("Failed to delete comment from issue %s", issue.Key)
							continue
						}
						newIssues[tmp] = newIssue.Key
					}
				}
			}
		}
	}

	result := make([]string, 0)
	for key := range newIssues {
		result = append(result, key)
	}

	return result, nil
}

// GetFunctionName returns the name of the function where failure happened.
// When Jira issue is filed for an e2e tagging sanity, comment contains:
// - Failure Location: <line where failure happened>
// - Full Stack Trace: <method where failure happened> + <full stack trace>
// getFunctionName extracts the name of the method where failure happened
func GetFunctionName(c *jira.Comment, logger logr.Logger) (string, error) {
	if c.Author.Name != AtomUser {
		return "", fmt.Errorf("comment not added by %s", AtomUser)
	}

	const fullStackTraceText string = "Full Stack Trace "
	fullStackTraceIndex := strings.Index(c.Body, fullStackTraceText)
	if fullStackTraceIndex == -1 {
		logger.Info("full strack trace not present")
		return "", fmt.Errorf("full strack trace not present")
	}
	begin := fullStackTraceIndex + len(fullStackTraceText)
	spaceIndex := strings.Index(c.Body[begin:], "0x")
	if spaceIndex == -1 {
		logger.Info("full strack trace is malformed")
		return "", fmt.Errorf("full strack trace is malformed")
	}
	end := begin + spaceIndex
	return strings.TrimSuffix(c.Body[begin:end], "."), nil
}

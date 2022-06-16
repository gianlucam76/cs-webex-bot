package utils

import (
	"bufio"
	"context"
	"fmt"
	"io"
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

// BuildUCSUsageReports creates:
// - a slice containing all pods for which at least a usage report was found
func BuildUCSUsageReports(ctx context.Context, logger logr.Logger) ([]string, error) {
	reports := make([]string, 0)

	runs, err := GetLastNRuns(ctx, false, 5, logger)
	if err != nil {
		return nil, err
	}

	// a map listing usage report per pod
	reportsMap := make(map[string]bool)

	for i := range runs {
		reports, err := es_utils.GetUsageReports(ctx, logger,
			fmt.Sprintf("%d", runs[i]), // filter on this run
			"",                         // no pod filter.
			false,                      // no vcs. VCS has subsets of tests.
			true,                       // from ucs. UCS has all tests.
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed usage reports in ucs from elastic DB. Err: %v", err))
			continue
		}

		var rtyp es_utils.UsageReport
		for _, item := range reports.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.UsageReport)
			reportsMap[r.Name] = true
		}
	}

	for k := range reportsMap {
		reports = append(reports, k)
	}

	return reports, nil
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
func SplitIssue(ctx context.Context, jiraClient *jira.Client, issueToSplit *jira.Issue, openIssues []jira.Issue,
	logger logr.Logger) ([]string, error) {
	logger.Info(fmt.Sprintf("SplitIssue %s", issueToSplit.Key))
	project, activeSprint, err := getJiraProjectAndActiveSprint(ctx, jiraClient, logger)
	if err != nil || activeSprint == nil {
		logger.Info(fmt.Sprintf("Failed to get project or active sprint. Err: %v", err))
		return nil, err
	}

	// Consider all open issues, excluding:
	// - any issue with multiple failures.
	// Build a map:
	// - key: failure
	// - value: issue
	existingFailureMap := buildFailureMap(ctx, jiraClient, openIssues, logger)

	options := &jira.GetQueryOptions{Expand: "renderedFields"}
	u, _, err := jiraClient.Issue.Get(issueToSplit.Key, options)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to query renderedFields. Error: %v\n", err))
		return nil, err
	}

	// Walk all comment added by Atom user to this issue.
	// Any comment is a failure (different run for sure, possibly different method as well)
	// For any comment representing a different location where failure happened, create a new issue.

	var firstDetectedFailureLocation string

	// This map contains per each failure the corresponding new jira issue filed
	newIssues := make(map[string]*jira.Issue)
	for _, c := range u.RenderedFields.Comments.Comments {
		if c.Author.Name == AtomUser { // Consider only comment added by Atom user
			newComment := &jira.Comment{
				Body: c.Body,
			}
			if failureLocation, err := GetFunctionName(c, logger); err == nil {
				if failureLocation == "" {
					// Nothing to do. We were not able to identify in which method issue failed
					// when this comment was added
					continue
				} else if firstDetectedFailureLocation == "" || failureLocation == firstDetectedFailureLocation {
					// This comment was added for a different run. But the method were failure happened
					// matches first failure reported in this issue.
					firstDetectedFailureLocation = failureLocation
				} else if v, ok := newIssues[failureLocation]; ok {
					// This comment was added for a failure that happened in the same test but in a different
					// method.
					// We have already seen other comments like this one. So a new jira issue has been already
					// created. Simply add this comment to the new jira issue and remove it from the issue we
					// were request to split into multiple.
					if _, resp, err := jiraClient.Issue.AddCommentWithContext(ctx, v.ID, newComment); err != nil {
						body, _ := io.ReadAll(resp.Body)
						logger.Info(fmt.Sprintf("Failed to add new comment to issue %s. Err: %v. Body: %s", v.Key, err, string(body)))
						continue
					}
					// Remove comment from issue
					if err := jiraClient.Issue.DeleteCommentWithContext(ctx, issueToSplit.ID, c.ID); err != nil {
						logger.Info(fmt.Sprintf("Failed to delete comment from issue %s. Err %v", issueToSplit.Key, err))
						continue
					}
				} else {
					// This comment is referencing a failure never seen so far. If another existing open issue exists matching
					// current failure, use such an issue. Otherwise create new issue.
					// Then move comment.
					if newIssue, err := useExistingOrCreateNewIssue(ctx, jiraClient, project, activeSprint,
						issueToSplit.Fields.Assignee.Name, issueToSplit.Fields.Summary, failureLocation, existingFailureMap, logger); err != nil {
						logger.Info(fmt.Sprintf("Failed to create new issue. Err: %v", err))
						continue
					} else {
						newIssues[failureLocation] = newIssue
						if _, resp, err := jiraClient.Issue.AddCommentWithContext(ctx, newIssue.ID, newComment); err != nil {
							body, _ := io.ReadAll(resp.Body)
							logger.Info(fmt.Sprintf("Failed to add new comment to issue %s. Err: %v. Body: %s", newIssue.ID, err, string(body)))
							continue
						}
						// Remove comment from issue
						if err := jiraClient.Issue.DeleteCommentWithContext(ctx, issueToSplit.ID, c.ID); err != nil {
							logger.Info(fmt.Sprintf("Failed to delete comment from issue %s. Err: %v", issueToSplit.Key, err))
							continue
						}
					}
				}
			}
		}
	}

	result := make([]string, 0)
	for _, value := range newIssues {
		result = append(result, value.Key)
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
	scanner := bufio.NewScanner(strings.NewReader(c.Body))
	for scanner.Scan() {
		text := scanner.Text()
		if strings.Contains(text, fullStackTraceText) {
			addressIndex := strings.Index(text, "0x")
			if addressIndex != -1 {
				return text[:addressIndex], nil
			}
			return text, nil
		}
	}
	return "", nil
}

func Reverse(s []float64) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// buildFailureMap considers all open issues and builds a map: <failure location>: <jira issue>
// consideri all open issues, excluding:
// - any issue with multiple failures.
func buildFailureMap(ctx context.Context, jiraClient *jira.Client, openIssues []jira.Issue,
	logger logr.Logger) map[string]*jira.Issue {
	failureMap := make(map[string]*jira.Issue)

	options := &jira.GetQueryOptions{Expand: "renderedFields"}

	// For every open issue filed by Atom user, get comments.
	for i := range openIssues {
		u, _, err := jiraClient.Issue.Get(openIssues[i].Key, options)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to query renderedFields for issue %s. Error: %v\n", openIssues[i].Key, err))
			continue
		}

		// Walk all comments added by Atom user to this specific issue.
		// Any comment is a failure (different run for sure, possibly different method as well).
		// If an issue contains comment(s) representing a single issue, add it to failureMap.
		// Otherwise an issue with different type of failures is ignored.
		ignore := false
		var firstDetectedFailureLocation string
		for _, c := range u.RenderedFields.Comments.Comments {
			if c.Author.Name == AtomUser { // Consider only comment added by Atom user
				if failureLocation, err := GetFunctionName(c, logger); err == nil {
					if failureLocation == "" {
						// Nothing to do. We were not able to identify in which method issue failed
						// when this comment was added
						continue
					} else if firstDetectedFailureLocation == "" || failureLocation == firstDetectedFailureLocation {
						// First issue or a comment added for a different run. But the method were failure happened
						// matches first failure reported in this issue.
						firstDetectedFailureLocation = failureLocation
					} else {
						// This issue contains multiple different failires. Ignore it
						ignore = true
					}
				}
			}
		}
		if !ignore && firstDetectedFailureLocation != "" {
			failureMap[firstDetectedFailureLocation] = &openIssues[i]
		}
	}

	return failureMap
}

// Looking at already existing issues, if one exists matching current failure, use such issue.
// If no existing issue is found matching current failure, create a new one.
func useExistingOrCreateNewIssue(ctx context.Context, jiraClient *jira.Client,
	project *jira.Project, activeSprint *jira.Sprint,
	assignee, summary, failureLocation string,
	existingFailureMap map[string]*jira.Issue,
	logger logr.Logger) (*jira.Issue, error) {
	// Look at existing open failures.If one matches failureLocation, use that jira Issue
	for k, v := range existingFailureMap {
		if failureLocation == k {
			return v, nil
		}
	}

	// No existing open issue matches current failure Location. Create new issue,.
	priority := jira.Priority{Name: "P1"}

	// This comment is referencing a failure never seen so far. So first create new issue then move comment.
	newIssue, err := jira_utils.CreateIssue(ctx, jiraClient, activeSprint, &priority, project.Key, "e2e",
		assignee, "", summary, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to create new issue. Err: %v", err))
		return nil, err
	}

	return newIssue, nil
}

func getJiraProjectAndActiveSprint(ctx context.Context, jiraClient *jira.Client, logger logr.Logger) (*jira.Project, *jira.Sprint, error) {
	project, err := jira_utils.GetJiraProject(ctx, jiraClient, "", logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project. Err: %v", err))
		return nil, nil, err
	}

	board, err := jira_utils.GetJiraBoard(ctx, jiraClient, project.Key, LCSBoardName, logger)
	if err != nil || board == nil {
		logger.Info(fmt.Sprintf("Failed to get jira board. Err %v", err))
		return nil, nil, err
	}

	activeSprint, err := jira_utils.GetJiraActiveSprint(ctx, jiraClient, fmt.Sprintf("%d", board.ID), logger)
	if err != nil || activeSprint == nil {
		logger.Info(fmt.Sprintf("Failed to get active sprint. Err: %v", err))
		return nil, nil, err
	}

	return project, activeSprint, nil
}

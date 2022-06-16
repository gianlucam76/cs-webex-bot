package learning

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"

	jira_utils "github.com/gianlucam76/jira_utils/jira"
	"github.com/gianlucam76/webex_bot/utils"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

const (
	analyzedIssueFilename = "/tmp/analyzed_open_issue"
)

var (
	// resolvedIssueOwners is a map in the form: key: <method name> value: <engineer name>: <jira id> where:
	// - <method name> is the method where e2e failed when jira issue was created
	// - <engineer name> is the name of the engineer the resolved jira bug was assigned to.
	// This map is periodically updated.
	resolvedIssueOwners map[string]string

	// Contains list of methods that should not be reassigned
	skipReassignment []string
)

func init() {
	resolvedIssueOwners = make(map[string]string)

	skipReassignment = []string{
		"utils.checkContainerStatuses", // this indicates a pod crashed. So don't reassign
	}
}

// AnalyzeOpenIssues:
// - consider all resolved issues file by atomUser during e2e automatic tagging;
// - update <method name>:<engineer name> map accordingly
// It also, twice a day considers open issues (no more than once per issue). If it finds a
// resolved issue with matching method (method is the function where e2e failed) sends a
// message.
// Open issues, before being analyzed, are stored to a file. Any open issue find in such
// a file won't be analyzed again.
func AnalyzeOpenIssues(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client, logger logr.Logger) {
	_ = gocron.Every(1).Day().At("23:30:00").Do(fromResolvedIssues, ctx, jiraClient, logger)
	_ = gocron.Every(1).Day().At("8:00:00").Do(splitOpenIssues, ctx, webexClient, roomID, jiraClient, logger)
	_ = gocron.Every(1).Day().At("8:30:00").Do(reassignOpenIssues, ctx, webexClient, roomID, jiraClient, logger)
	_ = gocron.Every(1).Day().At("18:30:00").Do(reassignOpenIssues, ctx, webexClient, roomID, jiraClient, logger)
}

// fromResolvedIssues gets list of all resolved bugs filed by atomUser during e2e tagging sanity.
// It creates a map: <method name>:<engineer name> where:
// - <method name> is the method where e2e failed when jira issue was created
// - <engineer name> is the name of the engineer the resolved jira bug was assigned to (we assume this
// engineer is the one who fixed the issue).
// When a test fails, e2e jira issue is filed and assigned to test owner. But looking at past, we might learn
// whom is more appropriate to assign the bug to.
func fromResolvedIssues(ctx context.Context, jiraClient *jira.Client, logger logr.Logger) {
	resolvedIssueOwners = make(map[string]string)

	project, err := jira_utils.GetJiraProject(ctx, jiraClient, "", logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project. Err: %v", err))
		return
	}

	jql := fmt.Sprintf("Status IN (Resolved) and reporter = %s and project = %s",
		utils.AtomUser, project.Name)
	options := &jira.SearchOptions{
		MaxResults: 100,
	}
	issues, _, err := jiraClient.Issue.SearchWithContext(ctx, jql, options)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get open issues. Error: %v", err))
		return
	}

	for i := range issues {
		issue := issues[i]
		logger.Info(fmt.Sprintf("Considering resolved issue %s", issue.Key))

		options := &jira.GetQueryOptions{Expand: "renderedFields"}
		u, _, err := jiraClient.Issue.Get(issue.Key, options)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to query renderedFields. Error: %v\n", err))
			continue
		}

		// Comments are returned first to last in order of time.
		// Created is not a time but a string, so cannot really be used as time.
		var fullStackTrace string
		for _, c := range u.RenderedFields.Comments.Comments {
			if c.Author.Name == utils.AtomUser {
				if tmp, err := utils.GetFunctionName(c, logger); err == nil {
					fullStackTrace = tmp
				}
			}
		}
		if fullStackTrace != "" {
			resolvedIssueOwners[fullStackTrace] = fmt.Sprintf("%s:%s", issue.Fields.Assignee.Name, issue.Key)
		}
	}
}

// reassignOpenIssues considers all open issues file by atomUser during e2e tagging sanities.
// Looking at past resolved issues, reassign issue accordingly if needed.
// atomUser assigns issue to test owner. Every issue comment contains an entry pointing to the
// method were issue happened.
// Looking at past resolved issues, a map of <method name> : <engineer name> is created (where engineer
// name is the user id of the engineer that resolved the issue).
// We assume that this map is more precise the test owner. So issue is reassigned if a match is found.
func reassignOpenIssues(ctx context.Context, webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client, logger logr.Logger) {
	analyzedIssues, err := loadAnalyzedIssues(logger)
	if err != nil {
		return
	}

	project, err := jira_utils.GetJiraProject(ctx, jiraClient, "", logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project. Err: %v", err))
		return
	}

	// Fetch open issues
	jql := fmt.Sprintf("Status NOT IN (Resolved,Closed) and reporter = atom-ci.gen and project = %s",
		project.Name)

	openIssues, _, err := jiraClient.Issue.SearchWithContext(ctx, jql, nil)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get open issues. Error: %v", err))
		return
	}

	for i := range openIssues {
		issue := openIssues[i]

		if _, ok := analyzedIssues[issue.Key]; ok {
			continue
		}

		options := &jira.GetQueryOptions{Expand: "renderedFields"}
		u, _, err := jiraClient.Issue.Get(issue.Key, options)

		if err != nil {
			logger.Info(fmt.Sprintf("Failed to query renderedFields. Error: %v\n", err))
			continue
		}

		// Comments are returned first to last in order of time.
		// Created is not a time but a string, so cannot really be used as time.
		var fullStackTrace string
		for _, c := range u.RenderedFields.Comments.Comments {
			if c.Author.Name == utils.AtomUser {
				if tmp, err := utils.GetFunctionName(c, logger); err == nil {
					fullStackTrace = tmp
				}
			}
		}

		// First save to file, only if that succeeds analyze file and eventually send webex message.
		// This order is necessary to avoid sending messages for the same issues multiple times.
		f, err := os.OpenFile(analyzedIssueFilename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Info("Failed to open file %s for write", analyzedIssueFilename)
			continue
		}
		defer f.Close()
		if _, err := f.WriteString(fmt.Sprintf("%s\n", issue.Key)); err != nil {
			logger.Info("Failed to append to file %s", analyzedIssueFilename)
			continue
		}

		if shouldTryToReassign(fullStackTrace) {
			if resolvedIssueData, ok := resolvedIssueOwners[fullStackTrace]; ok {
				info := strings.Split(resolvedIssueData, ":")
				resolvedIssueEngineer := info[0]
				oldIssueID := info[1]
				if resolvedIssueEngineer != issue.Fields.Assignee.Name {
					msg := fmt.Sprintf("%s currently assigned to <@personEmail:%s@cisco.com|%s> is in an area (%s) similar to %s previously solved by <@personEmail:%s@cisco.com|%s>  \n",
						issue.Key, issue.Fields.Assignee.Name, issue.Fields.Assignee.Name, fullStackTrace,
						oldIssueID, resolvedIssueEngineer, resolvedIssueEngineer)
					msg += "Maybe issue owner should be changed."
					logger.Info(msg)
					if err = webex_utils.SendMessage(webexClient, roomID, msg, logger); err != nil {
						logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
					}
				}
			}
		}
	}
}

// splitOpenIssues considers all open issues file by atomUser during e2e tagging sanities.
// issues filed by atomUser during e2e tagging sanities are per test. If a given test fails due
// to different reasons (for instance SynchronizedBeforeSuite might fail due to different reasons)
// only a new comment to same issue is added.
// This method looks at open issue and if different failures are found, a new bug is filed.
func splitOpenIssues(ctx context.Context, webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client, logger logr.Logger) {
	project, err := jira_utils.GetJiraProject(ctx, jiraClient, "", logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project. Err: %v", err))
		return
	}

	// Fetch open issues
	jql := fmt.Sprintf("Status NOT IN (Resolved,Closed) and reporter = atom-ci.gen and project = %s",
		project.Name)

	openIssues, _, err := jiraClient.Issue.SearchWithContext(ctx, jql, nil)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get open issues. Error: %v", err))
		return
	}

	for i := range openIssues {
		issue := openIssues[i]
		options := &jira.GetQueryOptions{Expand: "renderedFields"}
		u, _, err := jiraClient.Issue.Get(issue.Key, options)

		if err != nil {
			logger.Info(fmt.Sprintf("Failed to query renderedFields. Error: %v\n", err))
			continue
		}

		logger.Info(fmt.Sprintf("Considering issue %s", issue.Key))
		fullStackTraces := make(map[string]bool)
		for _, c := range u.RenderedFields.Comments.Comments {
			if c.Author.Name == utils.AtomUser {
				if tmp, err := utils.GetFunctionName(c, logger); err == nil {
					fullStackTraces[tmp] = true
				}
			}
		}

		if len(fullStackTraces) > 1 {
			msg := fmt.Sprintf("[%s](https://jira-eng-sjc10.cisco.com/jira/browse/%s) contains different failures  \n",
				issue.Key, issue.Key)
			for k := range fullStackTraces {
				msg += fmt.Sprintf("%s  \n", k)
			}
			msg += "  \nPlease considering splitting this issue into multiple ones so we can debug all."
			msg += fmt.Sprintf("If you prefer me to do it, please ask me to (\"split %s\")", issue.Key)
			logger.Info(msg)
			if err = webex_utils.SendMessage(webexClient, roomID, msg, logger); err != nil {
				logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
			}
		}
	}
}

// loadAnalyzedIssues loads analyzed issues
func loadAnalyzedIssues(logger logr.Logger) (map[string]bool, error) {
	analyzedIssues := make(map[string]bool, 0)

	if _, err := os.Stat(analyzedIssueFilename); err == nil {
		file, err := os.Open(analyzedIssueFilename)
		if err != nil {
			logger.Info("Failed to read file %s with analyzed issues", analyzedIssueFilename)
			return nil, err
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			analyzedIssues[scanner.Text()] = true
		}
	}

	return analyzedIssues, nil
}

func shouldTryToReassign(failedMethod string) bool {
	for i := range skipReassignment {
		if strings.Contains(failedMethod, skipReassignment[i]) {
			return false
		}
	}

	return true
}

package analyze

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"
	"github.com/gonum/stat"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	jira_utils "github.com/gianlucam76/jira_utils/jira"
	"github.com/gianlucam76/webex_bot/utils"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

const jiraProject = "CLOUDSTACK"
const jiraBoard = "CloudStack - LCS"

// Every other day
const intervalPeriod time.Duration = 48 * time.Hour

const tickAtHour int = 9
const tickAtMinute int = 00
const tickAtSecond int = 00

// Minimum number of successfull runs needed.
const numberOfRuns int = 8

// When relative standard deviation is higher than this value
// an action is taken
const rsdThreshold float64 = 10

// CheckTestDurationOnUCS every day, for each test in UCS:
// - consider the last 8 runs.
func CheckTestDurationOnUCS(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	jobTicker := &jobTicker{}
	jobTicker.updateTimer()
	for {
		<-jobTicker.timer.C
		logger.Info(fmt.Sprintln(time.Now(), "- check test durations"))
		evaluateUCSTest(ctx, webexClient, roomID, jiraClient, logger)
		jobTicker.updateTimer()
	}
}

func evaluateUCSTest(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	testNames, err := utils.BuildTests(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get tests. Err: %v", err))
		return
	}

	for i := range testNames {
		ucsResults, err := es_utils.GetResults(ctx, logger,
			"",           // no specific run
			testNames[i], // for this specific test
			false,        // no vcs
			true,         // only ucs
			true,         // only results where test passed
			false,        // no failed
			false,        // no skipped
			numberOfRuns) // last numberOfRuns runs
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testNames[i], err))
			return
		}

		var mantainer string
		var rtyp es_utils.Result
		data := make([]float64, 0)
		for _, item := range ucsResults.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Result)
			mantainer = r.Maintainer
			data = append(data, r.DurationInMinutes)
		}

		mean, std := stat.MeanStdDev(data, nil)
		rsd := std * 100 / mean
		logger.Info(fmt.Sprintf("Test: %s Mean: %f Standard Deviation: %f Relative Standard Deviation: %f",
			testNames[i], mean, std, rsd))

		if rsd > rsdThreshold {
			textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s>  \nI detected something which I believe needs to be looked at.  \n",
				mantainer, mantainer)
			textMessage += fmt.Sprintf("Test: %s The relative standard deviation: %f is higher than threshold: %f. Mean value: %f. UCS samples: %d.  \n",
				testNames[i], rsd, rsdThreshold, mean, numberOfRuns)
			issueKey, err := fileJiraIssues(ctx, jiraClient, jiraProject, jiraBoard, testNames[i], mantainer, logger)
			if err == nil {
				textMessage += fmt.Sprintf("Issue: [%s](https://jira-eng-sjc10.cisco.com/jira/browse/%s)  \n",
					issueKey, issueKey)
			}
			if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
				logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
			}
		}
	}
}

func fileJiraIssues(ctx context.Context, jiraClient *jira.Client,
	projectName, boardName, testName, maintainer string,
	logger logr.Logger) (string, error) {
	summary := fmt.Sprintf("Test: %s relative standard deviation too large", testName)

	project, err := jira_utils.GetJiraProject(ctx, jiraClient, projectName, logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project %s. Err %v", projectName, err))
		return "", err
	}

	board, err := jira_utils.GetJiraBoard(ctx, jiraClient, project.Key, boardName, logger)
	if err != nil || board == nil {
		logger.Info(fmt.Sprintf("Failed to get jira board %s. Err %v", boardName, err))
		return "", err
	}

	activeSprint, err := jira_utils.GetJiraActiveSprint(ctx, jiraClient, fmt.Sprintf("%d", board.ID), logger)
	if err != nil || activeSprint == nil {
		logger.Info(fmt.Sprintf("Failed to get jira active sprint %s. Err %v", boardName, err))
		return "", err
	}

	jql := fmt.Sprintf("reporter = %s and type = Bug and Status NOT IN (Resolved,Closed)", jira_utils.GetUsername(logger))
	openIssues, err := jira_utils.GetJiraIssues(ctx, jiraClient, jql, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get jira open issue %s. Err %v", boardName, err))
		return "", err
	}

	priority := jira.Priority{Name: "P3"}

	if openIssue := findExistingIssue(openIssues, summary); openIssue != nil {
		jira_utils.MoveIssueToSprint(ctx, jiraClient, activeSprint.ID, openIssue.ID, logger)
		return openIssue.Key, nil
	}

	issueKey, err := jira_utils.CreateIssue(ctx, jiraClient, activeSprint, &priority, project.Key,
		"e2e", maintainer, testName, summary, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to create jira issue. Err %v", err))
		return "", err
	}

	jira_utils.MoveIssueToSprint(ctx, jiraClient, activeSprint.ID, issueKey, logger)

	return issueKey, nil
}

func findExistingIssue(openIssues []jira.Issue, summary string) *jira.Issue {
	for i := range openIssues {
		if openIssues[i].Fields.Summary == summary {
			return &openIssues[i]
		}
	}
	return nil
}

package analyze

import (
	"context"
	"fmt"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"

	jira_utils "github.com/gianlucam76/jira_utils/jira"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

// OpenIssues checks currently open issues. If at least one is open,
// send a message.
func OpenIssues(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	_ = gocron.Every(1).Thursday().At("11:30:00").Do(sendOpenIssue,
		ctx, webexClient, roomID, jiraClient, logger)
	_ = gocron.Every(1).Sunday().At("11:30:00").Do(sendOpenIssue,
		ctx, webexClient, roomID, jiraClient, logger)
}

func sendOpenIssue(ctx context.Context, webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client, logger logr.Logger) {
	logger.Info("Preparing open issue report")

	project, err := jira_utils.GetJiraProject(ctx, jiraClient, "", logger)
	if err != nil || project == nil {
		logger.Info(fmt.Sprintf("Failed to get jira project. Err: %v", err))
		return
	}

	jql := fmt.Sprintf("Status NOT IN (Resolved,Closed) and reporter = atom-ci.gen and project = %s",
		project.Name)
	issues, err := jira_utils.GetJiraIssues(ctx, jiraClient, jql, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get open issues. Err: %v", err))
		return
	}

	if len(issues) == 0 {
		logger.Info("There are currently no open issues. Nothing to do.")
		return
	}

	logger.Info(fmt.Sprintf("Found %d open issues", len(issues)))

	textMessage := "Hello cloudstack team here is the list of current open issues:  \n"
	for i := range issues {
		createdTime := time.Time(issues[i].Fields.Created)
		diff := time.Since(createdTime)
		lastUpdate := fmt.Sprintf("%d days", int(diff.Hours()/24))
		assignee := issues[i].Fields.Assignee.Name
		textMessage += fmt.Sprintf("[%s](https://jira-eng-sjc10.cisco.com/jira/browse/%s). Issue opened %s ago. Assignee <@personEmail:%s@cisco.com|%s>  \n",
			issues[i].Key, issues[i].Key, lastUpdate, assignee, assignee)
	}

	if err = webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

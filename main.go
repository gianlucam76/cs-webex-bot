package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	jira_utils "github.com/gianlucam76/jira_utils/jira"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

const (
	defaultPollInterval = 20 * time.Second
	webexRoom           = "E2E_WEBEX_ROOM"
	issueText           = "issues"
	vcsText             = "vcs"
	vcsLink             = "https://cs-aci-jenkins.cisco.com:8443/job/Production/job/Cloudstack/job/Cloudstack-Virtual-Sanity/"
	ucsText             = "ucs"
	ucsLink             = "https://cs-aci-jenkins.cisco.com:8443/job/Production/job/Cloudstack/job/Cloudstack-UCS-Sanity/"
)

var pollInterval time.Duration

func main() {
	ctx := context.Background()

	klog.InitFlags(nil)
	logger := klogr.New()

	initFlags(pflag.CommandLine)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	webexClient := webex_utils.GetClient(logger)

	roomName := getRoom(logger)

	logger = logger.WithValues("roomName", roomName)
	room, err := webex_utils.GetRoom(webexClient, roomName, logger)
	if err != nil {
		logger.Info("Failed to get room")
		return
	}

	jiraClient, err := jira_utils.GetJiraClient(ctx, jira_utils.GetUsername(logger), jira_utils.GetPassword(logger), logger)
	if err != nil {
		logger.Info("Failed to get jira client")
		return
	}

	// Bot will respond to one message at a time.
	// If this is not set, last message sent to bot will be answered. Otherwise last
	// message sent to bot after this will be answered
	lastMessageID := ""

	now := time.Now()
	maxOldMessageTimestamp := now.Add(-5 * pollInterval)

	for {
		messages, err := webex_utils.GetMessages(webexClient, room.ID, logger)
		if err != nil {
			return
		}

		logger.Info(fmt.Sprintf("Got messages (%d)", len(messages.Items)))
		for i := range messages.Items {
			m := &messages.Items[i]
			from := m.PersonEmail

			if lastMessageID != "" && m.ID == lastMessageID {
				logger.Info("No more messages to answer")
				break
			} else if m.Created.Before(maxOldMessageTimestamp) {
				logger.Info("No new messages to answer")
				break
			} else {
				if strings.Contains(m.Text, issueText) {
					handleOpenIssueRequest(ctx, webexClient, jiraClient, room.ID, from, logger)
				} else if strings.Contains(m.Text, vcsText) {
					handleVcsResultRequest(ctx, webexClient, room.ID, from, logger)
				} else if strings.Contains(m.Text, ucsText) {
					handleUcsResultRequest(ctx, webexClient, room.ID, from, logger)
				} else if testName, isMatch, err := doesMatchTest(ctx, webexClient, room.ID, from, m.Text, logger); err == nil {
					if isMatch {
						sendMessageWithTestResult(ctx, webexClient, room.ID, from, testName, logger)
					} else {
						sendDefaultResponse(ctx, webexClient, room.ID, from, m.Text, logger)
					}
				}
			}
		}

		// Messages are always from last sent backwards in time
		if len(messages.Items) > 0 {
			lastMessageID = messages.Items[0].ID
		}

		time.Sleep(pollInterval)
	}
}

func getRoom(logger logr.Logger) string {
	room, ok := os.LookupEnv(webexRoom)
	if !ok {
		logger.Info(fmt.Sprintf("Env variable %s supposed to contain webex room not found", webexRoom))
		panic(1)
	}

	if room == "" {
		logger.Info("Room cannot be emty")
		panic(1)
	}

	return room
}

func handleOpenIssueRequest(ctx context.Context, webexClient *webexteams.Client,
	jiraClient *jira.Client, roomID, from string, logger logr.Logger) {
	logger.Info("Handling open issue request")

	jql := "Status NOT IN (Resolved,Closed) and reporter = atom-ci.gen"
	issues, err := jira_utils.GetJiraIssues(ctx, jiraClient, jql, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get open issues. Err: %v", err))
		return
	}

	textMessage := fmt.Sprintf("Hello <@personEmail:%s|%s> thanks for your question.\n\n",
		from, from)

	if len(issues) == 0 {
		textMessage += "There are currently no open jira issues"
	} else {
		textMessage += "Here is the list of open issues:\n\n"
		for i := range issues {
			textMessage += fmt.Sprintf("Issue: [%s](https://jira-eng-sjc10.cisco.com/jira/browse/%s)\n\n",
				issues[i].Key, issues[i].Key)
		}
	}

	if err = webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

func sendDefaultResponse(ctx context.Context, webexClient *webexteams.Client,
	roomID, from, message string, logger logr.Logger) {
	logger.Info(fmt.Sprintf("Sending default response. Failed to understand %q", message))

	if err := webex_utils.SendMessageWithCard(webexClient, roomID, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

func handleVcsResultRequest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from string, logger logr.Logger) {
	lastRun, err := getLastRun(ctx, true, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get last run ID. Err: %v", err))
		return
	}

	if lastRun != 0 {
		results, err := es_utils.GetResults(ctx, logger,
			fmt.Sprintf("%d", lastRun), // from this run
			"",                         // no specific test
			true,                       // from vcs
			false,                      // no ucs
			false,                      // no passed
			true,                       // get failed tests
			false,                      // no skipped
			100,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in vcs run %d from elastic DB. Err: %v", lastRun, err))
			return
		}

		textMessage := fmt.Sprintf("Hello <@personEmail:%s|%s> thanks for your question.\n\n",
			from, from)

		var rtyp es_utils.Result
		failedTests := false
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			failedTests = true
			r := item.(es_utils.Result)
			textMessage += fmt.Sprintf("Test %s failed in vcs run [%d](%s/%d) \n\n",
				r.Name, lastRun, vcsLink, lastRun)
		}
		if !failedTests {
			textMessage += fmt.Sprintf("No tests failed in vcs run [%d](%s/%d)",
				lastRun, vcsLink, lastRun)
		}

		if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
	}
}

func handleUcsResultRequest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from string, logger logr.Logger) {
	lastRun, err := getLastRun(ctx, false, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get last run ID. Err: %v", err))
		return
	}

	if lastRun != 0 {
		results, err := es_utils.GetResults(ctx, logger,
			fmt.Sprintf("%d", lastRun), // from this run
			"",                         // no specific test
			false,                      // no vcs
			true,                       // from ucs
			false,                      // no passed
			true,                       // get failed tests
			false,                      // no skipped
			100,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in ucs run %d from elastic DB. Err: %v", lastRun, err))
			return
		}

		textMessage := fmt.Sprintf("Hello <@personEmail:%s|%s> thanks for your question.\n\n",
			from, from)

		var rtyp es_utils.Result
		failedTests := false
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			failedTests = true
			r := item.(es_utils.Result)
			textMessage += fmt.Sprintf("Test %s failed\n\n in ucs run [%d](%s/%d)",
				r.Name, lastRun, ucsLink, lastRun)
		}
		if !failedTests {
			textMessage += fmt.Sprintf("No tests failed in ucs run [%d](%s/%d)",
				lastRun, ucsLink, lastRun)
		}

		if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
	}
}

// doesMatchTest returns true if message contain a test name along with test name
// retuns false otherwise
func doesMatchTest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from, message string, logger logr.Logger) (string, bool, error) {
	testName, err := buildTests(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get tests. Err: %v", err))
		return "", false, err
	}

	for i := range testName {
		if strings.Contains(message, testName[i]) {
			return testName[i], true, nil
		}
	}

	return "", false, nil
}

// buildTests creates:
// - a slice containing all test names
// - a map containing for each test its descriptions
func buildTests(ctx context.Context, logger logr.Logger) (testName []string, err error) {
	testName = make([]string, 0)

	lastRun, err := getLastRun(ctx, false, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get last run ID. Err: %v", err))
		return
	}

	if lastRun != 0 {
		results, err := es_utils.GetResults(ctx, logger,
			fmt.Sprintf("%d", lastRun), // from this run
			"",                         // no specific test
			false,                      // no vcs. VCS has subsets of tests.
			true,                       // from ucs. UCS has all tests.
			true,                       // no passed
			true,                       // get failed tests
			true,                       // no skipped
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in ucs run %d from elastic DB. Err: %v", lastRun, err))
			return nil, err
		}

		var rtyp es_utils.Result
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Result)
			testName = append(testName, r.Name)
		}
	}

	return
}

// getLastRun returns last run ID.
// vcs bool controls whether that is going to be for last VCS run or UCS run
func getLastRun(ctx context.Context, vcs bool, logger logr.Logger) (int64, error) {
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

func sendMessageWithTestResult(ctx context.Context, webexClient *webexteams.Client,
	roomID, from, testName string, logger logr.Logger) {
	vcsResults, err := es_utils.GetResults(ctx, logger, "", testName, true, false, false, false, false, 20)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testName, err))
		return
	}
	ucsResults, err := es_utils.GetResults(ctx, logger, "", testName, false, true, false, false, false, 20)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testName, err))
		return
	}

	textMessage := fmt.Sprintf("Hello <@personEmail:%s|%s> thanks for your question.\n\n",
		from, from)

	var rtyp es_utils.Result
	for _, item := range vcsResults.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		textMessage += fmt.Sprintf("Test %s result: %s in VCS run [%d](%s/%d)\n\n",
			r.Name, r.Result, r.Run, vcsLink, r.Run)
	}
	for _, item := range ucsResults.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		textMessage += fmt.Sprintf("Test %s result: %s in UCS run [%d](%s/%d)\n\n",
			r.Name, r.Result, r.Run, ucsLink, r.Run)
	}

	if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

func initFlags(fs *pflag.FlagSet) {
	flag.DurationVar(&pollInterval,
		"poll-interval",
		defaultPollInterval,
		"The minimum interval at which watched resources are reconciled (e.g. 10m)",
	)
}

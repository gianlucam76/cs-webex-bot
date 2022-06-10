package main

import (
	"context"
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"
	"github.com/gonum/stat"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"
	"github.com/johnfercher/maroto/pkg/consts"
	"github.com/johnfercher/maroto/pkg/pdf"
	"github.com/johnfercher/maroto/pkg/props"
	gim "github.com/ozankasikci/go-image-merge"
	"github.com/spf13/pflag"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	jira_utils "github.com/gianlucam76/jira_utils/jira"
	"github.com/gianlucam76/webex_bot/analyze"
	"github.com/gianlucam76/webex_bot/learning"
	"github.com/gianlucam76/webex_bot/utils"
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
	pieChartText        = "charts"
	reportText          = "reports"
	summaryText         = "summary"
	splitIssueText      = "split"
)

var pollInterval time.Duration

func main() {
	ctx := context.Background()

	klog.InitFlags(nil)

	if err := flag.Lookup("v").Value.Set("5"); err != nil {
		os.Exit(1)
	}

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

	// Send weekly reports on UCS and VCS failed test stats
	analyze.WeeklyStats(ctx, webexClient, room.ID, logger)

	// Check time duration for UCS tests. Send webex message when the time relative standard deviation
	// is too large
	analyze.CheckTestDurationOnUCS(ctx, webexClient, room.ID, logger)

	// Analyze reports for UCS tests. Send webex message when the time relative standard deviation
	// is too large
	analyze.CheckReportDurationOnUCS(ctx, webexClient, room.ID, logger)

	// Send reports on currently open issues
	analyze.OpenIssues(ctx, webexClient, room.ID, jiraClient, logger)

	// Generate pie charts for UCS and VCS considering test durations
	analyze.CreatePieCharts(ctx, webexClient, room.ID, logger)

	learning.AnalyzeOpenIssues(ctx, webexClient, room.ID, jiraClient, logger)

	go startCron()

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
				} else if strings.Contains(m.Text, pieChartText) {
					handlePieChartRequest(ctx, webexClient, room.ID, from, logger)
				} else if strings.Contains(m.Text, reportText) {
					handleReportRequest(ctx, webexClient, room.ID, from, logger)
				} else if strings.Contains(m.Text, summaryText) {
					handleSummaryRequest(ctx, webexClient, room.ID, from, logger)
				} else if strings.Contains(m.Text, splitIssueText) {
					handleSplitIssueRequest(ctx, webexClient, room.ID, from, m.Text, jiraClient, logger)
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

	issues, err := utils.GetOpenIssues(ctx, jiraClient, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get open issues. Err: %v", err))
		return
	}

	textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
		from, from)

	if len(issues) == 0 {
		textMessage += "There are currently no open jira issues"
	} else {
		textMessage += "Here is the list of open issues:  \n"
		for i := range issues {
			createdTime := time.Time(issues[i].Fields.Created)
			diff := time.Since(createdTime)
			lastUpdate := fmt.Sprintf("%d days", int(diff.Hours()/24))
			assignee := issues[i].Fields.Assignee.Name
			textMessage += fmt.Sprintf("[%s](https://jira-eng-sjc10.cisco.com/jira/browse/%s). Issue opened %s ago. Assignee <@personEmail:%s@cisco.com|%s>  \n",
				issues[i].Key, issues[i].Key, lastUpdate, assignee, assignee)
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
	lastRun, err := utils.GetLastRun(ctx, true, logger)
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
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in vcs run %d from elastic DB. Err: %v", lastRun, err))
			return
		}

		textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
			from, from)

		var rtyp es_utils.Result
		failedTests := false
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			failedTests = true
			r := item.(es_utils.Result)
			textMessage += fmt.Sprintf("Test %s failed in vcs run [%d](%s/%d) ❌  \n",
				r.Name, lastRun, vcsLink, lastRun)
		}
		if !failedTests {
			textMessage += fmt.Sprintf("No tests failed in vcs run [%d](%s/%d) 🥇   \n",
				lastRun, vcsLink, lastRun)
		}

		if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
	}
}

func handleUcsResultRequest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from string, logger logr.Logger) {
	lastRun, err := utils.GetLastRun(ctx, false, logger)
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
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in ucs run %d from elastic DB. Err: %v", lastRun, err))
			return
		}

		textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
			from, from)

		var rtyp es_utils.Result
		failedTests := false
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			failedTests = true
			r := item.(es_utils.Result)
			textMessage += fmt.Sprintf("Test %s failed in ucs run [%d](%s/%d) 💔  \n",
				r.Name, lastRun, ucsLink, lastRun)
		}
		if !failedTests {
			textMessage += fmt.Sprintf("No tests failed in ucs run [%d](%s/%d) 🥇  \n",
				lastRun, ucsLink, lastRun)
		}

		if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
	}
}

func handlePieChartRequest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from string, logger logr.Logger) {
	logger.Info("Handling pie chart request")

	if fileName, err := analyze.CreateDurationPieChart(ctx, true, roomID, logger); err == nil {
		textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
			from, from)
		textMessage += "please open the attached file to see test duration pie chart from last VCS run  \n"
		if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage, []string{fileName}, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
	}
	// create pie chart for ucs
	if fileName, err := analyze.CreateDurationPieChart(ctx, false, roomID, logger); err == nil {
		textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
			from, from)
		textMessage += "please open the attached file to see test duration pie chart from last UCS run  \n"
		if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage, []string{fileName}, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
	}
}

func handleReportRequest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from string, logger logr.Logger) {
	logger.Info("Handling report request")
	files, err := getReportFiles(ctx, logger)
	if err != nil {
		return
	}

	sort.Strings(files)

	grids := make([]*gim.Grid, 0)
	for i := range files {
		tmpGrid := gim.Grid{ImageFilePath: files[i]}
		grids = append(grids, &tmpGrid)
	}
	rgba, err := gim.New(grids, 3, 3).Merge()
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to create grid. Error %v", err))
		return
	}

	gridFileName := "/tmp/report_grid.png"
	file, err := os.Create(gridFileName)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to create grid file. Error %v", err))
		return
	}

	if err = png.Encode(file, rgba); err != nil {
		logger.Info(fmt.Sprintf("Failed to encode grid file. Error %v", err))
		return
	}

	textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
		from, from)
	textMessage += "Please find attached the cloudstack e2e report duration plots"

	if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage,
		[]string{gridFileName}, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

func handleSplitIssueRequest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from, text string, jiraClient *jira.Client, logger logr.Logger) {
	logger.Info("Handling split request")

	index := strings.Index(text, splitIssueText)
	if index == -1 {
		logger.Info(fmt.Sprintf("Failed to find %s in `%s`", splitIssueText, text))
	}

	openIssues, err := utils.GetOpenIssues(ctx, jiraClient, logger)
	if err != nil {
		return
	}

	re := regexp.MustCompile(`[-]?\d[\d,]*`)
	submatchall := re.FindAllString(text[index:], -1)

	if len(submatchall) == 0 || len(submatchall) > 1 {
		msg := fmt.Sprintf("I am able to split only one issue at time. Found %d in `%s`",
			len(submatchall), text)
		logger.Info(msg)
		if err := webex_utils.SendMessage(webexClient, roomID, msg, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
		return
	}

	found := false
	var webexMessage string
	element := submatchall[0]
	for i := range openIssues {
		issue := &openIssues[i]
		if strings.Contains(issue.Key, element) { // issue.Key CLOUDSTACK-3509 element is just 3509
			found = true
			if result, err := utils.SplitIssue(ctx, jiraClient, issue, logger); err != nil {
				webexMessage = fmt.Sprintf("Failed to split issue %s. Error %v", issue.Key, err)
			} else if len(result) > 0 {
				webexMessage = "Created new issues:  \n"
				for i := range result {
					webexMessage += fmt.Sprintf("[%s](https://jira-eng-sjc10.cisco.com/jira/browse/%s)", result[i], result[i])
				}
			} else {
				webexMessage = fmt.Sprintf("Did not find any way to split issue [%s](https://jira-eng-sjc10.cisco.com/jira/browse/CLOUDSTACK-%s)",
					element, element)
			}
			break
		}
	}

	if !found {
		webexMessage = fmt.Sprintf("did not find any open issue matching the request %s", text)
	}

	if err := webex_utils.SendMessage(webexClient, roomID, webexMessage, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

func handleSummaryRequest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from string, logger logr.Logger) {
	logger.Info("Handling summary request")

	m := pdf.NewMaroto(consts.Portrait, consts.A4)
	var margin float64 = 10
	m.SetPageMargins(margin, margin, margin)

	vcsRun, err := utils.GetLastRun(ctx, true, logger)
	if err != nil {
		return
	}

	ucsRun, err := utils.GetLastRun(ctx, false, logger)
	if err != nil {
		return
	}

	m.RegisterHeader(func() {
		m.Row(10, func() {
			m.Col(20, func() {
				m.Text("Prepared for you by cloudstack e2e assistant.", props.Text{
					Top:   0,
					Style: consts.Bold,
					Align: consts.Center,
				})
				m.Text(fmt.Sprintf("Last UCS run: %d. Last VCS run: %d", ucsRun, vcsRun),
					props.Text{
						Top:   6,
						Style: consts.Bold,
						Align: consts.Center,
					})
			})
		})
	})

	m.RegisterFooter(func() {
		m.Row(10, func() {
			m.Col(12, func() {
				m.Text("For any feedback, please reach out to mgianluc@cisco.com", props.Text{
					Top:   13,
					Style: consts.BoldItalic,
					Size:  8,
					Align: consts.Center,
				})
			})
		})
	})

	_, height := m.GetPageSize()
	var currentHeight float64 = height

	// Get list of UCS tests. VCS tests are a subsets of UCS.
	testNames, err := utils.BuildUCSTests(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get tests. Err: %v", err))
		return
	}

	// For each test, get duration plot in both UCS and VCS (if not skipped)
	// Add those to document
	for i := range testNames {
		tmpTestFiles, _, err := getTestFiles(ctx, testNames[i], logger)
		if err != nil {
			continue
		}

		var testFile string

		// both VCS and UCS data available, create a grid
		if len(tmpTestFiles) == 2 {
			grids := make([]*gim.Grid, 0)
			for i := range tmpTestFiles {
				tmpGrid := gim.Grid{ImageFilePath: tmpTestFiles[i]}
				grids = append(grids, &tmpGrid)
			}
			rgba, err := gim.New(grids, 2, 1).Merge()
			if err != nil {
				logger.Info(fmt.Sprintf("Failed to create grid. Error %v", err))
				return
			}
			gridFileName := fmt.Sprintf("/tmp/result_grid_%s.png", testNames[i])
			file, err := os.Create(gridFileName)
			if err != nil {
				logger.Info(fmt.Sprintf("Failed to create grid file. Error %v", err))
				return
			}

			if err = png.Encode(file, rgba); err != nil {
				logger.Info(fmt.Sprintf("Failed to encode grid file. Error %v", err))
				return
			}

			testFile = gridFileName
		} else if len(tmpTestFiles) == 1 {
			testFile = tmpTestFiles[0]
		}

		if testFile != "" {
			m.Row(currentHeight+margin, func() {
				m.Col(0, func() {
					err = m.FileImage(testFile, props.Rect{
						Top:     margin,
						Left:    margin,
						Percent: 75,
					})
					if err != nil {
						logger.Info(fmt.Sprintf("Failed to add image %v", err))
					}
				})
			})
			currentHeight += height
		}
	}

	// Get all report plots and add to summary document
	files, err := getReportFiles(ctx, logger)
	if err != nil {
		return
	}
	for i := range files {
		m.Row(currentHeight, func() {
			m.Col(0, func() {
				err = m.FileImage(files[i], props.Rect{
					Top:     margin,
					Left:    margin,
					Percent: 75,
				})
				if err != nil {
					logger.Info(fmt.Sprintf("Failed to add image %v", err))
				}
			})
		})
		currentHeight += height
	}

	summaryFile := "/tmp/summary.pdf"
	err = m.OutputFileAndClose(summaryFile)
	if err != nil {
		os.Exit(1)
	}

	textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
		from, from)
	textMessage += "Please find attached a summary document."

	if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage,
		[]string{summaryFile}, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

// doesMatchTest returns true if message contain a test name along with test name
// retuns false otherwise
func doesMatchTest(ctx context.Context, webexClient *webexteams.Client,
	roomID, from, message string, logger logr.Logger) (string, bool, error) {
	testName, err := utils.BuildUCSTests(ctx, logger)
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

func sendMessageWithTestResult(ctx context.Context, webexClient *webexteams.Client,
	roomID, from, testName string, logger logr.Logger) {
	textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s|%s> thanks for your question.  \n",
		from, from)

	files, tmpMessage, err := getTestFiles(ctx, testName, logger)
	if err != nil {
		return
	}

	textMessage += tmpMessage

	if len(files) > 0 {
		grids := make([]*gim.Grid, 0)
		for i := range files {
			tmpGrid := gim.Grid{ImageFilePath: files[i]}
			grids = append(grids, &tmpGrid)
		}
		rgba, err := gim.New(grids, 2, 1).Merge()
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to create grid. Error %v", err))
			return
		}
		gridFileName := "/tmp/result_grid.png"
		file, err := os.Create(gridFileName)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to create grid file. Error %v", err))
			return
		}

		if err = png.Encode(file, rgba); err != nil {
			logger.Info(fmt.Sprintf("Failed to encode grid file. Error %v", err))
			return
		}

		if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage, []string{gridFileName}, logger); err != nil {
			logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
		}
		return
	}
	if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
}

func appendResultsToMessage(passedRuns, failedRuns, skippedRuns []int, testName, env string) string {
	textMessage := ""
	if len(passedRuns) > 0 {
		textMessage += fmt.Sprintf("Test **%s** passed in **%s** runs: ", testName, env)
		for i := range passedRuns {
			textMessage += fmt.Sprintf("[%d](%s/%d) ", passedRuns[i], vcsLink, passedRuns[i])
		}
		textMessage += "✅  \n"
	}
	if len(skippedRuns) > 0 {
		textMessage += fmt.Sprintf("Test **%s** was skipped in **%s** runs: ", testName, env)
		for i := range skippedRuns {
			textMessage += fmt.Sprintf("[%d](%s/%d) ", skippedRuns[i], vcsLink, skippedRuns[i])
		}
		textMessage += "⏸  \n"
	}
	if len(failedRuns) > 0 {
		textMessage += fmt.Sprintf("Test **%s** failed in **%s** runs: ", testName, env)
		for i := range failedRuns {
			textMessage += fmt.Sprintf("[%d](%s/%d) ", failedRuns[i], vcsLink, failedRuns[i])
		}
		textMessage += "❌  \n"
	}
	return textMessage
}

func initFlags(fs *pflag.FlagSet) {
	flag.DurationVar(&pollInterval,
		"poll-interval",
		defaultPollInterval,
		"The minimum interval at which watched resources are reconciled (e.g. 10m)",
	)
}

func createDurationPlot(environment, testName string, data []float64, logger logr.Logger) string {
	logger.Info(fmt.Sprintf("Generate duration plot for %s (env %s)", testName, environment))

	min := data[0]
	max := data[0]
	pts := make(plotter.XYs, len(data))
	for i := range data {
		pts[i].X = float64(i)
		pts[i].Y = data[i]
		if max < data[i] {
			max = data[i]
		}
		if min > data[i] {
			min = data[i]
		}
	}

	p := plot.New()

	p.Title.Text = testName
	p.X.Label.Text = "Runs (index is not used)"
	p.Y.Label.Text = "Time in minute"

	p.Y.Max = max + 5
	p.Y.Min = min - 5
	p.X.Max = float64(len(data) + 5)

	err := plotutil.AddLinePoints(p,
		fmt.Sprintf("Test duration (env %s)", environment), pts)

	mean, _ := stat.MeanStdDev(data, nil)

	meanPlot := plotter.NewFunction(func(x float64) float64 { return mean })
	meanPlot.Color = color.RGBA{B: 255, A: 255}
	p.Add(meanPlot)
	p.Legend.Add("Mean", meanPlot)

	if err != nil {
		panic(err)
	}

	fileName := fmt.Sprintf("/tmp/duration_%s_%s.png", environment, testName)
	// Save the plot to a PNG file.
	if err := p.Save(4*vg.Inch, 4*vg.Inch, fileName); err != nil {
		panic(err)
	}

	return fileName
}

func startCron() {
	<-gocron.Start()
}

func getReportFiles(ctx context.Context, logger logr.Logger) ([]string, error) {
	files := make([]string, 0)

	reportTypes, err := utils.BuildUCSReports(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get reports. Err: %v", err))
		return nil, err
	}

	for i := range reportTypes {
		var reportType, reportSubType string
		info := strings.Split(reportTypes[i], utils.ReportTypeSeparator)
		if len(info) > 1 {
			reportType = info[0]
			reportSubType = info[1]
		} else {
			reportType = reportTypes[i]
		}

		ucsReports, err := es_utils.GetReports(ctx, logger,
			"",            // no specific run
			reportType,    // for this specific report type
			reportSubType, // for this specific report subType
			"",            // no filter on name
			false,         // no vcs
			true,          // only ucs
			100)           // consider the last 100 runs. We have an average of 3 runs per week. Setting this higher.
		// Runs older than two weeks will be discarded later on.

		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get data for report %q. Error %v", reportTypes[i], err))
			return nil, err
		}

		var rtyp es_utils.Report
		data := make([]float64, 0)
		for _, item := range ucsReports.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Report)

			// Discard runs older than a week
			lastValidTime := time.Now().Add(-14 * 24 * time.Hour)
			if r.CreatedTime.After(lastValidTime) {
				data = append(data, r.DurationInMinutes)
			}
		}

		if len(data) > 0 {
			ucsPlot := createDurationPlot("ucs", reportTypes[i], data, logger)
			files = append(files, ucsPlot)
		}
	}

	return files, nil
}

// getTestFiles for a given test collects the results in the last 30 runs.
// Returns location of files with duration plot and a string containing list of runs where it passed/failed/skipped.
func getTestFiles(ctx context.Context, testName string, logger logr.Logger) ([]string, string, error) {
	vcsResults, err := es_utils.GetResults(ctx, logger, "", testName, true, false, false, false, false, 30)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testName, err))
		return nil, "", err
	}
	ucsResults, err := es_utils.GetResults(ctx, logger, "", testName, false, true, false, false, false, 30)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testName, err))
		return nil, "", err
	}

	var textMessage string

	passedRuns := make([]int, 0)
	failedRuns := make([]int, 0)
	skippedRuns := make([]int, 0)
	var rtyp es_utils.Result
	vcsData := make([]float64, 0)
	for _, item := range vcsResults.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		if r.Result == "passed" {
			passedRuns = append(passedRuns, r.Run)
			vcsData = append(vcsData, r.DurationInMinutes)
		} else if r.Result == "failed" {
			failedRuns = append(failedRuns, r.Run)
		} else if r.Result == "skipped" {
			skippedRuns = append(skippedRuns, r.Run)
		}
	}
	textMessage += appendResultsToMessage(passedRuns, failedRuns, skippedRuns, testName, "vcs")

	passedRuns = make([]int, 0)
	failedRuns = make([]int, 0)
	skippedRuns = make([]int, 0)
	ucsData := make([]float64, 0)
	for _, item := range ucsResults.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		if r.Result == "passed" {
			passedRuns = append(passedRuns, r.Run)
			ucsData = append(ucsData, r.DurationInMinutes)
		} else if r.Result == "failed" {
			failedRuns = append(failedRuns, r.Run)
		} else if r.Result == "skipped" {
			skippedRuns = append(skippedRuns, r.Run)
		}
	}
	textMessage += appendResultsToMessage(passedRuns, failedRuns, skippedRuns, testName, "ucs")

	files := make([]string, 0)

	if len(ucsData) > 0 {
		ucsPlot := createDurationPlot("ucs", testName, ucsData, logger)
		files = append(files, ucsPlot)
	}
	if len(vcsData) > 0 {
		vcsPlot := createDurationPlot("vcs", testName, vcsData, logger)
		files = append(files, vcsPlot)
	}

	return files, textMessage, nil
}

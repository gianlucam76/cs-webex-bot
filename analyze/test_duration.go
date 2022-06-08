package analyze

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"reflect"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-logr/logr"
	"github.com/gonum/stat"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"
	"github.com/olivere/elastic/v7"
	gim "github.com/ozankasikci/go-image-merge"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	"github.com/gianlucam76/webex_bot/utils"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

// Minimum number of successfull runs needed.
const numberOfSuccessfulRuns int = 7

// Minimum duration to run this metric (in minutes)
const minDurationInMinutes float64 = 5

// When relative standard deviation is higher than this value
// an action is taken
const rsdThreshold float64 = 10

// CreatePieCharts creates pie chart with test duration for last available VCS
// and UCS run
func CreatePieCharts(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	_ = gocron.Every(1).Friday().At("9:30:00").Do(sendDurationPieChart,
		ctx, webexClient, roomID, logger)
}

func sendDurationPieChart(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	if shouldSendPieChart(ctx, true, logger) {
		// create pie chart for vcs
		if fileName, err := CreateDurationPieChart(ctx, true, roomID, logger); err == nil {
			textMessage := "please open the attached file to see test duration pie chart from last VCS run"
			if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage, []string{fileName}, logger); err != nil {
				logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
			}
		}
	}

	if shouldSendPieChart(ctx, false, logger) {
		// create pie chart for ucs
		if fileName, err := CreateDurationPieChart(ctx, false, roomID, logger); err == nil {
			textMessage := "please open the attached file to see test duration pie chart from last UCS run"
			if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage, []string{fileName}, logger); err != nil {
				logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
			}
		}
	}
}

// CheckTestDurationOnUCS for each test in UCS:
// - consider the runs in the last week;
// - if the mean value is higher than minDurationInMinutes and
// - if relative standard deviation is higher than rsdThreshold
// send a message on the webex channel
func CheckTestDurationOnUCS(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	_ = gocron.Every(1).Monday().At("11:30:00").Do(evaluateUCSTest,
		ctx, webexClient, roomID, logger)
}

func evaluateUCSTest(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	maintainers := make(map[string]bool)
	testFiles := make([]string, 0)

	testNames, err := utils.BuildUCSTests(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get tests. Err: %v", err))
		return
	}

	for i := range testNames {
		data, maintainer, err := getTestData(ctx, testNames[i], logger)
		if err != nil {
			continue
		}

		if len(data) < numberOfSuccessfulRuns {
			continue
		}

		mean, std := stat.MeanStdDev(data, nil)
		rsd := std * 100 / mean
		logger.Info(fmt.Sprintf("Test: %s Mean: %f Standard Deviation: %f Relative Standard Deviation: %f",
			testNames[i], mean, std, rsd))

		if mean >= minDurationInMinutes && rsd >= rsdThreshold {
			file := createDurationPlot(&testNames[i], data, mean, logger)
			testFiles = append(testFiles, file)
			maintainers[maintainer] = true
		}
	}

	if len(testFiles) > 0 {
		sendAlertForTest(webexClient, roomID, testFiles, maintainers, logger)
	}
}

// getLastRunResults returns last run results
func getLastRunResults(ctx context.Context, vcs bool,
	logger logr.Logger) (*elastic.SearchResult, error) {
	env := "ucs"
	if vcs {
		env = "vcs"
	}
	lastRun, err := utils.GetLastRun(ctx, vcs, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get last run ID. Err: %v", err))
		return nil, err
	}

	var results *elastic.SearchResult
	if lastRun != 0 {
		results, err = es_utils.GetResults(ctx, logger,
			fmt.Sprintf("%d", lastRun), // from this run
			"",                         // no specific test
			vcs,                        // from vcs
			!vcs,                       // no ucs
			false,                      // no passed
			false,                      // get failed tests
			false,                      // no skipped
			200,
		)
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get failed test in vcs %s %d from elastic DB. Err: %v", env, lastRun, err))
			return nil, err
		}
		return results, nil
	}

	return nil, nil
}

// CreateDurationPieChart takes into consideration last available run.
// Generates a pie chart considering test duration time.
// Only tests that account for at least one percent of the total time
// will be displayed
func CreateDurationPieChart(ctx context.Context, vcs bool,
	roomID string, logger logr.Logger) (string, error) {
	env := "ucs"
	if vcs {
		env = "vcs"
	}
	items := make([]opts.PieData, 0)

	results, err := getLastRunResults(ctx, vcs, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get failed test in %s from elastic DB. Err: %v", env, err))
		return "", err
	}

	// TODO: we stopped storing setup_wait_for_e2e result in es db.
	// This can be removed in few days
	exclude_test := "setup_wait_for_e2e"

	var totalTime float64 = 0
	var rtyp es_utils.Result
	for _, item := range results.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		if r.Name == exclude_test {
			continue
		}
		totalTime += r.DurationInMinutes
	}

	// Total time all tests that individually accounted for less than one percent of the total time.
	var discardedTotalTime float64 = 0
	for _, item := range results.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)

		if r.Name == exclude_test {
			continue
		}

		if 100*r.DurationInMinutes/totalTime < 1.0 {
			discardedTotalTime += r.DurationInMinutes
			continue
		}

		name := r.Name
		if r.Serial {
			name = fmt.Sprintf("%s*", r.Name)
		}
		items = append(items, opts.PieData{
			Name:  name,
			Value: fmt.Sprintf("%.2f", r.DurationInMinutes),
		})
	}

	if discardedTotalTime > 0 {
		items = append(items, opts.PieData{
			Name:  "all others",
			Value: discardedTotalTime,
		})
	}

	if len(items) > 0 {
		pie := charts.NewPie()
		pie.SetGlobalOptions(
			charts.WithTitleOpts(
				opts.Title{
					Title:    "Test duration (time is in minutes)",
					Subtitle: "Only tests which account for at least more than one percent of total time are displayed\nTests marked with '*' ran in serial",
				},
			),
		)
		pie.SetSeriesOptions()
		pie.AddSeries("Test duration", items).
			SetSeriesOptions(
				charts.WithPieChartOpts(
					opts.PieChart{
						Radius: 100,
					},
				),
				charts.WithLabelOpts(
					opts.Label{
						Show:      true,
						Formatter: "{b}: {c}",
					},
				),
			)
		filname := "/tmp/pie_chart_duration.html"
		f, _ := os.Create(filname)
		_ = pie.Render(f)

		return filname, nil
	}
	logger.Info("no data to generate pie chart")
	return "", fmt.Errorf("no data to generate pie chart")
}

// shouldSendPieChart gets latest run and if there are at least more than 20
// tests in that run return yes.
// Return false if last run contains less than 20 tests
func shouldSendPieChart(ctx context.Context, vcs bool,
	logger logr.Logger) bool {
	env := "ucs"
	if vcs {
		env = "vcs"
	}

	results, err := getLastRunResults(ctx, vcs, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get failed test in %s from elastic DB. Err: %v", env, err))
		return false
	}

	if results.TotalHits() > 20 {
		return true
	}

	return false
}

// getTestData consider the last 30 UCS runs and for a given test returns:
// - durations in a form of slice
// - maintainer
func getTestData(ctx context.Context, testName string, logger logr.Logger) (data []float64, maintainer string, err error) {
	var ucsResults *elastic.SearchResult
	ucsResults, err = es_utils.GetResults(ctx, logger,
		"",       // no specific run
		testName, // for this specific test
		false,    // no vcs
		true,     // only ucs
		true,     // only results where test passed
		false,    // no failed
		false,    // no skipped
		100)      // consider the last 100 runs. We have an average of 3 runs per week. Setting this higher.
	// Runs older than two weeks will be discarded later on.
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testName, err))
		return
	}

	var rtyp es_utils.Result
	data = make([]float64, 0)
	for _, item := range ucsResults.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)

		// Discard runs older than two weeks
		lastValidTime := time.Now().Add(-14 * 24 * time.Hour)
		if r.StartTime.After(lastValidTime) {
			maintainer = r.Maintainer
			data = append(data, r.DurationInMinutes)
		}
	}

	return
}

// sendAlertForTest generates a plot with test duration and send a webex message with such graph.
func sendAlertForTest(webexClient *webexteams.Client, roomID string,
	testFiles []string, maintainers map[string]bool,
	logger logr.Logger) {

	textMessage := "I detected something which I believe needs to be looked at. Tagging all maintainers:  \n"
	for m := range maintainers {
		textMessage += fmt.Sprintf("1. <@personEmail:%s@cisco.com|%s>  \n", m, m)
	}
	textMessage += "  \nFor the tests in the plot the relative standard deviation is too big.  \n"

	x, y := getGridSize(len(testFiles))

	grids := make([]*gim.Grid, 0)
	for i := range testFiles {
		tmpGrid := gim.Grid{ImageFilePath: testFiles[i]}
		grids = append(grids, &tmpGrid)
	}
	rgba, err := gim.New(grids, x, y).Merge()
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to create grid. Error %v", err))
		return
	}

	gridFileName := "/tmp/test_analysis_grid.png"
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
}

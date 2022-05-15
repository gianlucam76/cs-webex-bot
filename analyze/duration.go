package analyze

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"reflect"
	"sort"

	"github.com/andygrunwald/go-jira"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-logr/logr"
	"github.com/gonum/stat"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"
	"github.com/olivere/elastic/v7"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	"github.com/gianlucam76/webex_bot/utils"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

// Minimum number of successfull runs needed.
const numberOfSuccessfulRuns int = 10

// Minimum duration to run this metric (in minutes)
const minDurationInMinutes float64 = 10

// When relative standard deviation is higher than this value
// an action is taken
const rsdThreshold float64 = 10

// CreatePieCharts
func CreatePieCharts(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	_ = gocron.Every(1).Friday().At("9:30:00").Do(sendDurationPieChart,
		ctx, webexClient, roomID, jiraClient, logger)
}

func sendDurationPieChart(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
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
// - consider the last numberOfRuns runs;
// - if the mean value is higher than minDurationInMinutes and
// - if relative standard deviation is higher than rsdThreshold
// send a message on the webex channel
func CheckTestDurationOnUCS(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	_ = gocron.Every(1).Monday().At("11:30:00").Do(evaluateUCSTest,
		ctx, webexClient, roomID, jiraClient, logger)
}

func evaluateUCSTest(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	const maxMessage = 2

	alreadySent := 0

	testNames, err := utils.BuildUCSTests(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get tests. Err: %v", err))
		return
	}

	for i := range testNames {
		ucsResults, err := es_utils.GetResults(ctx, logger,
			"",                     // no specific run
			testNames[i],           // for this specific test
			false,                  // no vcs
			true,                   // only ucs
			true,                   // only results where test passed
			false,                  // no failed
			false,                  // no skipped
			numberOfSuccessfulRuns) // last numberOfRuns runs
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testNames[i], err))
			return
		}

		var mantainer string
		var rtyp es_utils.Result
		data := make([]float64, 0)
		runIds := make([]int, 0)
		for _, item := range ucsResults.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Result)
			mantainer = r.Maintainer
			data = append(data, r.DurationInMinutes)
			runIds = append(runIds, r.Run)
		}

		if len(data) < numberOfSuccessfulRuns {
			continue
		}

		mean, std := stat.MeanStdDev(data, nil)
		rsd := std * 100 / mean
		logger.Info(fmt.Sprintf("Test: %s Mean: %f Standard Deviation: %f Relative Standard Deviation: %f",
			testNames[i], mean, std, rsd))

		if mean >= minDurationInMinutes && rsd >= rsdThreshold {
			alreadySent++
			min, max := minMax(data)
			textMessage := fmt.Sprintf("Hello 🤚 <@personEmail:%s@cisco.com|%s>  \nI detected something which I believe needs to be looked at.  \n",
				mantainer, mantainer)
			textMessage += fmt.Sprintf("For this test: %s The relative standard deviation: %f is higher than threshold: %f.  \n",
				testNames[i], rsd, rsdThreshold)
			textMessage += fmt.Sprintf("Min duration %f min. Max duration %f min", min, max)
			textMessage += fmt.Sprintf("Mean value: %f. UCS samples: %d  \n", mean, numberOfSuccessfulRuns)
			fileName := createDurationPlot(&testNames[i], data, runIds, mean, logger)
			if err := webex_utils.SendMessageWithGraphs(webexClient, roomID, textMessage, []string{fileName}, logger); err != nil {
				logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
			}
			if alreadySent >= maxMessage {
				break
			}
		}
	}
}

func minMax(data []float64) (min, max float64) {
	min = data[0]
	max = data[0]
	for _, value := range data {
		if max < value {
			max = value
		}
		if min > value {
			min = value
		}
	}
	return
}

func createDurationPlot(testName *string, data []float64, runIds []int, mean float64, logger logr.Logger) string {
	logger.Info(fmt.Sprintf("Generate duration plot for test %s", *testName))

	// Create a map: <run id> : <duration>
	infoMap := make(map[int]float64)
	for i := range runIds {
		infoMap[runIds[i]] = data[i]
	}

	// Create temporary slice with run id values.
	// This slice will then be sorted
	tmp := make([]int, 0)
	for k := range runIds {
		tmp = append(tmp, runIds[k])
	}
	sort.Ints(tmp)

	pts := make(plotter.XYs, len(data))
	for i := range tmp {
		pts[i].X = float64(tmp[i])
		pts[i].Y = infoMap[tmp[i]]
	}

	p := plot.New()

	p.Title.Text = *testName
	p.X.Label.Text = "Run ID"
	p.Y.Label.Text = "Time in minute"

	min, max := minMax(data)
	p.Y.Max = max + 5
	p.Y.Min = min - 5
	p.X.Max = float64(tmp[len(tmp)-1] + 5)

	err := plotutil.AddLinePoints(p,
		"Duration", pts)

	meanPlot := plotter.NewFunction(func(x float64) float64 { return mean })
	meanPlot.Color = color.RGBA{B: 255, A: 255}
	p.Add(meanPlot)
	p.Legend.Add("Mean", meanPlot)

	if err != nil {
		panic(err)
	}

	fileName := "/tmp/duration.png"
	// Save the plot to a PNG file.
	if err := p.Save(4*vg.Inch, 4*vg.Inch, fileName); err != nil {
		panic(err)
	}

	return fileName
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

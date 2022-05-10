package analyze

import (
	"context"
	"fmt"
	"image/color"
	"reflect"
	"sort"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"
	"github.com/gonum/stat"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"
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

// CheckTestDurationOnUCS for each test in UCS:
// - consider the last numberOfRuns runs;
// - if the mean value is higher than minDurationInMinutes and
// - if relative standard deviation is higher than rsdThreshold
// send a message on the webex channel
func CheckTestDurationOnUCS(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	_ = gocron.Every(1).Monday().At("09:00:00").Do(evaluateUCSTest,
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

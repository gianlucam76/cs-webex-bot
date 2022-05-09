package analyze

import (
	"container/heap"
	"context"
	"fmt"
	"reflect"

	"github.com/andygrunwald/go-jira"
	"github.com/go-logr/logr"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	"github.com/gianlucam76/webex_bot/utils"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

// Considering an average of 3 runs per day. We want to run this weekly so 21 runs.
const numberOfRuns int = 21

type kv struct {
	Key   string
	Value int
}

type KVHeap []kv

// WeeklyStats sends a summary of UCS and VCS weekly result
func WeeklyStats(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	_ = gocron.Every(1).Friday().At("09:00:00").Do(sendStats,
		ctx, webexClient, roomID, jiraClient, logger)
}

// sendStats collects all runs in the last "run".
// Report tests the top 5 test which failed the most, if any.
func sendStats(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client,
	logger logr.Logger) {
	// Consider UCS runs
	sendStatsPerEnvironment(ctx, webexClient, roomID, jiraClient, true, logger)
	// Consider VCS runs
	sendStatsPerEnvironment(ctx, webexClient, roomID, jiraClient, false, logger)
}

// sendWeeklyStats collects all runs in the last 7 days.
// Report tests the top 5 test which failed the most, if any.
func sendStatsPerEnvironment(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	jiraClient *jira.Client, ucs bool,
	logger logr.Logger) {
	var err error
	var testNames []string
	var env string
	if ucs {
		env = "ucs"
		testNames, err = utils.BuildUCSTests(ctx, logger)
	} else {
		env = "vcs"
		testNames, err = utils.BuildVCSTests(ctx, logger)
	}
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get tests. Err: %v", err))
		return
	}

	// failedTestMap contains, per test number of times test failed
	failedTestMap := make(map[string]int)
	// failedTestMap contains, per test number of times test passed
	passedTestMap := make(map[string]int)

	foundFailed := false

	for i := range testNames {
		ucsResults, err := es_utils.GetResults(ctx, logger,
			"",           // no specific run
			testNames[i], // for this specific test
			!ucs,         // vcs
			ucs,          // ucs
			false,        // no filter on passed test
			false,        // no filter on failed test
			false,        // no filter on skipped test
			numberOfRuns) // last numberOfRuns runs
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get results for test %q. Error %v", testNames[i], err))
			return
		}

		failedTimes := 0
		passedTimes := 0
		var rtyp es_utils.Result
		for _, item := range ucsResults.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Result)
			if r.Result == "failed" {
				failedTimes++
				foundFailed = true
			} else if r.Result == "passed" {
				passedTimes++
			}
		}

		failedTestMap[testNames[i]] = failedTimes
		passedTestMap[testNames[i]] = passedTimes
	}

	var textMessage string
	if foundFailed {
		textMessage = "Hello 🤚 This is a weekly report for top failed tests in UCS runs.  \n"
		h := getHeap(failedTestMap)
		for i := 1; i <= 3; i++ {
			test := heap.Pop(h).(kv)
			if test.Value == 0 {
				break
			}
			passed := passedTestMap[test.Key]
			textMessage += fmt.Sprintf("test: %s failed %d times.  (passed %d times)", test.Key, test.Value, passed)
		}
	} else {
		textMessage = fmt.Sprintf("Hello 🤚 Amazing work team. No test has failed in the last %d %s runs 🙌👏  \n",
			numberOfRuns, env)
	}

	if err := webex_utils.SendMessage(webexClient, roomID, textMessage, logger); err != nil {
		logger.Info(fmt.Sprintf("Failed to send message. Err: %v", err))
	}
	logger.Info(textMessage)
}

func getHeap(m map[string]int) *KVHeap {
	h := &KVHeap{}
	heap.Init(h)
	for k, v := range m {
		heap.Push(h, kv{k, v})
	}
	return h
}

// Note that "Less" is greater-than here so we can pop *larger* items.
func (h KVHeap) Less(i, j int) bool { return h[i].Value > h[j].Value }
func (h KVHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h KVHeap) Len() int           { return len(h) }

func (h *KVHeap) Push(x interface{}) {
	*h = append(*h, x.(kv))
}

func (h *KVHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

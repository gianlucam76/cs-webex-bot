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
	_ = gocron.Every(1).Friday().At("09:00:00").Do(SendStats,
		ctx, webexClient, roomID, jiraClient, logger)
}

// sendStats collects all runs in the last "run".
// Report tests the top 5 test which failed the most, if any.
func SendStats(ctx context.Context,
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
	var env string

	// failedTestMap contains, per test number of times test failed
	failedTestMap := make(map[string]int)
	// failedTestMap contains, per test number of times test passed
	passedTestMap := make(map[string]int)

	collectStats(ctx, ucs, failedTestMap, passedTestMap, numberOfRuns, logger)
	if ucs {
		env = "ucs"
	} else {
		env = "vcs"
	}

	var textMessage string
	if len(failedTestMap) != 0 {
		textMessage = fmt.Sprintf("Hello 🤚 This is a weekly report for top failed tests in the last %d %s runs  \n",
			numberOfRuns, env)
		h := getHeap(failedTestMap)
		for i := 1; i <= 3; i++ {
			test := heap.Pop(h).(kv)
			if test.Value == 0 {
				break
			}
			passed := passedTestMap[test.Key]
			textMessage += fmt.Sprintf("1. test: %s failed **%d** times.  (passed %d times)  \n",
				test.Key, test.Value, passed)
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

// collectStats get latest numberOfRuns for either ucs or vcs.
// For each available runs:
// - get all tests.
// - walk all tests and update failedTestMap for each failed test
// and passedTestMap for each test that passed.
func collectStats(ctx context.Context, ucs bool,
	failedTestMap, passedTestMap map[string]int,
	numberOfRuns int,
	logger logr.Logger) {
	env := "vcs"
	if ucs {
		env = "ucs"
	}

	// Get ES client
	esClient, err := es_utils.GetClient()
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get es client. Err: %v", err))
		return
	}

	// Get last numberOfRuns for this env (ucs vs vcs)
	b, err := es_utils.GetAvailableRuns(ctx, esClient, env, numberOfRuns, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get available UCS runs. Err: %v", err))
		return
	}

	// For each available run, get all results
	for _, bucket := range b.Buckets {
		logger.Info(fmt.Sprintf("Run %s", bucket.KeyNumber.String()))
		results, err := es_utils.GetResults(ctx, logger,
			bucket.KeyNumber.String(), // filter on run
			"",                        // no filter on test name
			!ucs,                      // filter on vcs
			ucs,                       // filter ucs runs
			false,                     // no filter on passed tests
			false,                     // no filter on failed tests
			false,                     // no filter on skipped tests
			200)                       // limit on results
		if err != nil {
			logger.Info(fmt.Sprintf("Failed to get results for run %s Err: %v",
				bucket.KeyNumber.String(), err))
			continue
		}

		var rtyp es_utils.Result
		for _, item := range results.Each(reflect.TypeOf(rtyp)) {
			r := item.(es_utils.Result)
			if r.Result == "failed" {
				failedTestMap[r.Name]++
			} else if r.Result == "passed" {
				passedTestMap[r.Name]++
			}
		}
	}
}

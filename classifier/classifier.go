package classifier

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/n3integration/classifier/naive"

	"github.com/gianlucam76/cs-e2e-result/es_utils"
)

const (
	OpenIssues   = "open-issues"
	LastVCSRun   = "last-vcs-run"
	LastUCSRun   = "last-ucs-run"
	SpecificTest = "specific-test"
)

func GetClassifier() *naive.Classifier {
	return naive.New()
}

func Train(ctx context.Context, classifier *naive.Classifier, logger logr.Logger) {
	logger.Info("Train for open issue questions")
	trainOpenIssues(classifier)

	logger.Info("Train for VCS questions")
	trainVcs(classifier)

	logger.Info("Train for UCS questions")
	trainUcs(classifier)

	logger.Info("Train for test result questions")
	trainTests(ctx, classifier, logger)
}

func trainOpenIssues(classifier *naive.Classifier) {
	_ = classifier.TrainString("show me open issues", OpenIssues)
	_ = classifier.TrainString("jira", OpenIssues)
	_ = classifier.TrainString("currently open", OpenIssues)
	_ = classifier.TrainString("show jira issues", OpenIssues)
	_ = classifier.TrainString("please show me jira issues", OpenIssues)
	_ = classifier.TrainString("tell me open issues", OpenIssues)
	_ = classifier.TrainString("bugs", OpenIssues)
	_ = classifier.TrainString("list e2e bugs", OpenIssues)
}

func trainVcs(classifier *naive.Classifier) {
	_ = classifier.TrainString("vcs failed tests", LastVCSRun)
	_ = classifier.TrainString("which test failed in last vcs run", LastVCSRun)
	_ = classifier.TrainString("test failed in vcs", LastVCSRun)
	_ = classifier.TrainString("has any test failed in vcs", LastVCSRun)
	_ = classifier.TrainString("all good in vcs", LastVCSRun)
	_ = classifier.TrainString("vcs failed tests", LastVCSRun)
	_ = classifier.TrainString("list failed test in vcs", LastVCSRun)
	_ = classifier.TrainString("vcs failed test", LastVCSRun)
}

func trainUcs(classifier *naive.Classifier) {
	_ = classifier.TrainString("ucs failed tests", LastUCSRun)
	_ = classifier.TrainString("which test failed in last ucs run", LastUCSRun)
	_ = classifier.TrainString("test failed in ucs", LastUCSRun)
	_ = classifier.TrainString("has any test failed in ucs", LastUCSRun)
	_ = classifier.TrainString("all good in ucs", LastUCSRun)
	_ = classifier.TrainString("ucs failed tests", LastUCSRun)
	_ = classifier.TrainString("list failed test in ucs", LastUCSRun)
	_ = classifier.TrainString("ucs failed test", LastUCSRun)
}

func trainTests(ctx context.Context, classifier *naive.Classifier, logger logr.Logger) {
	trainWithVCSTests(ctx, classifier, true, false, logger)
	trainWithVCSTests(ctx, classifier, false, true, logger)
}

func trainWithVCSTests(ctx context.Context, classifier *naive.Classifier,
	vcs, ucs bool,
	logger logr.Logger) {
	c, err := es_utils.GetClient()
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get elastic client. Err %v", err))
		return
	}

	b, err := es_utils.GetAvailableRuns(ctx, c, "vcs", 10, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get available vcs runs from elastic DB. Err: %v", err))
		return
	}

	var lastRun int64
	for _, bucket := range b.Buckets {
		if rID, err := bucket.KeyNumber.Int64(); err == nil {
			if lastRun == 0 || lastRun < rID {
				lastRun = rID
			}
		}
	}

	results, err := es_utils.GetResults(ctx, logger,
		fmt.Sprintf("%d", lastRun), // from this run
		"",                         // no specific test
		vcs,                        // from vcs
		ucs,                        // from ucs
		true,                       // get passed tests
		true,                       // get failed tests
		true,                       // get skipped tests
		200,
	)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get failed test in vcs run %d from elastic DB. Err: %v", lastRun, err))
		return
	}

	var rtyp es_utils.Result
	for _, item := range results.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Result)
		_ = classifier.TrainString(r.Name, SpecificTest)
		_ = classifier.TrainString(r.Description, SpecificTest)
		_ = classifier.TrainString(r.Maintainer, SpecificTest)
	}
}

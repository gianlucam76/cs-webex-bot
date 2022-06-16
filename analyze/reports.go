package analyze

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/gonum/stat"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"
	gim "github.com/ozankasikci/go-image-merge"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	"github.com/gianlucam76/webex_bot/utils"
	"github.com/gianlucam76/webex_bot/webex_utils"
)

// Minimum number of runs with reports needed.
const numberOfAvailableRuns int = 7

// CheckReportDurationOnUCS for each reports in UCS:
// - consider the runs in the two last week;
// - if relative standard deviation is higher than rsdThreshold
// send a message on the webex channel
func CheckReportDurationOnUCS(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	_ = gocron.Every(1).Monday().At("12:00:00").Do(evaluateUCSReports,
		ctx, webexClient, roomID, logger)
}

// evaluateUCSReports:
// - gets list of available reports
// - groups reports for type (and subType if avaialble)
// - evaluate mean and mean standard deviation
// - sends webex message when rsd is too large
func evaluateUCSReports(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	reportTypes, err := utils.BuildUCSReports(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get reports. Err: %v", err))
		return
	}

	reportFiles := make([]string, 0)

	files := analyzeByGroupingForTypeAndSubtype(ctx, reportTypes, logger)
	reportFiles = append(reportFiles, files...)
	files = analyzeByGroupingForTypeSubtypeAndName(ctx, reportTypes, logger)
	reportFiles = append(reportFiles, files...)

	if len(reportFiles) > 0 {
		textMessage := "Hello I detected something which I believe needs to be looked at.  \n"
		textMessage += "For the reports in the plot the relative standard deviation is too big.  \n"
		sendAlertForReport(webexClient, roomID, textMessage, reportFiles, logger)
	}
}

// analyzeByGroupingForTypeAndSubtype groups reports by type and subType if available (name is ignored).
// Collects durations and if there is too much variance, sends an alert to the webex space.
func analyzeByGroupingForTypeAndSubtype(ctx context.Context, reportTypes []string, logger logr.Logger) []string {
	reportFiles := make([]string, 0)

	for i := range reportTypes {
		// Get reports for a given type/subtype
		data, err := getReportData(ctx, reportTypes[i], logger)
		if err != nil {
			continue
		}

		if len(data) < numberOfAvailableRuns {
			continue
		}

		durations := getDurations(data)

		mean, std := stat.MeanStdDev(durations, nil)

		if mean <= 1 {
			logger.Info(fmt.Sprintf("Report: %s Mean: %f is too low. Skip analyzing rsd", reportTypes[i], mean))
			continue
		}

		rsd := std * 100 / mean
		logger.Info(fmt.Sprintf("Report: %s Mean: %f Standard Deviation: %f Relative Standard Deviation: %f",
			reportTypes[i], mean, std, rsd))

		if rsd >= rsdThreshold {
			// Results are returned with last one first.
			// Reverse the order while creating a plot
			utils.Reverse(durations)
			reportName := reportTypes[i]
			environment := "ucs"
			fileName := CreateDurationPlot(&environment, &reportName, durations, mean, logger)
			reportFiles = append(reportFiles, fileName)
		}
	}

	return reportFiles
}

// analyzeByGroupingForTypeSubtypeAndName groups reports by type and subType if available.
// If it detects name as constant accross multiple runs, uses the name to group as well.
// Collects durations and if there is too much variance, sends an alert to the webex space.
func analyzeByGroupingForTypeSubtypeAndName(ctx context.Context, reportTypes []string, logger logr.Logger) []string {
	reportFiles := make([]string, 0)

	for i := range reportTypes {
		// Get reports for a given type/subtype
		data, err := getReportData(ctx, reportTypes[i], logger)
		if err != nil {
			continue
		}

		if len(data) < numberOfAvailableRuns {
			continue
		}

		// Try to group by name. So walk all reports (so far grouped by type and subType)
		// and build a new map where report name is the key
		reportByName := make(map[string][]es_utils.Report)
		for j := range data {
			if reportByName[data[j].Name] == nil {
				reportByName[data[j].Name] = make([]es_utils.Report, 0)
			}
			reportByName[data[j].Name] = append(reportByName[data[j].Name], data[j])
		}

		if len(reportByName) == len(data) { // Name appears to be random so ignore it
			logger.Info(fmt.Sprintf("Report %s name appears to be random. Skip per name analysis", reportTypes[i]))
			continue
		}

		logger.Info(fmt.Sprintf("Report %s name appears to be NOT random", reportTypes[i]))
		files := analyzePerNameReports(reportTypes[i], reportByName, logger)
		reportFiles = append(reportFiles, files...)
	}

	return reportFiles
}

func analyzePerNameReports(reportInfo string, reportByName map[string][]es_utils.Report,
	logger logr.Logger) []string {
	reportFiles := make([]string, 0)

	for name := range reportByName {
		perNameReports := reportByName[name]

		if len(perNameReports) < numberOfAvailableRuns/2.0 {
			logger.Info(fmt.Sprintf("Report %s name %s not enough data (%d) for an analysis",
				reportInfo, name, len(perNameReports)))
			continue
		}

		durations := make([]float64, 0)
		for j := range perNameReports {
			durations = append(durations, perNameReports[j].DurationInMinutes)
		}

		mean, std := stat.MeanStdDev(durations, nil)
		rsd := std * 100 / mean
		logger.Info(fmt.Sprintf("Report: %s:%s Mean: %f Standard Deviation: %f Relative Standard Deviation: %f",
			reportInfo, name, mean, std, rsd))

		if rsd >= rsdThreshold {
			// Results are returned with last one first.
			// Reverse the order while creating a plot
			utils.Reverse(durations)
			environment := "ucs"
			reportByName := fmt.Sprintf("%s_%s", reportInfo, name)
			fileName := CreateDurationPlot(&environment, &reportByName, durations, mean, logger)
			reportFiles = append(reportFiles, fileName)
		}
	}

	return reportFiles
}

func getDurations(data []es_utils.Report) []float64 {
	durations := make([]float64, 0)
	for j := range data {
		durations = append(durations, data[j].DurationInMinutes)
	}

	return durations
}

// getReportData for a given report (type and subtype if available) considers the last 30 runs,
// filtering out any run older than two week.
// Returns a slice with all durations.
func getReportData(ctx context.Context, reportInfo string, logger logr.Logger) ([]es_utils.Report, error) {
	var reportType, reportSubType string
	info := strings.Split(reportInfo, utils.ReportTypeSeparator)
	if len(info) > 1 {
		reportType = info[0]
		reportSubType = info[1]
	} else {
		reportType = reportInfo
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
		logger.Info(fmt.Sprintf("Failed to get data for report %q. Error %v", reportInfo, err))
		return nil, err
	}

	var rtyp es_utils.Report
	data := make([]es_utils.Report, 0)
	for _, item := range ucsReports.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.Report)

		// Discard runs older than two week
		lastValidTime := time.Now().Add(-14 * 24 * time.Hour)
		if r.CreatedTime.After(lastValidTime) {
			data = append(data, r)
		}
	}

	return data, nil
}

// sendAlertForReport generates a plot with duration and send a webex message with such graph.
func sendAlertForReport(webexClient *webexteams.Client, roomID, textMessage string,
	reportFiles []string, logger logr.Logger) {
	x, y := GetGridSize(len(reportFiles))

	grids := make([]*gim.Grid, 0)
	for i := range reportFiles {
		tmpGrid := gim.Grid{ImageFilePath: reportFiles[i]}
		grids = append(grids, &tmpGrid)
	}
	rgba, err := gim.New(grids, x, y).Merge()
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to create grid. Error %v", err))
		return
	}

	gridFileName := "/tmp/report_analysis_grid.png"
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

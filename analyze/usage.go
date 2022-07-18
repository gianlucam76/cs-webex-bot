package analyze

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/gonum/stat"
	"github.com/jasonlvhit/gocron"
	webexteams "github.com/jbogarin/go-cisco-webex-teams/sdk"

	es_utils "github.com/gianlucam76/cs-e2e-result/es_utils"
	"github.com/gianlucam76/webex_bot/utils"
)

// If a pod is consuming more memory that this threshold and there is no limit
// defined send a warning.
const maxMemory = 500000

// When relative standard deviation is higher than this value
// an action is taken
const memoryRsdThreshold float64 = 40

// When relative standard deviation is higher than this value
// an action is taken
const cpuRsdThreshold float64 = 40

// CheckReportUsageOnUCS for each reports in UCS:
// - consider the runs in the last week;
// - if relative standard deviation is higher than rsdThreshold
// send a message on the webex channel
// - if memory limit is defined, and memory usage is close to limit,
// send a message on the webex channel
func CheckReportUsageOnUCS(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	_ = gocron.Every(1).Tuesday().At("11:00:00").Do(evaluateUCSUsageReports,
		ctx, webexClient, roomID, logger)
}

// evaluateUCSUsageReports:
// - gets list of available usage reports
// - groups reports pod
// - evaluate mean and mean standard deviation
// - sends webex message when rsd is too large
func evaluateUCSUsageReports(ctx context.Context,
	webexClient *webexteams.Client, roomID string,
	logger logr.Logger) {
	usageReports, err := utils.BuildUCSUsageReports(ctx, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get usage reports. Err: %v", err))
		return
	}

	var reportFiles []string

	// Analyze per pod, memory and cpu variance.
	reportFiles = analyzeUsageVariance(ctx, usageReports, logger)
	if len(reportFiles) > 0 {
		textMessage := "Hello I detected something which I believe needs to be looked at.  \n"
		textMessage += "For the reports in the plot the relative standard deviation is too big.  \n"
		logger.Info(textMessage)
		//sendAlertForReport(webexClient, roomID, textMessage, reportFiles, logger)
	}

	// Analyze per pod memory usage compared to memory limit.
	reportFiles = analyzeMemoryUsage(ctx, usageReports, logger)
	if len(reportFiles) > 0 {
		textMessage := "Hello I detected something which I believe needs to be looked at.  \n"
		textMessage += "For the reports in the plot, the max memory usage is too close to memory limit. Please consider increasing limit.  \n"
		sendAlertForReport(webexClient, roomID, textMessage, reportFiles, logger)
	}

	// Analyze per pod memory usage compared to memory limit.
	reportFiles = analyzeMemoryUsageWithNoLimit(ctx, usageReports, logger)
	if len(reportFiles) > 0 {
		textMessage := "Hello I detected something which I believe needs to be looked at.  \n"
		textMessage += "For the reports in the plot, the max memory usage is too high and no memory limit is defined. Please consider adding requets and limits.  \n"
		sendAlertForReport(webexClient, roomID, textMessage, reportFiles, logger)
	}
}

// analyzeMemoryUsage considers all pods for which memory usage was collected.
// If pod memory usage is too close to limit, generate a plot with collected samples.
func analyzeMemoryUsage(ctx context.Context, reports []string, logger logr.Logger) []string {
	reportFiles := make([]string, 0)

	for i := range reports {
		podName := &reports[i]
		// Get reports for a given pod
		data, err := getUsageReportData(ctx, *podName, logger)
		if err != nil {
			continue
		}

		if len(data) < numberOfAvailableRuns {
			logger.Info(fmt.Sprintf("Not enough available runs for pod %q", reports[i]))
			continue
		}

		memorySamples := getMemorySamples(data)
		_, max := minMax(memorySamples)

		// data[0] is from last available run, so always use it.
		if data[0].MemoryLimit != 0 && max >= 0.9*float64(data[0].MemoryLimit) {
			logger.Info(fmt.Sprintf("Max memory consumption (%f) is too close to memory limit (%d)", max, data[0].MemoryLimit))
			mean, _ := stat.MeanStdDev(memorySamples, nil)
			environment := "ucs"
			fileName := CreateMemoryPlot(&environment, podName, memorySamples, mean, float64(data[0].MemoryLimit), logger)
			reportFiles = append(reportFiles, fileName)
		}
	}

	return reportFiles
}

// analyzeMemoryUsageWithNoLimit considers all pods for which memory usage was collected.
// If pod memory usage is too high and no limit is defined, generate a plot with collected samples.
func analyzeMemoryUsageWithNoLimit(ctx context.Context, reports []string, logger logr.Logger) []string {
	reportFiles := make([]string, 0)

	for i := range reports {
		podName := &reports[i]
		// Get reports for a given pod
		data, err := getUsageReportData(ctx, *podName, logger)
		if err != nil {
			continue
		}

		if len(data) < numberOfAvailableRuns {
			logger.Info(fmt.Sprintf("Not enough available runs for pod %q", reports[i]))
			continue
		}

		memorySamples := getMemorySamples(data)
		_, max := minMax(memorySamples)

		// data[0] is from last available run, so always use it.
		if data[0].MemoryLimit == 0 {
			if max > maxMemory {
				logger.Info(fmt.Sprintf("Max memory consumption (%f) is too high and no memory limit is defined. Please considere adding mremory limit/request",
					max))
				mean, _ := stat.MeanStdDev(memorySamples, nil)
				environment := "ucs"
				fileName := CreateMemoryPlot(&environment, podName, memorySamples, mean, float64(data[0].MemoryLimit), logger)
				reportFiles = append(reportFiles, fileName)
			} else {
				logger.Info(fmt.Sprintf("Pod: %s memory limit not set. Skip analyzing it", reports[i]))
			}
			continue
		}
	}

	return reportFiles
}

// analyzeUsageVariance considers all pods for which (memory and cpu) usage was collected.
// Considering all collected pod samples if there is too much variance, generate a plot with samples.
func analyzeUsageVariance(ctx context.Context, reports []string, logger logr.Logger) []string {
	reportFiles := make([]string, 0)

	for i := range reports {
		// Get usage reports for a given pod
		data, err := getUsageReportData(ctx, reports[i], logger)
		if err != nil {
			continue
		}

		if len(data) < numberOfAvailableRuns {
			continue
		}

		if fileName := analyzeMemoryVariance(reports[i], data, logger); fileName != "" {
			reportFiles = append(reportFiles, fileName)
		}

		if fileName := analyzeCPUVariance(reports[i], data, logger); fileName != "" {
			reportFiles = append(reportFiles, fileName)
		}
	}

	return reportFiles
}

// getUsageReportData for a given pod, returns usage considering the last 30 runs,
// filtering out any run older than two weeks.
// Returns a slice with all usage.
func getUsageReportData(ctx context.Context, podInfo string, logger logr.Logger) ([]es_utils.UsageReport, error) {
	ucsUsageReports, err := es_utils.GetUsageReports(ctx, logger,
		"",      // no specific run
		podInfo, // for this specific report type
		false,   // no vcs
		true,    // only ucs
		100)     // consider the last 100 runs. We have an average of 3 runs per week. Setting this higher.
	// Runs older than two weeks will be discarded later on.

	if err != nil {
		logger.Info(fmt.Sprintf("Failed to get usage report  data for pod %q. Error %v", podInfo, err))
		return nil, err
	}

	var rtyp es_utils.UsageReport
	data := make([]es_utils.UsageReport, 0)
	for _, item := range ucsUsageReports.Each(reflect.TypeOf(rtyp)) {
		r := item.(es_utils.UsageReport)

		// Discard runs older than two week
		lastValidTime := time.Now().Add(-14 * 24 * time.Hour)
		if r.CreatedTime.After(lastValidTime) {
			data = append(data, r)
		}
	}

	return data, nil
}

func getMemorySamples(data []es_utils.UsageReport) []float64 {
	memory := make([]float64, 0)
	for j := range data {
		memory = append(memory, float64(data[j].Memory))
	}

	return memory
}

func getCPUSamples(data []es_utils.UsageReport) []float64 {
	cpu := make([]float64, 0)
	for j := range data {
		cpu = append(cpu, float64(data[j].CPU))
	}

	return cpu
}

func analyzeMemoryVariance(pod string, data []es_utils.UsageReport, logger logr.Logger) string {
	memorySamples := getMemorySamples(data)
	mean, std := stat.MeanStdDev(memorySamples, nil)

	if mean <= 1 {
		logger.Info(fmt.Sprintf("Usage Report for pod: %s Mean: %f is too low. Skip analyzing rsd", pod, mean))
		return ""
	}

	rsd := std * 100 / mean
	logger.Info(fmt.Sprintf("Usage Report for pod: %s Mean: %f Standard Deviation: %f Relative Standard Deviation: %f",
		pod, mean, std, rsd))

	if rsd >= memoryRsdThreshold {
		environment := "ucs"
		fileName := CreateMemoryPlot(&environment, &pod, memorySamples, mean, float64(data[0].MemoryLimit), logger)
		return fileName
	}

	return ""
}

func analyzeCPUVariance(pod string, data []es_utils.UsageReport, logger logr.Logger) string {
	cpuSamples := getCPUSamples(data)
	mean, std := stat.MeanStdDev(cpuSamples, nil)

	if mean <= 10 {
		logger.Info(fmt.Sprintf("Usage Report for pod: %s Mean: %f is too low. Skip analyzing rsd", pod, mean))
		return ""
	}

	rsd := std * 100 / mean
	logger.Info(fmt.Sprintf("Usage Report for pod: %s Mean: %f Standard Deviation: %f Relative Standard Deviation: %f",
		pod, mean, std, rsd))

	if rsd >= cpuRsdThreshold {
		fileName := createCPUPlot(&pod, cpuSamples, mean, float64(data[0].CPULimit), logger)
		return fileName
	}

	return ""
}

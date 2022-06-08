package analyze

import (
	"fmt"
	"image/color"

	"github.com/go-logr/logr"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

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

func createDurationPlot(testName *string, data []float64, mean float64, logger logr.Logger) string {
	logger.Info(fmt.Sprintf("Generate duration plot for test %s", *testName))

	pts := make(plotter.XYs, len(data))
	for i := range data {
		pts[i].X = float64(i)
		pts[i].Y = data[i]
	}

	p := plot.New()

	p.Title.Text = *testName
	p.X.Label.Text = "Run ID"
	p.Y.Label.Text = "Time in minute"

	min, max := minMax(data)
	p.Y.Max = max + 5
	p.Y.Min = min - 5
	p.X.Max = float64(len(data) + 3)

	err := plotutil.AddLinePoints(p, "Duration", pts)

	meanPlot := plotter.NewFunction(func(x float64) float64 { return mean })
	meanPlot.Color = color.RGBA{B: 255, A: 255}
	p.Add(meanPlot)
	p.Legend.Add("Mean", meanPlot)

	if err != nil {
		panic(err)
	}

	fileName := fmt.Sprintf("/tmp/duration_%s.png", *testName)
	// Save the plot to a PNG file.
	if err := p.Save(4*vg.Inch, 4*vg.Inch, fileName); err != nil {
		panic(err)
	}

	return fileName
}

func getGridSize(numberOfPlots int) (x, y int) {
	if numberOfPlots == 1 {
		x = 1
		y = 1
	} else if numberOfPlots == 2 {
		x = 2
		y = 1
	} else if numberOfPlots <= 4 {
		x = 2
		y = 2
	} else { // this is max number of plot we can put on grid and still have it readable
		x = 3
		y = 3
	}

	return
}

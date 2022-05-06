package analyze

import "time"

type jobTicker struct {
	timer *time.Timer
}

func (t *jobTicker) updateTimer() {
	nextTick := time.Date(time.Now().Year(), time.Now().Month(),
		time.Now().Day(), tickAtHour, tickAtMinute, tickAtSecond, 0, time.Local)
	if !nextTick.After(time.Now()) {
		nextTick = nextTick.Add(intervalPeriod)
	}
	diff := time.Until(time.Now())
	if t.timer == nil {
		t.timer = time.NewTimer(diff)
	} else {
		t.timer.Reset(diff)
	}
}

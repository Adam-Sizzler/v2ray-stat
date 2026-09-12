package scheduler

import (
	"testing"
	"time"

	cron "github.com/netresearch/go-cron"
)

func TestCronSpecParsing(t *testing.T) {
	c := cron.New(cron.WithLocation(time.UTC))
	id, err := c.AddFunc(CronCRMInfraBillingNodesNotifications, func() {})
	if err != nil {
		t.Fatalf("AddFunc error: %v", err)
	}
	entry := c.Entry(id)
	now := time.Date(2026, 9, 11, 16, 59, 0, 0, time.UTC)
	next := entry.Schedule.Next(now)
	t.Logf("Next run after %s: %s", now, next)
	next2 := entry.Schedule.Next(next)
	t.Logf("Next run after %s: %s", next, next2)
}

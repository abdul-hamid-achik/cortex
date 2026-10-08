package casefs

import (
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func TestJobIdentityFieldsPersist(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	job := domain.Job{ID: "job_id", TaskID: c.ID, Kind: "investigate", Question: "q", Status: domain.JobQueued, CreatedAt: time.Now().UTC(), Token: "tok"}
	if err := s.AppendJob(c.ID, job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateJob(c.ID, job.ID, func(j *domain.Job) error { j.PID, j.ProcessStart = 77, &start; return nil }); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.Jobs(c.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v %v", jobs, err)
	}
	if jobs[0].Token != "tok" || jobs[0].PID != 77 || jobs[0].ProcessStart == nil || !jobs[0].ProcessStart.Equal(start) {
		t.Errorf("identity fields lost on round trip: %+v", jobs[0])
	}
}

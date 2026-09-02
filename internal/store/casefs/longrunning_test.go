package casefs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abdul-hamid-achik/cortex/internal/domain"
)

func storeFinding(taskID, id string) domain.Finding {
	now := time.Now().UTC()
	return domain.Finding{ID: id, TaskID: taskID, Kind: domain.FindingBug, Severity: "medium", Title: "t " + id, Status: domain.FindingOpen, CreatedAt: now, UpdatedAt: now}
}

func TestFindingsRoundTripAndUpdate(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Findings(c.ID); err != nil || len(got) != 0 {
		t.Fatalf("empty findings = %v %v", got, err)
	}
	if err := s.AppendFinding(c.ID, storeFinding("task_OTHER", "fnd_1")); err == nil {
		t.Fatal("finding owned by another task must be rejected")
	}
	if err := s.AppendFinding(c.ID, storeFinding(c.ID, "fnd_1")); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFinding(c.ID, storeFinding(c.ID, "fnd_1")); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate id error = %v", err)
	}
	updated, err := s.UpdateFinding(c.ID, "fnd_1", func(f *domain.Finding) error {
		f.Status, f.Reason = domain.FindingDismissed, "not a bug"
		return nil
	})
	if err != nil || updated.Status != domain.FindingDismissed {
		t.Fatalf("update = %+v %v", updated, err)
	}
	if _, err := s.UpdateFinding(c.ID, "fnd_1", func(f *domain.Finding) error { f.Status = domain.FindingConverted; return nil }); err == nil {
		t.Fatal("update that breaks validation must be rejected")
	}
	if _, err := s.UpdateFinding(c.ID, "fnd_zz", func(*domain.Finding) error { return nil }); !errors.Is(err, ErrFindingNotFound) {
		t.Fatalf("missing finding error = %v", err)
	}
	if _, err := s.Findings("bogus"); err == nil {
		t.Fatal("invalid task id must be rejected")
	}
}

func TestCoverageLedgerPersistence(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadCoverage(c.ID); !errors.Is(err, ErrNoCoverage) {
		t.Fatalf("absent ledger error = %v", err)
	}
	if _, err := s.UpdateCoverage(c.ID, func(*domain.CoverageLedger) error { return nil }); !errors.Is(err, ErrNoCoverage) {
		t.Fatalf("update of absent ledger error = %v", err)
	}
	ledger := domain.CoverageLedger{SchemaVersion: 1, Source: "git_tree", BuiltAt: time.Now().UTC(), Modules: []domain.ModuleCoverage{{Path: "src", Status: domain.ModuleUnseen, FanIn: 1}}}
	if err := s.SaveCoverage(c.ID, ledger); err != nil {
		t.Fatal(err)
	}
	got, err := s.UpdateCoverage(c.ID, func(l *domain.CoverageLedger) error {
		l.Modules[0].Status = domain.ModuleExplored
		return nil
	})
	if err != nil || got.Modules[0].Status != domain.ModuleExplored {
		t.Fatalf("update = %+v %v", got, err)
	}
	loaded, err := s.LoadCoverage(c.ID)
	if err != nil || loaded.Source != "git_tree" || loaded.Modules[0].Status != domain.ModuleExplored {
		t.Fatalf("load = %+v %v", loaded, err)
	}
	if err := s.SaveCoverage("bogus", ledger); err == nil {
		t.Fatal("invalid task id must be rejected")
	}
}

func TestWorkplanPersistenceValidates(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	plan, err := s.LoadWorkplan(c.ID)
	if err != nil || len(plan.Items) != 0 || plan.SchemaVersion != 1 {
		t.Fatalf("empty plan = %+v %v", plan, err)
	}
	now := time.Now().UTC()
	plan, err = s.UpdateWorkplan(c.ID, func(p *domain.Workplan) error {
		p.Items = append(p.Items, domain.WorkItem{ID: "a", Goal: "g", Mode: domain.ModeChange, Status: domain.WorkItemPending, CreatedAt: now})
		return nil
	})
	if err != nil || len(plan.Items) != 1 {
		t.Fatalf("update = %+v %v", plan, err)
	}
	if _, err := s.UpdateWorkplan(c.ID, func(p *domain.Workplan) error {
		p.Items = append(p.Items, domain.WorkItem{ID: "b", Goal: "g", Mode: domain.ModeChange, DependsOn: []string{"zz"}, CreatedAt: now})
		return nil
	}); err == nil {
		t.Fatal("unknown dependency must be rejected at write time")
	}
	again, _ := s.LoadWorkplan(c.ID)
	if len(again.Items) != 1 {
		t.Fatalf("rejected write must not persist: %+v", again)
	}
}

func TestJobsPersistenceAndLogPath(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := domain.Job{ID: "job_1", TaskID: c.ID, Kind: "investigate", Question: "q", Status: domain.JobQueued, CreatedAt: now}
	if err := s.AppendJob(c.ID, job); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendJob(c.ID, job); err == nil {
		t.Fatal("duplicate job id must be rejected")
	}
	other := job
	other.TaskID = "task_OTHER"
	if err := s.AppendJob(c.ID, other); err == nil {
		t.Fatal("job owned by another task must be rejected")
	}
	updated, err := s.UpdateJob(c.ID, "job_1", func(j *domain.Job) error { j.Status = domain.JobRunning; j.PID = 42; return nil })
	if err != nil || updated.Status != domain.JobRunning || updated.PID != 42 {
		t.Fatalf("update = %+v %v", updated, err)
	}
	if _, err := s.UpdateJob(c.ID, "job_zz", func(*domain.Job) error { return nil }); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing job error = %v", err)
	}
	jobs, err := s.Jobs(c.ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v %v", jobs, err)
	}
	logPath, err := s.JobLogPath(c.ID, "job_1")
	if err != nil || filepath.Base(logPath) != "job_1.log" || !strings.Contains(logPath, filepath.Join(c.ID, "jobs")) {
		t.Fatalf("log path = %q %v", logPath, err)
	}
	if _, err := s.JobLogPath(c.ID, "../escape"); err == nil {
		t.Fatal("unsafe job id must be rejected")
	}
}

func TestCheckpointRoundTripAndBound(t *testing.T) {
	s := newStore(t)
	c := sampleCase()
	if err := s.Create(c); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadCheckpoint(c.ID); err != nil || got != "" {
		t.Fatalf("absent checkpoint = %q %v", got, err)
	}
	if err := s.WriteCheckpoint(c.ID, "# packet\n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ReadCheckpoint(c.ID); got != "# packet\n" {
		t.Fatalf("checkpoint = %q", got)
	}
	huge := strings.Repeat("x", maxCheckpointBytes+100)
	if err := s.WriteCheckpoint(c.ID, huge); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ReadCheckpoint(c.ID)
	if len(got) > maxCheckpointBytes+64 || !strings.Contains(got, "truncated") {
		t.Fatalf("oversized checkpoint should be truncated with a marker (len %d)", len(got))
	}
	if err := s.WriteCheckpoint("bogus", "x"); err == nil {
		t.Fatal("invalid task id must be rejected")
	}
}

func TestRepoStoreDossierUpdateRendersMarkdown(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repos", "x")
	if _, err := NewRepoStore(""); err == nil {
		t.Fatal("empty root must be rejected")
	}
	r, err := NewRepoStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Root() != root {
		t.Errorf("root = %q", r.Root())
	}
	d, err := r.LoadDossier()
	if err != nil || len(d.Entries) != 0 || d.SchemaVersion != 1 {
		t.Fatalf("empty dossier = %+v %v", d, err)
	}
	now := time.Now().UTC()
	rendered := ""
	d, err = r.UpdateDossier(now, func(d *domain.Dossier) error {
		d.Repository = "x"
		d.Entries = append(d.Entries, domain.DossierEntry{ID: "dos_1", Module: "src", Kind: domain.DossierArchitecture, Title: "t", Summary: "s", CreatedAt: now, UpdatedAt: now})
		return nil
	}, func(d domain.Dossier) string { rendered = "# " + d.Repository; return rendered })
	if err != nil || len(d.Entries) != 1 || d.UpdatedAt.IsZero() {
		t.Fatalf("update = %+v %v", d, err)
	}
	if data, err := readFileLimited(filepath.Join(root, "dossier.md"), 1<<20); err != nil || string(data) != rendered {
		t.Fatalf("dossier.md = %q %v", data, err)
	}
	if _, err := r.UpdateDossier(now, func(d *domain.Dossier) error {
		d.Entries = append(d.Entries, domain.DossierEntry{ID: "dos_2"})
		return nil
	}, nil); err == nil {
		t.Fatal("invalid entry must be rejected")
	}
	again, _ := r.LoadDossier()
	if len(again.Entries) != 1 {
		t.Fatalf("rejected write must not persist: %+v", again)
	}
}

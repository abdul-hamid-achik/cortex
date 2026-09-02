package domain

import (
	"strings"
	"testing"
	"time"
)

func TestSurveyModeIsValid(t *testing.T) {
	if !ModeSurvey.Valid() || Mode("campaign").Valid() {
		t.Fatal("survey must be a valid mode and unknown modes must not")
	}
}

func TestFindingValidation(t *testing.T) {
	now := time.Now().UTC()
	good := Finding{ID: "fnd_1", TaskID: "task_1", Kind: FindingBug, Severity: "high", Title: "t", Status: FindingOpen, CreatedAt: now, UpdatedAt: now}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid finding rejected: %v", err)
	}
	cases := map[string]Finding{
		"no id":        {TaskID: "task_1", Kind: FindingBug, Severity: "low", Title: "t", Status: FindingOpen, CreatedAt: now},
		"no task":      {ID: "f", Kind: FindingBug, Severity: "low", Title: "t", Status: FindingOpen, CreatedAt: now},
		"no title":     {ID: "f", TaskID: "task_1", Kind: FindingBug, Severity: "low", Title: " ", Status: FindingOpen, CreatedAt: now},
		"bad kind":     {ID: "f", TaskID: "task_1", Kind: "smell", Severity: "low", Title: "t", Status: FindingOpen, CreatedAt: now},
		"bad severity": {ID: "f", TaskID: "task_1", Kind: FindingBug, Severity: "urgent", Title: "t", Status: FindingOpen, CreatedAt: now},
		"bad status":   {ID: "f", TaskID: "task_1", Kind: FindingBug, Severity: "low", Title: "t", Status: "done", CreatedAt: now},
		"no reason":    {ID: "f", TaskID: "task_1", Kind: FindingBug, Severity: "low", Title: "t", Status: FindingDismissed, CreatedAt: now},
		"no child":     {ID: "f", TaskID: "task_1", Kind: FindingBug, Severity: "low", Title: "t", Status: FindingConverted, CreatedAt: now},
		"no timestamp": {ID: "f", TaskID: "task_1", Kind: FindingBug, Severity: "low", Title: "t", Status: FindingOpen},
	}
	for name, f := range cases {
		if err := f.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	if !FindingKind("feature").Valid() || FindingKind("x").Valid() || !FindingStatus("triaged").Valid() || FindingStatus("x").Valid() {
		t.Error("kind/status vocabularies")
	}
	if !FindingConverted.Terminal() || FindingOpen.Terminal() {
		t.Error("terminal statuses")
	}
	if got := (Finding{ID: "fnd_AB-1"}).CriterionID(); got != "fnd_ab_1" {
		t.Errorf("criterion id = %q", got)
	}
}

func TestDossierEntryValidationAndViews(t *testing.T) {
	now := time.Now().UTC()
	e := DossierEntry{ID: "dos_1", Module: "internal/kernel", Kind: DossierInvariant, Title: "t", Summary: "s", CreatedAt: now}
	if err := e.Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	for name, bad := range map[string]DossierEntry{
		"no id":      {Module: "m", Kind: DossierHotspot, Title: "t", Summary: "s", CreatedAt: now},
		"no module":  {ID: "d", Kind: DossierHotspot, Title: "t", Summary: "s", CreatedAt: now},
		"bad kind":   {ID: "d", Module: "m", Kind: "rumor", Title: "t", Summary: "s", CreatedAt: now},
		"no title":   {ID: "d", Module: "m", Kind: DossierHotspot, Summary: "s", CreatedAt: now},
		"no summary": {ID: "d", Module: "m", Kind: DossierHotspot, Title: "t", CreatedAt: now},
		"no created": {ID: "d", Module: "m", Kind: DossierHotspot, Title: "t", Summary: "s"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	d := Dossier{Entries: []DossierEntry{e, {ID: "dos_2", Module: "internal/kernel/", Stale: true}, {ID: "dos_3", Module: "cmd"}}}
	if len(d.Fresh()) != 2 || len(d.ForModule("/internal/kernel")) != 2 {
		t.Errorf("fresh=%d forModule=%d", len(d.Fresh()), len(d.ForModule("/internal/kernel")))
	}
}

func TestCoverageLedgerNextSummaryAndOwner(t *testing.T) {
	l := CoverageLedger{Modules: []ModuleCoverage{
		{Path: "internal/cli", Status: ModuleUnseen, FanIn: 5},
		{Path: "internal/kernel", Status: ModuleUnseen, FanIn: 9},
		{Path: ".", Status: ModuleExplored, Rounds: 2},
	}}
	l.Sort()
	if l.Modules[0].Path != "internal/kernel" {
		t.Errorf("sort by fan-in: %+v", l.Modules)
	}
	if next := l.Next(); next == nil || next.Path != "internal/kernel" {
		t.Errorf("next should be the highest fan-in unseen module: %+v", next)
	}
	s := l.Summary()
	if s.Total != 3 || s.Unseen != 2 || s.Explored != 1 || s.Next != "internal/kernel" || s.Percent < 33 || s.Percent > 34 {
		t.Errorf("summary = %+v", s)
	}
	if l.Find("internal/kernel/") == nil || l.Find("nope") != nil {
		t.Error("find")
	}
	if o := l.Owner("internal/kernel/status.go"); o == nil || o.Path != "internal/kernel" {
		t.Errorf("owner = %+v", o)
	}
	if o := l.Owner("README.md"); o == nil || o.Path != "." {
		t.Errorf("root owner = %+v", o)
	}
	for i := range l.Modules {
		l.Modules[i].Status = ModuleExplored
		l.Modules[i].Rounds = 3 - i
	}
	if next := l.Next(); next == nil || next.Rounds != 1 {
		t.Errorf("once everything is seen, next is the least-visited explored module: %+v", next)
	}
	if (CoverageLedger{}).Next() != nil || (CoverageLedger{}).Summary().Total != 0 {
		t.Error("empty ledger")
	}
	for in, want := range map[string]string{"": ".", "./": ".", "a\\b/": "a/b", "/x/y/": "x/y"} {
		if got := NormalizeModulePath(in); got != want {
			t.Errorf("normalize %q = %q, want %q", in, got, want)
		}
	}
}

func TestWorkplanValidation(t *testing.T) {
	now := time.Now().UTC()
	item := func(id string, deps ...string) WorkItem {
		return WorkItem{ID: id, Goal: "g " + id, Mode: ModeChange, Status: WorkItemPending, DependsOn: deps, CreatedAt: now}
	}
	ok := Workplan{Items: []WorkItem{item("a"), item("b", "a"), item("c", "a", "b")}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	if ok.Find("b") == nil || ok.Find("zz") != nil {
		t.Error("find")
	}
	for name, bad := range map[string]Workplan{
		"no id":       {Items: []WorkItem{{Goal: "g", Mode: ModeChange}}},
		"no goal":     {Items: []WorkItem{{ID: "a", Mode: ModeChange}}},
		"bad mode":    {Items: []WorkItem{{ID: "a", Goal: "g", Mode: "x"}}},
		"dup id":      {Items: []WorkItem{item("a"), item("a")}},
		"self dep":    {Items: []WorkItem{item("a", "a")}},
		"unknown dep": {Items: []WorkItem{item("a", "zz")}},
		"cycle":       {Items: []WorkItem{item("a", "b"), item("b", "c"), item("c", "a")}},
	} {
		err := bad.Validate()
		if err == nil {
			t.Errorf("%s: expected a validation error", name)
		} else if name == "cycle" && !strings.Contains(err.Error(), "cycle") {
			t.Errorf("cycle error = %v", err)
		}
	}
}

func TestJobValidation(t *testing.T) {
	now := time.Now().UTC()
	good := Job{ID: "job_1", TaskID: "task_1", Kind: "investigate", Question: "q", Status: JobQueued, CreatedAt: now}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	for name, bad := range map[string]Job{
		"no id":      {TaskID: "task_1", Kind: "investigate", Question: "q", Status: JobQueued, CreatedAt: now},
		"no task":    {ID: "j", Kind: "investigate", Question: "q", Status: JobQueued, CreatedAt: now},
		"bad kind":   {ID: "j", TaskID: "task_1", Kind: "deploy", Question: "q", Status: JobQueued, CreatedAt: now},
		"no work":    {ID: "j", TaskID: "task_1", Kind: "investigate", Status: JobQueued, CreatedAt: now},
		"bad status": {ID: "j", TaskID: "task_1", Kind: "investigate", Question: "q", Status: "paused", CreatedAt: now},
		"no created": {ID: "j", TaskID: "task_1", Kind: "investigate", Question: "q", Status: JobQueued},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	if !JobDone.Terminal() || !JobFailed.Terminal() || !JobCanceled.Terminal() || JobRunning.Terminal() || JobQueued.Terminal() {
		t.Error("terminal job statuses")
	}
}

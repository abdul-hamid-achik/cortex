package adapters

import "testing"

func TestJunkDiscoveryPath(t *testing.T) {
	cases := []struct {
		path string
		junk bool
	}{
		{".agent/cases/task_x/commands.jsonl", true},
		{"worker/src/jobs/profile.ts", false},
		{"dist/bundle.js", true},
		{"web-app/src/components/ProfileWrap.vue", false},
		{"generic in .agent/cases/task_06/commands.jsonl (score 1.00)", true},
	}
	for _, tc := range cases {
		if got := JunkDiscoveryPath(tc.path); got != tc.junk {
			t.Errorf("JunkDiscoveryPath(%q)=%v want %v", tc.path, got, tc.junk)
		}
	}
}

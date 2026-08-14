package lifecycle

import (
	"path/filepath"
	"testing"
)

func TestFileJobsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "jobs.json")
	first, err := NewFileJobs(path)
	if err != nil {
		t.Fatalf("NewFileJobs() error = %v", err)
	}
	first.Put(Job{ID: "durable-1", Kind: "provision", State: Running, Completed: map[string]bool{"backup": true}})

	second, err := NewFileJobs(path)
	if err != nil {
		t.Fatalf("NewFileJobs() after restart error = %v", err)
	}
	job, ok := second.Get("durable-1")
	if !ok || !job.Completed["backup"] {
		t.Fatalf("reloaded job = %#v, ok=%v", job, ok)
	}
}

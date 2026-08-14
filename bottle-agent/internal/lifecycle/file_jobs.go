package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileJobs persists resumable job checkpoints with atomic replace semantics. The
// containing directory must be root-owned and mode 0700 in production.
type FileJobs struct {
	mu   sync.Mutex
	path string
	jobs map[string]Job
}

func NewFileJobs(path string) (*FileJobs, error) {
	f := &FileJobs{path: path, jobs: map[string]Job{}}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(payload, &f.jobs); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *FileJobs) Get(id string) (Job, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job, ok := f.jobs[id]
	return cloneJob(job), ok
}

func (f *FileJobs) Put(job Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job.UpdatedAt = time.Now().UTC()
	f.jobs[job.ID] = cloneJob(job)
	if err := f.flush(); err != nil {
		panic("persist lifecycle job: " + err.Error())
	}
}

func (f *FileJobs) flush() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil {
		return err
	}
	payload, err := json.Marshal(f.jobs)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".jobs-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, f.path)
}

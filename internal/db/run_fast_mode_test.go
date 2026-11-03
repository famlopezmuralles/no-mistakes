package db

import (
	"path/filepath"
	"testing"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

func TestRunFastModePinnedOnlyBeforeExecution(t *testing.T) {
	d := openTestDB(t)
	repo, err := d.InsertRepo("/repo", "https://example.com/repo.git", "main")
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := d.InsertRun(repo.ID, "ordinary", "head", "base")
	if err != nil {
		t.Fatal(err)
	}
	fast, err := d.InsertRun(repo.ID, "fast", "head", "base")
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.FastMode || fast.FastMode {
		t.Fatal("new runs must default to ordinary mode")
	}
	if err := d.SetRunFastMode(fast.ID); err != nil {
		t.Fatal(err)
	}
	for _, run := range []*Run{ordinary, fast} {
		if err := d.UpdateRunStatus(run.ID, types.RunRunning); err != nil {
			t.Fatal(err)
		}
		if err := d.SetRunFastMode(run.ID); err == nil {
			t.Fatal("active run policy was mutable")
		}
		got, err := d.GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.FastMode != (run.ID == fast.ID) {
			t.Fatalf("persisted FastMode = %v for %s", got.FastMode, run.Branch)
		}
	}
}

func TestRunFastModeMigrationAndLegacyRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := d.InsertRepo("/repo", "https://example.com/repo.git", "main")
	if err != nil {
		t.Fatal(err)
	}
	run, err := d.InsertRun(repo.ID, "feature", "head", "base")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.sql.Exec(`ALTER TABLE runs DROP COLUMN fast_mode`); err != nil {
		t.Fatal(err)
	}
	// Read-only callers can inspect a pre-migration database without enabling
	// fast mode, before the writable daemon has a chance to migrate it.
	got, err := d.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FastMode {
		t.Fatal("legacy run read as fast")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err = d.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FastMode {
		t.Fatal("migration converted an existing run to fast")
	}
	if err := d.SetRunFastMode(run.ID); err != nil {
		t.Fatal(err)
	}
}

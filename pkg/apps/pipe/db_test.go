package pipe

import (
	"log/slog"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/db/postgres/pgtest"
)

var testDB *PsqlDB

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

func cleanupTestData(t *testing.T) {
	t.Helper()
	if testDB == nil {
		testDB = NewDB(postgres.NewDB(pgtest.URL(t), slog.New(slog.DiscardHandler)))
	}
	pgtest.Clean(t, testDB.Db)
}

func TestUpsertPipeMonitor(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("pipemonitorowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pipemonitorowner", "comment", "")

	winEnd := time.Now().Add(time.Hour)
	err := testDB.UpsertPipeMonitor(user.ID, "test-topic", 5*time.Minute, &winEnd)
	if err != nil {
		t.Fatalf("UpsertPipeMonitor failed: %v", err)
	}

	monitor, err := testDB.FindPipeMonitorByTopic(user.ID, "test-topic")
	if err != nil {
		t.Fatalf("FindPipeMonitorByTopic failed: %v", err)
	}
	if monitor.Topic != "test-topic" {
		t.Errorf("expected topic 'test-topic', got '%s'", monitor.Topic)
	}
	if monitor.WindowDur != 5*time.Minute {
		t.Errorf("expected window_dur 5m, got %v", monitor.WindowDur)
	}
}

func TestUpsertPipeMonitor_Update(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("pipeupdateowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pipeupdateowner", "comment", "")

	winEnd1 := time.Now().Add(time.Hour)
	err := testDB.UpsertPipeMonitor(user.ID, "update-topic", 5*time.Minute, &winEnd1)
	if err != nil {
		t.Fatalf("first UpsertPipeMonitor failed: %v", err)
	}

	winEnd2 := time.Now().Add(2 * time.Hour)
	err = testDB.UpsertPipeMonitor(user.ID, "update-topic", 10*time.Minute, &winEnd2)
	if err != nil {
		t.Fatalf("second UpsertPipeMonitor failed: %v", err)
	}

	monitor, err := testDB.FindPipeMonitorByTopic(user.ID, "update-topic")
	if err != nil {
		t.Fatalf("FindPipeMonitorByTopic failed: %v", err)
	}
	if monitor.WindowDur != 10*time.Minute {
		t.Errorf("expected window_dur 10m after update, got %v", monitor.WindowDur)
	}
}

func TestUpdatePipeMonitorLastPing(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("pipepingowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pipepingowner", "comment", "")

	winEnd := time.Now().Add(time.Hour)
	err := testDB.UpsertPipeMonitor(user.ID, "ping-topic", 5*time.Minute, &winEnd)
	if err != nil {
		t.Fatalf("UpsertPipeMonitor failed: %v", err)
	}

	lastPing := time.Now()
	err = testDB.UpdatePipeMonitorLastPing(user.ID, "ping-topic", &lastPing)
	if err != nil {
		t.Fatalf("UpdatePipeMonitorLastPing failed: %v", err)
	}

	monitor, err := testDB.FindPipeMonitorByTopic(user.ID, "ping-topic")
	if err != nil {
		t.Fatalf("FindPipeMonitorByTopic failed: %v", err)
	}
	if monitor.LastPing == nil {
		t.Error("expected last_ping to be set, got nil")
	}
}

func TestRemovePipeMonitor(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("piperemoveowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI piperemoveowner", "comment", "")

	winEnd := time.Now().Add(time.Hour)
	err := testDB.UpsertPipeMonitor(user.ID, "remove-topic", 5*time.Minute, &winEnd)
	if err != nil {
		t.Fatalf("UpsertPipeMonitor failed: %v", err)
	}

	err = testDB.RemovePipeMonitor(user.ID, "remove-topic")
	if err != nil {
		t.Fatalf("RemovePipeMonitor failed: %v", err)
	}

	_, err = testDB.FindPipeMonitorByTopic(user.ID, "remove-topic")
	if err == nil {
		t.Error("expected error finding removed monitor, got nil")
	}
}

func TestFindPipeMonitorByTopic_NotFound(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("pipenotfoundowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pipenotfoundowner", "comment", "")

	_, err := testDB.FindPipeMonitorByTopic(user.ID, "nonexistent-topic")
	if err == nil {
		t.Error("expected error for nonexistent monitor, got nil")
	}
}

func TestFindPipeMonitorsByUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("pipemonlistowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pipemonlistowner", "comment", "")

	winEnd := time.Now().Add(time.Hour)
	_ = testDB.UpsertPipeMonitor(user.ID, "service-a", 5*time.Minute, &winEnd)
	_ = testDB.UpsertPipeMonitor(user.ID, "service-b", 10*time.Minute, &winEnd)
	_ = testDB.UpsertPipeMonitor(user.ID, "service-c", 1*time.Hour, &winEnd)

	monitors, err := testDB.FindPipeMonitorsByUser(user.ID)
	if err != nil {
		t.Fatalf("FindPipeMonitorsByUser failed: %v", err)
	}

	if len(monitors) != 3 {
		t.Errorf("expected 3 monitors, got %d", len(monitors))
	}

	// Should be ordered by topic
	if monitors[0].Topic != "service-a" {
		t.Errorf("expected first topic 'service-a', got %s", monitors[0].Topic)
	}
	if monitors[1].Topic != "service-b" {
		t.Errorf("expected second topic 'service-b', got %s", monitors[1].Topic)
	}
	if monitors[2].Topic != "service-c" {
		t.Errorf("expected third topic 'service-c', got %s", monitors[2].Topic)
	}
}

func TestFindPipeMonitorsByUser_Empty(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("pipenomonitors", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pipenomonitors", "comment", "")

	monitors, err := testDB.FindPipeMonitorsByUser(user.ID)
	if err != nil {
		t.Fatalf("FindPipeMonitorsByUser failed: %v", err)
	}

	if len(monitors) != 0 {
		t.Errorf("expected 0 monitors for user with none, got %d", len(monitors))
	}
}

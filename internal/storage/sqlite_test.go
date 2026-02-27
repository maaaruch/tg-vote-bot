package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Чтобы SQLite в тестах был максимально предсказуемый
	db.SetMaxOpenConns(1)

	s := New(db)
	if err := s.InitSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return s, db
}

func mustCount(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	return n
}

func TestStore_CreateRoom_AndJoinByPassword(t *testing.T) {
	s, _ := newTestStore(t)

	roomID, err := s.CreateRoom(777, "test room", "secret")
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	// ok password
	room, err := s.GetRoomByIDAndPassword(roomID, "secret")
	if err != nil {
		t.Fatalf("GetRoomByIDAndPassword(ok): %v", err)
	}
	if room.ID != roomID || room.OwnerUserID != 777 || room.Title != "test room" {
		t.Fatalf("unexpected room: %+v", room)
	}

	// wrong password -> ErrNotFound
	_, err = s.GetRoomByIDAndPassword(roomID, "nope")
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestStore_RecordVote_UpsertPerNomination(t *testing.T) {
	s, db := newTestStore(t)

	roomID, _ := s.CreateRoom(1, "room", "pw")
	nomID, _ := s.CreateNomination(roomID, "Best dev", "desc")
	aliceID, _ := s.CreateNominee(nomID, "Alice")
	bobID, _ := s.CreateNominee(nomID, "Bob")

	userHash := "hash_user_1"

	// 1) vote Alice
	if err := s.RecordVote(userHash, nomID, aliceID, time.Unix(100, 0)); err != nil {
		t.Fatalf("RecordVote(alice): %v", err)
	}

	// 2) revote Bob (must overwrite)
	if err := s.RecordVote(userHash, nomID, bobID, time.Unix(200, 0)); err != nil {
		t.Fatalf("RecordVote(bob): %v", err)
	}

	// In votes should be ровно 1 строка для (userHash, nomID)
	if got := mustCount(t, db, `SELECT COUNT(*) FROM votes WHERE user_hash = ? AND nomination_id = ?`, userHash, nomID); got != 1 {
		t.Fatalf("expected 1 vote row, got %d", got)
	}

	// Results: Bob = 1, Alice = 0
	results, err := s.ResultsByNomination(nomID)
	if err != nil {
		t.Fatalf("ResultsByNomination: %v", err)
	}

	var aliceVotes, bobVotes int64
	for _, r := range results {
		if r.ID == aliceID {
			aliceVotes = r.Votes
		}
		if r.ID == bobID {
			bobVotes = r.Votes
		}
	}
	if aliceVotes != 0 || bobVotes != 1 {
		t.Fatalf("unexpected votes: alice=%d bob=%d (results=%+v)", aliceVotes, bobVotes, results)
	}
}

func TestStore_DeleteNominee_CascadesVotes(t *testing.T) {
	s, db := newTestStore(t)

	roomID, _ := s.CreateRoom(1, "room", "pw")
	nomID, _ := s.CreateNomination(roomID, "Nom", "")
	nomineeID, _ := s.CreateNominee(nomID, "Victim")

	if err := s.RecordVote("u1", nomID, nomineeID, time.Now()); err != nil {
		t.Fatalf("RecordVote: %v", err)
	}
	if got := mustCount(t, db, `SELECT COUNT(*) FROM votes`); got != 1 {
		t.Fatalf("expected 1 vote before delete, got %d", got)
	}

	deleted, err := s.DeleteNominee(nomineeID)
	if err != nil {
		t.Fatalf("DeleteNominee: %v", err)
	}
	if !deleted {
		t.Fatalf("expected deleted=true")
	}

	// Vote должен исчезнуть по ON DELETE CASCADE
	if got := mustCount(t, db, `SELECT COUNT(*) FROM votes`); got != 0 {
		t.Fatalf("expected 0 votes after nominee delete, got %d", got)
	}
}

func TestStore_DeleteNomination_CascadesNomineesAndVotes(t *testing.T) {
	s, db := newTestStore(t)

	roomID, _ := s.CreateRoom(1, "room", "pw")
	nomID, _ := s.CreateNomination(roomID, "Nom", "")
	n1, _ := s.CreateNominee(nomID, "A")
	n2, _ := s.CreateNominee(nomID, "B")

	_ = s.RecordVote("u1", nomID, n1, time.Now())
	_ = s.RecordVote("u2", nomID, n2, time.Now())

	if got := mustCount(t, db, `SELECT COUNT(*) FROM nominees WHERE nomination_id = ?`, nomID); got != 2 {
		t.Fatalf("expected 2 nominees, got %d", got)
	}
	if got := mustCount(t, db, `SELECT COUNT(*) FROM votes WHERE nomination_id = ?`, nomID); got != 2 {
		t.Fatalf("expected 2 votes, got %d", got)
	}

	deleted, err := s.DeleteNomination(nomID)
	if err != nil {
		t.Fatalf("DeleteNomination: %v", err)
	}
	if !deleted {
		t.Fatalf("expected deleted=true")
	}

	if got := mustCount(t, db, `SELECT COUNT(*) FROM nominees WHERE nomination_id = ?`, nomID); got != 0 {
		t.Fatalf("expected 0 nominees after delete, got %d", got)
	}
	if got := mustCount(t, db, `SELECT COUNT(*) FROM votes WHERE nomination_id = ?`, nomID); got != 0 {
		t.Fatalf("expected 0 votes after delete, got %d", got)
	}
}